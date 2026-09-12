package aigateway

import (
	"testing"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/aigateway"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func grantedService(t *testing.T) *Service {
	t.Helper()
	s := NewService(zerolog.Nop())
	if err := s.GrantScope("acct-1", "transfer", 10000, 70, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantScope("acct-1", "balance", 10000, 100, testNow); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIntentHappyPath(t *testing.T) {
	s := grantedService(t)
	in, err := s.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "1000", "risk_score": "5",
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != shared.StatusPendingConfirmation {
		t.Fatalf("mutating stages confirmation: %+v", in)
	}
	got, _ := s.Get(in.ID)
	if err := s.Confirm(in.ID, got.ConfirmToken, testNow); err != nil {
		t.Fatal(err)
	}
	outcome, reason, err := s.Execute(in.ID, testNow)
	if err != nil || outcome != "executed" || reason == "" {
		t.Fatalf("executed: %s %q %v", outcome, reason, err)
	}
}

func TestUnconsentedBlocked(t *testing.T) {
	s := grantedService(t)
	if _, err := s.SubmitIntent("transfer", map[string]string{"recipient": "stranger"}, testNow); err != shared.ErrNoConsent {
		t.Fatalf("unconsented: %v", err)
	}
}

func TestOverLimitBlocked(t *testing.T) {
	s := grantedService(t)
	if _, err := s.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "50000",
	}, testNow); err != shared.ErrOverLimit {
		t.Fatalf("over-limit: %v", err)
	}
}

func TestConfirmationExpiry(t *testing.T) {
	s := grantedService(t)
	in, err := s.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "100",
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(in.ID)
	if err := s.Confirm(in.ID, got.ConfirmToken, testNow.Add(shared.ConfirmTTL+time.Minute)); err != shared.ErrConfirmExpired {
		t.Fatalf("expiry: %v", err)
	}
}

func TestRevokeAndReadOnly(t *testing.T) {
	s := grantedService(t)
	if err := s.RevokeScope("acct-1", "transfer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitIntent("transfer", map[string]string{"recipient": "acct-1"}, testNow); err != shared.ErrNoConsent {
		t.Fatalf("revoked: %v", err)
	}
	// Read-only still flows without confirmation.
	in, err := s.SubmitIntent("balance", map[string]string{"recipient": "acct-1"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	outcome, _, err := s.Execute(in.ID, testNow)
	if err != nil || outcome != "executed" {
		t.Fatalf("read-only: %s %v", outcome, err)
	}
	if _, err := s.SubmitIntent("", map[string]string{"recipient": "a"}, testNow); err == nil {
		t.Fatal("empty action must fail")
	}
}
