package cardtokens

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	sharedtokens "github.com/nexora/nexora/shared/cardtokens"
)

func testService() *Service {
	return NewService(zerolog.Nop())
}

func TestServiceLifecycleSuspendResumeRevoke(t *testing.T) {
	svc := testService()
	tok, err := svc.Issue("fp-svc-1", sharedtokens.Scope{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Suspend(tok.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(tok.ID)
	if got.Status != sharedtokens.IssuerStatusSuspended {
		t.Fatalf("want SUSPENDED, got %s", got.Status)
	}
	if _, err := svc.Resume(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Revoke(tok.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(tok.ID)
	if got.Status != sharedtokens.IssuerStatusRevoked {
		t.Fatalf("want REVOKED, got %s", got.Status)
	}
	if _, err := svc.Resume(tok.ID); !errors.Is(err, sharedtokens.ErrIllegalIssuerMove) {
		t.Fatalf("revoked resume must conflict, got %v", err)
	}
	if _, err := svc.Get("missing"); !errors.Is(err, sharedtokens.ErrIssuerTokenNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestServiceRotationRemapAtomic(t *testing.T) {
	svc := testService()
	old, err := svc.Issue("fp-svc-rot", sharedtokens.Scope{Merchants: []string{"NETFLIX"}}, time.Hour, []string{"cof-1", "cof-2"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := svc.Rotate(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != sharedtokens.IssuerStatusActive {
		t.Fatalf("successor ACTIVE, got %s", next.Status)
	}
	if len(next.Dependents) != 2 {
		t.Fatalf("dependents remapped, got %+v", next.Dependents)
	}
	prev, _ := svc.Get(old.ID)
	if prev.Status != sharedtokens.IssuerStatusRotating || len(prev.Dependents) != 0 {
		t.Fatalf("old drained to ROTATING: %+v", prev)
	}
}

func TestServiceScopeDenyAndNarrow(t *testing.T) {
	svc := testService()
	scope := sharedtokens.Scope{
		Merchants:      []string{"NETFLIX"},
		Countries:      []string{"UK"},
		MaxAmountMinor: 5000,
		WindowStart:    time.Now().UTC().Add(-time.Hour),
		WindowEnd:      time.Now().UTC().Add(time.Hour),
	}
	tok, err := svc.Issue("fp-svc-scope", scope, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	okCtx := sharedtokens.AuthContext{Merchant: "NETFLIX", Country: "UK", AmountMinor: 100, At: now}
	if d, _, _ := svc.Authorize(tok.ID, okCtx); d != sharedtokens.DecisionAllow {
		t.Fatal("in-scope must ALLOW")
	}
	denies := []sharedtokens.AuthContext{
		{Merchant: "AMAZON", Country: "UK", AmountMinor: 100, At: now},
		{Merchant: "NETFLIX", Country: "US", AmountMinor: 100, At: now},
		{Merchant: "NETFLIX", Country: "UK", AmountMinor: 6000, At: now},
		{Merchant: "NETFLIX", Country: "UK", AmountMinor: 100, At: now.Add(2 * time.Hour)},
	}
	for i, c := range denies {
		if d, reason, _ := svc.Authorize(tok.ID, c); d != sharedtokens.DecisionDeny || reason == "" {
			t.Fatalf("deny %d: %s %q", i, d, reason)
		}
	}
	narrow := sharedtokens.Scope{Merchants: []string{"NETFLIX"}, Countries: []string{"UK"}, MaxAmountMinor: 1000, WindowStart: scope.WindowStart, WindowEnd: scope.WindowEnd}
	if _, err := svc.NarrowScope(tok.ID, narrow); err != nil {
		t.Fatalf("narrow: %v", err)
	}
	wide := sharedtokens.Scope{Merchants: []string{"NETFLIX", "AMAZON"}, Countries: []string{"UK"}, MaxAmountMinor: 1000, WindowStart: scope.WindowStart, WindowEnd: scope.WindowEnd}
	if _, err := svc.NarrowScope(tok.ID, wide); !errors.Is(err, sharedtokens.ErrIssuerScopeWiden) {
		t.Fatalf("widen must fail, got %v", err)
	}
}
