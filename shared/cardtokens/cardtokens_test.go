package cardtokens

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func zerologNop() zerolog.Logger { return zerolog.Nop() }

var tokT0 = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func testVault() *IssuerVault {
	return NewIssuerVault(zerologNop())
}

func TestIssuerLifecycle(t *testing.T) {
	v := testVault()
	tok, err := v.Issue("fp-pan-1", Scope{}, time.Hour, tokT0)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Status != IssuerStatusActive {
		t.Fatalf("new token must be ACTIVE, got %s", tok.Status)
	}
	if _, err := v.Suspend(tok.ID, tokT0); err != nil {
		t.Fatal(err)
	}
	got, _ := v.Get(tok.ID)
	if got.Status != IssuerStatusSuspended {
		t.Fatalf("want SUSPENDED, got %s", got.Status)
	}
	if _, err := v.Resume(tok.ID, tokT0); err != nil {
		t.Fatal(err)
	}
	got, _ = v.Get(tok.ID)
	if got.Status != IssuerStatusActive {
		t.Fatalf("resume must return to ACTIVE, got %s", got.Status)
	}
	if _, err := v.Revoke(tok.ID, tokT0); err != nil {
		t.Fatal(err)
	}
	got, _ = v.Get(tok.ID)
	if got.Status != IssuerStatusRevoked {
		t.Fatalf("want REVOKED, got %s", got.Status)
	}
}

func TestIllegalTransitionsRejected(t *testing.T) {
	v := testVault()
	tok, _ := v.Issue("fp-pan-2", Scope{}, time.Hour, tokT0)
	if _, err := v.Resume(tok.ID, tokT0); !errors.Is(err, ErrIllegalIssuerMove) {
		t.Fatalf("ACTIVE resume must be illegal, got %v", err)
	}
	if _, err := v.Revoke(tok.ID, tokT0); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Suspend(tok.ID, tokT0); !errors.Is(err, ErrIllegalIssuerMove) {
		t.Fatalf("REVOKED suspend must be illegal, got %v", err)
	}
	if _, err := v.Resume(tok.ID, tokT0); !errors.Is(err, ErrIllegalIssuerMove) {
		t.Fatalf("REVOKED resume must be illegal, got %v", err)
	}
	if _, err := v.Rotate(tok.ID, tokT0); !errors.Is(err, ErrIllegalIssuerMove) {
		t.Fatalf("REVOKED rotate must be illegal, got %v", err)
	}
	if _, err := v.NarrowScope(tok.ID, Scope{Merchants: []string{"A"}}, tokT0); !errors.Is(err, ErrIllegalIssuerMove) {
		t.Fatalf("REVOKED rescope must be illegal, got %v", err)
	}
	// EXPIRED is terminal too.
	tok2, _ := v.Issue("fp-pan-3", Scope{}, time.Millisecond, tokT0)
	if n := v.SweepExpired(tokT0.Add(time.Hour)); n != 1 {
		t.Fatalf("sweep must expire 1, got %d", n)
	}
	if _, err := v.Resume(tok2.ID, tokT0); !errors.Is(err, ErrIllegalIssuerMove) {
		t.Fatalf("EXPIRED resume must be illegal, got %v", err)
	}
	if _, err := v.Get("missing"); !errors.Is(err, ErrIssuerTokenNotFound) {
		t.Fatalf("missing token: %v", err)
	}
}

func TestRotationRemapAtomic(t *testing.T) {
	v := testVault()
	old, _ := v.Issue("fp-pan-4", Scope{Merchants: []string{"NETFLIX"}}, time.Hour, tokT0)
	if _, err := v.AddDependent(old.ID, "cof-merchant-1", tokT0); err != nil {
		t.Fatal(err)
	}
	if _, err := v.AddDependent(old.ID, "cof-merchant-2", tokT0); err != nil {
		t.Fatal(err)
	}
	next, err := v.Rotate(old.ID, tokT0)
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != IssuerStatusActive {
		t.Fatalf("successor must be ACTIVE, got %s", next.Status)
	}
	if next.PrevID != old.ID {
		t.Fatalf("successor must link PrevID, got %+v", next)
	}
	if next.PANFingerprint != old.PANFingerprint {
		t.Fatal("rotation must preserve PAN fingerprint")
	}
	if len(next.Dependents) != 2 {
		t.Fatalf("dependents must remap to successor, got %+v", next.Dependents)
	}
	prev, _ := v.Get(old.ID)
	if prev.Status != IssuerStatusRotating {
		t.Fatalf("old must be ROTATING, got %s", prev.Status)
	}
	if prev.NextID != next.ID {
		t.Fatalf("old must link NextID, got %+v", prev)
	}
	if len(prev.Dependents) != 0 {
		t.Fatalf("old dependents must be drained, got %+v", prev.Dependents)
	}
	// Old ROTATING token no longer authorizes.
	if d, _, _ := v.Authorize(old.ID, AuthContext{Merchant: "NETFLIX", At: tokT0}); d != DecisionDeny {
		t.Fatal("ROTATING token must DENY")
	}
	if d, _, _ := v.Authorize(next.ID, AuthContext{Merchant: "NETFLIX", At: tokT0}); d != DecisionAllow {
		t.Fatal("successor must ALLOW in-scope merchant")
	}
}

