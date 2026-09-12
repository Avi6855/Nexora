package delegated

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func grantTest(t *testing.T, s *Service, caps []string, set string) *Grant {
	t.Helper()
	g, err := s.Grant("avi@example.com", caps, testNow, testNow.Add(7*24*time.Hour), set, testNow)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	return g
}

func TestGrantValidation(t *testing.T) {
	s := NewService(zerolog.Nop())
	if _, err := s.Grant("", []string{CapViewBalance}, testNow, testNow.Add(time.Hour), AccountsHousehold, testNow); !errors.Is(err, ErrGranteeRequired) {
		t.Fatalf("empty grantee must fail: %v", err)
	}
	if _, err := s.Grant("a@b.c", nil, testNow, testNow.Add(time.Hour), AccountsHousehold, testNow); !errors.Is(err, ErrCapabilityRequired) {
		t.Fatalf("empty capabilities must fail: %v", err)
	}
	if _, err := s.Grant("a@b.c", []string{"NOPE"}, testNow, testNow.Add(time.Hour), AccountsHousehold, testNow); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("unknown capability must fail: %v", err)
	}
	for _, denied := range DeniedCapabilities {
		if _, err := s.Grant("a@b.c", []string{denied}, testNow, testNow.Add(time.Hour), AccountsHousehold, testNow); !errors.Is(err, ErrDeniedCapability) {
			t.Fatalf("deny-list capability %q must fail at grant: %v", denied, err)
		}
	}
	if _, err := s.Grant("a@b.c", []string{CapViewBalance}, testNow, testNow, AccountsHousehold, testNow); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("empty window must fail: %v", err)
	}
	if _, err := s.Grant("a@b.c", []string{CapViewBalance}, testNow, testNow.Add(91*24*time.Hour), AccountsHousehold, testNow); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("window over 90 days must fail: %v", err)
	}
	if _, err := s.Grant("a@b.c", []string{CapViewBalance}, testNow, testNow.Add(time.Hour), "village", testNow); !errors.Is(err, ErrInvalidAccountSet) {
		t.Fatalf("unknown account set must fail: %v", err)
	}
	if _, err := s.Grant("a@b.c", []string{CapViewBalance}, testNow, testNow.Add(time.Hour), "", testNow); !errors.Is(err, ErrAccountSetRequired) {
		t.Fatalf("missing account set must fail: %v", err)
	}
}

func TestUseAllow(t *testing.T) {
	s := NewService(zerolog.Nop())
	g := grantTest(t, s, []string{CapViewBalance, CapViewTransactions}, AccountsHousehold)
	allowed, reason, err := s.Use(g.ID, CapViewBalance, AccountsHousehold, testNow.Add(time.Hour))
	if err != nil || !allowed {
		t.Fatalf("expected ALLOW: %v %q %v", allowed, reason, err)
	}
}

func TestUseOutOfScopeDenied(t *testing.T) {
	s := NewService(zerolog.Nop())
	g := grantTest(t, s, []string{CapViewBalance}, AccountsHousehold)
	allowed, _, err := s.Use(g.ID, CapDownloadStatements, AccountsHousehold, testNow.Add(time.Hour))
	if err != nil || allowed {
		t.Fatalf("out-of-scope capability must DENY: %v %v", allowed, err)
	}
	if allowed, _, err := s.Use(g.ID, DenyMakePayments, AccountsHousehold, testNow.Add(time.Hour)); err != nil || allowed {
		t.Fatalf("deny-list capability must DENY on use: %v %v", allowed, err)
	}
	if _, _, err := s.Use("missing", CapViewBalance, AccountsHousehold, testNow); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("unknown grant must fail: %v", err)
	}
}

func TestUseExpiredDenied(t *testing.T) {
	s := NewService(zerolog.Nop())
	g, err := s.Grant("avi@example.com", []string{CapViewBalance}, testNow, testNow.Add(time.Hour), AccountsHousehold, testNow)
	if err != nil {
		t.Fatal(err)
	}
	allowed, reason, err := s.Use(g.ID, CapViewBalance, AccountsHousehold, testNow.Add(2*time.Hour))
	if err != nil || allowed {
		t.Fatalf("expired grant must DENY: %v %q %v", allowed, reason, err)
	}
	if allowed, _, err := s.Use(g.ID, CapViewBalance, AccountsHousehold, testNow.Add(-time.Hour)); err != nil || allowed {
		t.Fatalf("pre-window use must DENY: %v %v", allowed, err)
	}
}

func TestUseRevokedDenied(t *testing.T) {
	s := NewService(zerolog.Nop())
	g := grantTest(t, s, []string{CapViewBalance}, AccountsChild)
	if err := s.Revoke(g.ID, testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(g.ID, testNow.Add(2*time.Hour)); !errors.Is(err, ErrAlreadyRevoked) {
		t.Fatalf("double revoke must conflict: %v", err)
	}
	allowed, reason, err := s.Use(g.ID, CapViewBalance, AccountsChild, testNow.Add(3*time.Hour))
	if err != nil || allowed {
		t.Fatalf("revoked grant must DENY: %v %q %v", allowed, reason, err)
	}
	if err := s.Revoke("missing", testNow); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("revoke unknown must fail: %v", err)
	}
}

func TestUseWrongAccountSetDenied(t *testing.T) {
	s := NewService(zerolog.Nop())
	g := grantTest(t, s, []string{CapViewBalance}, AccountsChild)
	if allowed, _, err := s.Use(g.ID, CapViewBalance, AccountsBusiness, testNow.Add(time.Hour)); err != nil || allowed {
		t.Fatalf("business view on child-only grant must DENY: %v %v", allowed, err)
	}
	if allowed, _, err := s.Use(g.ID, CapViewBalance, AccountsHousehold, testNow.Add(time.Hour)); err != nil || allowed {
		t.Fatalf("household view on child-only grant must DENY: %v %v", allowed, err)
	}
	if allowed, _, err := s.Use(g.ID, CapViewBalance, AccountsChild, testNow.Add(time.Hour)); err != nil || !allowed {
		t.Fatalf("child view on child-only grant must ALLOW: %v %v", allowed, err)
	}
}

func TestUsageAudit(t *testing.T) {
	s := NewService(zerolog.Nop())
	g := grantTest(t, s, []string{CapViewBalance}, AccountsBusiness)
	if _, _, err := s.Use(g.ID, CapViewBalance, AccountsBusiness, testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Use(g.ID, CapDownloadStatements, AccountsBusiness, testNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Audit(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 usage entries: %+v", entries)
	}
	if !entries[0].Allowed || entries[1].Allowed {
		t.Fatalf("first ALLOW then DENY expected: %+v", entries)
	}
	if entries[0].Capability != CapViewBalance || entries[0].AccountSet != AccountsBusiness ||
		!entries[0].When.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("first entry mismatch: %+v", entries[0])
	}
	if _, err := s.Audit("missing"); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("audit unknown must fail: %v", err)
	}
}
