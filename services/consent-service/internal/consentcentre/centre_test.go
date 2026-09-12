package consentcentre

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func connectTestProvider(t *testing.T, s *Service, name string) {
	t.Helper()
	if _, err := s.Connect(name, "budgeting insights", nil, testNow.Add(30*24*time.Hour), testNow); err != nil {
		t.Fatalf("connect %s: %v", name, err)
	}
}

func TestConnectAndCheckAllow(t *testing.T) {
	s := NewService(zerolog.Nop())
	p, err := s.Connect("HSBC", "budgeting insights", map[string]bool{"balance": true}, testNow.Add(30*24*time.Hour), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if p.Toggles[DataBalance] != true || p.Toggles[DataSell] != false {
		t.Fatalf("toggles must default unset datatypes to false: %+v", p.Toggles)
	}
	allowed, reason, err := s.Check("HSBC", "balance", "budget-agent", "budgeting insights", "TCK-1", testNow)
	if err != nil || !allowed {
		t.Fatalf("expected ALLOW: %v %q %v", allowed, reason, err)
	}
	if _, _, err := s.Check("HSBC", "NOPE", "w", "p", "t", testNow); !errors.Is(err, ErrInvalidDatatype) {
		t.Fatalf("unknown datatype must fail: %v", err)
	}
	if _, _, err := s.Check("Ghost", "balance", "w", "p", "t", testNow); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("unknown provider must fail: %v", err)
	}
}

func TestConnectValidation(t *testing.T) {
	s := NewService(zerolog.Nop())
	if _, err := s.Connect("", "p", nil, testNow.Add(time.Hour), testNow); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name must fail: %v", err)
	}
	if _, err := s.Connect("HSBC", "", nil, testNow.Add(time.Hour), testNow); !errors.Is(err, ErrPurposeRequired) {
		t.Fatalf("empty purpose must fail: %v", err)
	}
	if _, err := s.Connect("HSBC", "p", nil, testNow.Add(-time.Hour), testNow); !errors.Is(err, ErrExpiryRequired) {
		t.Fatalf("past expiry must fail: %v", err)
	}
	if _, err := s.Connect("HSBC", "p", map[string]bool{"NOPE": true}, testNow.Add(time.Hour), testNow); !errors.Is(err, ErrInvalidDatatype) {
		t.Fatalf("unknown datatype must fail: %v", err)
	}
	connectTestProvider(t, s, "HSBC")
	if _, err := s.Connect("HSBC", "other", nil, testNow.Add(time.Hour), testNow); !errors.Is(err, ErrProviderExists) {
		t.Fatalf("duplicate connect must conflict: %v", err)
	}
}

func TestToggleOffDenies(t *testing.T) {
	s := NewService(zerolog.Nop())
	connectTestProvider(t, s, "Amex")
	if err := s.SetToggle("Amex", "transactions", false, testNow); err != nil {
		t.Fatal(err)
	}
	allowed, reason, err := s.Check("Amex", "transactions", "agent", "p", "T-2", testNow)
	if err != nil || allowed {
		t.Fatalf("disabled datatype must DENY: %v %q %v", allowed, reason, err)
	}
	if err := s.SetToggle("Amex", "NOPE", true, testNow); !errors.Is(err, ErrInvalidDatatype) {
		t.Fatalf("bad datatype must fail: %v", err)
	}
	if err := s.SetToggle("Ghost", "balance", true, testNow); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("unknown provider must fail: %v", err)
	}
	// Re-enable restores access.
	if err := s.SetToggle("Amex", "transactions", true, testNow); err != nil {
		t.Fatal(err)
	}
	if allowed, _, err := s.Check("Amex", "transactions", "agent", "p", "T-3", testNow); err != nil || !allowed {
		t.Fatalf("re-enabled must ALLOW: %v %v", allowed, err)
	}
}

func TestRevokedAccessDenied(t *testing.T) {
	s := NewService(zerolog.Nop())
	connectTestProvider(t, s, "HSBC")
	if err := s.Revoke("HSBC", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke("HSBC", testNow); !errors.Is(err, ErrAlreadyRevoked) {
		t.Fatalf("double revoke must conflict: %v", err)
	}
	allowed, reason, err := s.Check("HSBC", "balance", "agent", "p", "T-9", testNow)
	if err != nil || allowed {
		t.Fatalf("revoked must DENY: %v %q %v", allowed, reason, err)
	}
	if err := s.SetToggle("HSBC", "balance", true, testNow); !errors.Is(err, ErrProviderRevoked) {
		t.Fatalf("toggle on revoked must fail: %v", err)
	}
	// Reconnect after revoke is allowed.
	if _, err := s.Connect("HSBC", "fresh purpose", nil, testNow.Add(time.Hour), testNow); err != nil {
		t.Fatalf("reconnect after revoke: %v", err)
	}
}

func TestExpiredGrantDeniedAndSwept(t *testing.T) {
	s := NewService(zerolog.Nop())
	if _, err := s.Connect("InvestCo", "portfolio view", nil, testNow.Add(time.Hour), testNow); err != nil {
		t.Fatal(err)
	}
	later := testNow.Add(2 * time.Hour)
	allowed, reason, err := s.Check("InvestCo", "holdings", "agent", "portfolio view", "T-4", later)
	if err != nil || allowed {
		t.Fatalf("expired must DENY: %v %q %v", allowed, reason, err)
	}
	swept := s.Sweep(later)
	found := false
	for _, n := range swept {
		if n == "InvestCo" {
			found = true
		}
	}
	if !found {
		// Already lazily marked expired by Check; a second sweep reports nothing new.
		if got := s.Sweep(later); len(got) != 0 {
			t.Fatalf("second sweep must be empty: %v", got)
		}
	}
	if err := s.SetToggle("InvestCo", "holdings", true, later); !errors.Is(err, ErrProviderExpired) {
		t.Fatalf("toggle on expired must fail: %v", err)
	}
}

func TestAccessLogQuery(t *testing.T) {
	s := NewService(zerolog.Nop())
	connectTestProvider(t, s, "HSBC")
	if _, _, err := s.Check("HSBC", "balance", "alice", "budgeting", "TCK-1", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SetToggle("HSBC", "sell", false, testNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Check("HSBC", "sell", "bob", "rebalance", "TCK-2", testNow); err != nil {
		t.Fatal(err)
	}
	log, err := s.AccessLog("HSBC")
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("expected 2 audit entries: %+v", log)
	}
	if log[0].Who != "alice" || log[0].What != "balance" || !log[0].When.Equal(testNow) ||
		log[0].Purpose != "budgeting" || log[0].Ticket != "TCK-1" || !log[0].Allowed {
		t.Fatalf("first entry mismatch: %+v", log[0])
	}
	if log[1].Allowed {
		t.Fatalf("sell was toggled off so second entry must be DENY: %+v", log[1])
	}
	if _, err := s.AccessLog("Ghost"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("unknown provider log must fail: %v", err)
	}
}