func TestExpirySweep(t *testing.T) {
	v := testVault()
	a, _ := v.Issue("fp-a", Scope{}, time.Minute, tokT0)
	b, _ := v.Issue("fp-b", Scope{}, 2*time.Hour, tokT0)
	c, _ := v.Issue("fp-c", Scope{}, 0, tokT0) // no expiry
	if n := v.SweepExpired(tokT0.Add(30 * time.Minute)); n != 1 {
		t.Fatalf("want 1 swept, got %d", n)
	}
	ga, _ := v.Get(a.ID)
	if ga.Status != IssuerStatusExpired {
		t.Fatalf("a must be EXPIRED, got %s", ga.Status)
	}
	gb, _ := v.Get(b.ID)
	if gb.Status != IssuerStatusActive {
		t.Fatalf("b must stay ACTIVE, got %s", gb.Status)
	}
	gc, _ := v.Get(c.ID)
	if gc.Status != IssuerStatusActive {
		t.Fatalf("no-expiry token must stay ACTIVE, got %s", gc.Status)
	}
}

func TestScopeDenyCases(t *testing.T) {
	v := testVault()
	scope := Scope{
		Merchants:      []string{"NETFLIX"},
		Countries:      []string{"UK"},
		Channels:       []string{"ECOM"},
		Devices:        []string{"pixel-9"},
		MaxAmountMinor: 5000,
		WindowStart:    tokT0.Add(-time.Hour),
		WindowEnd:      tokT0.Add(time.Hour),
	}
	tok, _ := v.Issue("fp-scope", scope, 2*time.Hour, tokT0)
	okCtx := AuthContext{Merchant: "netflix", Country: "uk", Channel: "ecom", Device: "PIXEL-9", AmountMinor: 100, At: tokT0}
	if d, _, _ := v.Authorize(tok.ID, okCtx); d != DecisionAllow {
		t.Fatal("in-scope attempt must ALLOW")
	}
	cases := []AuthContext{
		{Merchant: "AMAZON", Country: "UK", Channel: "ECOM", Device: "pixel-9", AmountMinor: 100, At: tokT0},
		{Merchant: "NETFLIX", Country: "US", Channel: "ECOM", Device: "pixel-9", AmountMinor: 100, At: tokT0},
		{Merchant: "NETFLIX", Country: "UK", Channel: "POS", Device: "pixel-9", AmountMinor: 100, At: tokT0},
		{Merchant: "NETFLIX", Country: "UK", Channel: "ECOM", Device: "iphone", AmountMinor: 100, At: tokT0},
		{Merchant: "NETFLIX", Country: "UK", Channel: "ECOM", Device: "pixel-9", AmountMinor: 6000, At: tokT0},
		{Merchant: "NETFLIX", Country: "UK", Channel: "ECOM", Device: "pixel-9", AmountMinor: 100, At: tokT0.Add(2 * time.Hour)},
		{Merchant: "NETFLIX", Country: "UK", Channel: "ECOM", Device: "pixel-9", AmountMinor: 100, At: tokT0.Add(-2 * time.Hour)},
	}
	for i, c := range cases {
		if d, reason, _ := v.Authorize(tok.ID, c); d != DecisionDeny || reason == "" {
			t.Fatalf("case %d must DENY with reason, got %s %q", i, d, reason)
		}
	}
	// Suspended token denies even in scope.
	if _, err := v.Suspend(tok.ID, tokT0); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := v.Authorize(tok.ID, okCtx); d != DecisionDeny {
		t.Fatal("suspended token must DENY")
	}
}

func TestScopeNarrowNeverWidens(t *testing.T) {
	v := testVault()
	tok, _ := v.Issue("fp-narrow", Scope{
		Merchants:      []string{"A", "B"},
		Countries:      []string{"UK", "FR"},
		MaxAmountMinor: 10000,
		WindowStart:    tokT0.Add(-2 * time.Hour),
		WindowEnd:      tokT0.Add(2 * time.Hour),
	}, time.Hour, tokT0)
	// Narrowing each axis is fine.
	narrow := Scope{
		Merchants:      []string{"A"},
		Countries:      []string{"UK"},
		MaxAmountMinor: 1000,
		WindowStart:    tokT0.Add(-time.Hour),
		WindowEnd:      tokT0.Add(time.Hour),
	}
	if _, err := v.NarrowScope(tok.ID, narrow, tokT0); err != nil {
		t.Fatalf("narrowing must succeed: %v", err)
	}
	// Widening attempts all fail.
	widens := []Scope{
		{Merchants: []string{"A", "C"}, Countries: []string{"UK"}, MaxAmountMinor: 1000, WindowStart: narrow.WindowStart, WindowEnd: narrow.WindowEnd},
		{Merchants: []string{"A"}, Countries: []string{"UK", "US"}, MaxAmountMinor: 1000, WindowStart: narrow.WindowStart, WindowEnd: narrow.WindowEnd},
		{Merchants: []string{"A"}, Countries: []string{"UK"}, MaxAmountMinor: 5000, WindowStart: narrow.WindowStart, WindowEnd: narrow.WindowEnd},
		{Merchants: []string{"A"}, Countries: []string{"UK"}, MaxAmountMinor: 1000, WindowStart: narrow.WindowStart.Add(-2 * time.Hour), WindowEnd: narrow.WindowEnd},
		{Merchants: []string{}, Countries: []string{"UK"}, MaxAmountMinor: 1000, WindowStart: narrow.WindowStart, WindowEnd: narrow.WindowEnd},
		{Merchants: []string{"A"}, Countries: []string{"UK"}, MaxAmountMinor: 0, WindowStart: narrow.WindowStart, WindowEnd: narrow.WindowEnd},
	}
	for i, w := range widens {
		if _, err := v.NarrowScope(tok.ID, w, tokT0); !errors.Is(err, ErrIssuerScopeWiden) {
			t.Fatalf("widen %d must fail with ErrIssuerScopeWiden, got %v", i, err)
		}
	}
}
