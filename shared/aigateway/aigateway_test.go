package aigateway

import (
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func granted(t *testing.T) *Gateway {
	t.Helper()
	g := NewGateway()
	if err := g.GrantScope("acct-1", "transfer", 10000, 70, tnow); err != nil {
		t.Fatal(err)
	}
	if err := g.GrantScope("acct-1", "balance", 10000, 100, tnow); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestReadOnlyExecutesWithoutConfirmation(t *testing.T) {
	g := granted(t)
	in, err := g.SubmitIntent("balance", map[string]string{"recipient": "acct-1"}, tnow)
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != StatusReady {
		t.Fatalf("read-only must be ready: %+v", in)
	}
	outcome, reason, err := g.Execute(in.ID, tnow)
	if err != nil || outcome != "executed" || reason == "" {
		t.Fatalf("execute: %s %q %v", outcome, reason, err)
	}
}

func TestMutatingNeedsConfirmation(t *testing.T) {
	g := granted(t)
	in, err := g.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "5000", "risk_score": "10",
	}, tnow)
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != StatusPendingConfirmation || in.ConfirmToken == "" {
		t.Fatalf("mutating must stage confirmation: %+v", in)
	}
	if _, _, err := g.Execute(in.ID, tnow); err != ErrConfirmRequired {
		t.Fatalf("execute before confirm must fail: %v", err)
	}
	if err := g.Confirm(in.ID, "wrong", tnow); err != ErrBadToken {
		t.Fatalf("wrong token: %v", err)
	}
	got, _ := g.Get(in.ID)
	if err := g.Confirm(in.ID, got.ConfirmToken, tnow); err != nil {
		t.Fatal(err)
	}
	outcome, reason, err := g.Execute(in.ID, tnow)
	if err != nil || outcome != "executed" || reason == "" {
		t.Fatalf("execute after confirm: %s %q %v", outcome, reason, err)
	}
}

func TestUnconsentedBlocked(t *testing.T) {
	g := granted(t)
	if _, err := g.SubmitIntent("transfer", map[string]string{"recipient": "stranger"}, tnow); err != ErrNoConsent {
		t.Fatalf("unconsented recipient must block: %v", err)
	}
	if _, err := g.SubmitIntent("withdraw", map[string]string{"recipient": "acct-1"}, tnow); err != ErrNoConsent {
		t.Fatalf("ungranted action must block: %v", err)
	}
}

func TestOverLimitAndRiskBlocked(t *testing.T) {
	g := granted(t)
	if _, err := g.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "99999",
	}, tnow); err != ErrOverLimit {
		t.Fatalf("over-limit must block: %v", err)
	}
	if _, err := g.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "100", "risk_score": "95",
	}, tnow); err != ErrRiskBlocked {
		t.Fatalf("over-risk must block: %v", err)
	}
}

func TestConfirmationExpiry(t *testing.T) {
	g := granted(t)
	in, err := g.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "100",
	}, tnow)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := g.Get(in.ID)
	if err := g.Confirm(in.ID, got.ConfirmToken, tnow.Add(ConfirmTTL+time.Minute)); err != ErrConfirmExpired {
		t.Fatalf("expired token must fail: %v", err)
	}
	if _, _, err := g.Execute(in.ID, tnow.Add(ConfirmTTL+time.Minute)); err != ErrConfirmRequired {
		t.Fatalf("unconfirmed must not execute: %v", err)
	}
}

func TestRevokedScopeSkipsAtExecute(t *testing.T) {
	g := granted(t)
	in, err := g.SubmitIntent("transfer", map[string]string{
		"recipient": "acct-1", "amount_minor": "100",
	}, tnow)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := g.Get(in.ID)
	if err := g.Confirm(in.ID, got.ConfirmToken, tnow); err != nil {
		t.Fatal(err)
	}
	if err := g.RevokeScope("acct-1", "transfer"); err != nil {
		t.Fatal(err)
	}
	outcome, reason, err := g.Execute(in.ID, tnow)
	if err != nil || outcome != "skipped" || reason == "" {
		t.Fatalf("revoked scope must skip: %s %q %v", outcome, reason, err)
	}
}

func TestScopeValidation(t *testing.T) {
	g := NewGateway()
	if err := g.GrantScope("", "transfer", 100, 10, tnow); err == nil {
		t.Fatal("empty recipient must fail")
	}
	if err := g.GrantScope("a", "transfer", 0, 10, tnow); err == nil {
		t.Fatal("non-positive limit must fail")
	}
	if err := g.RevokeScope("ghost", "transfer"); err != ErrNoConsent {
		t.Fatalf("revoke unknown: %v", err)
	}
	if _, err := g.SubmitIntent("", map[string]string{"recipient": "a"}, tnow); err == nil {
		t.Fatal("empty action must fail")
	}
	if _, err := g.SubmitIntent("transfer", map[string]string{}, tnow); err == nil {
		t.Fatal("unresolvable recipient must fail")
	}
	if _, err := g.SubmitIntent("transfer", map[string]string{"recipient": "a", "amount_minor": "-5"}, tnow); err == nil {
		t.Fatal("negative amount must fail")
	}
}
