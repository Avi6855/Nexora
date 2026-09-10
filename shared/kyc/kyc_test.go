package kyc

import (
	"errors"
	"testing"
	"time"
)

func TestRequiredTierByRisk(t *testing.T) {
	e := NewEngine(DefaultRequirements())
	cases := map[RiskLevel]Tier{
		RiskLow:    TierBasic,
		RiskMedium: TierStandard,
		RiskHigh:   TierEnhanced,
	}
	for risk, want := range cases {
		got, err := e.RequiredTier(risk)
		if err != nil || got != want {
			t.Errorf("%s → tier %d, %v; want %d", risk, got, err, want)
		}
	}
	if _, err := e.RequiredTier(RiskProhibited); !errors.Is(err, ErrProhibited) {
		t.Fatal("prohibited risk must return ErrProhibited")
	}
}

func TestEvaluateUpgradePath(t *testing.T) {
	e := NewEngine(DefaultRequirements())
	// Low-risk customer at basic tier: compliant.
	ev, err := e.Evaluate(Customer{ID: "c1", CurrentTier: TierBasic, RiskLevel: RiskLow})
	if err != nil || !ev.Compliant {
		t.Fatalf("basic/low must comply: %+v %v", ev, err)
	}
	// Risk escalates to MEDIUM: upgrade required.
	ev, err = e.Evaluate(Customer{ID: "c1", CurrentTier: TierBasic, RiskLevel: RiskMedium})
	if err != nil || ev.Compliant || ev.UpgradeTo != TierStandard {
		t.Fatalf("risk escalation must demand upgrade: %+v %v", ev, err)
	}
	// Open investigation blocks the upgrade path.
	ev, _ = e.Evaluate(Customer{ID: "c1", CurrentTier: TierBasic, RiskLevel: RiskMedium, OpenInvestigations: true})
	if ev.BlockedBy != "open_investigation" {
		t.Fatalf("blocked_by = %q", ev.BlockedBy)
	}
}

func TestBalanceCapEnforced(t *testing.T) {
	e := NewEngine(DefaultRequirements())
	// £2,500 on a STANDARD tier (cap £20,000) is fine; but a BASIC customer
	// (cap £1,000) holding £2,500 is not, even with matching risk.
	ev, err := e.Evaluate(Customer{ID: "c", CurrentTier: TierBasic, RiskLevel: RiskLow, BalanceMinor: 250_000})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if ev.Compliant || ev.BlockedBy != "balance_exceeds_tier" {
		t.Fatalf("balance cap must trip: %+v", ev)
	}
}

func TestEvidenceFreshnessWindows(t *testing.T) {
	now := time.Now()
	e := NewFreshnessEngine(DefaultPolicies())
	// Identity doc 6 years old: stale (refresh window 5y, expiry 10y).
	f, err := e.Classify(EvidenceItem{Type: EvidenceIdentityDoc, CollectedAt: now.Add(-6 * 365 * 24 * time.Hour)})
	if err != nil || f != RequiresRefresh {
		t.Fatalf("6y identity = %s, %v; want REQUIRES_REFRESH", f, err)
	}
	// Identity doc 11 years old: expired.
	f, _ = e.Classify(EvidenceItem{Type: EvidenceIdentityDoc, CollectedAt: now.Add(-11 * 365 * 24 * time.Hour)})
	if f != Expired {
		t.Fatalf("11y identity = %s, want EXPIRED", f)
	}
	// Address proof 13 months old: requires refresh (12m window).
	f, _ = e.Classify(EvidenceItem{Type: EvidenceAddress, CollectedAt: now.Add(-13 * 30 * 24 * time.Hour)})
	if f != RequiresRefresh {
		t.Fatalf("13m address = %s, want REQUIRES_REFRESH", f)
	}
	// Sanctions screening 2 days old: expired (7d hard expiry, 24h refresh).
	f, _ = e.Classify(EvidenceItem{Type: EvidenceSanctionsScreen, CollectedAt: now.Add(-2 * 24 * time.Hour)})
	if f != RequiresRefresh {
		t.Fatalf("2d sanctions = %s, want REQUIRES_REFRESH", f)
	}
	f, _ = e.Classify(EvidenceItem{Type: EvidenceSanctionsScreen, CollectedAt: now.Add(-8 * 24 * time.Hour)})
	if f != Expired {
		t.Fatalf("8d sanctions = %s, want EXPIRED", f)
	}
}

func TestUnknownEvidenceType(t *testing.T) {
	e := NewFreshnessEngine(nil)
	if _, err := e.Classify(EvidenceItem{Type: "MYSTERY", CollectedAt: time.Now()}); err == nil {
		t.Fatal("unknown evidence type must error")
	}
}

func TestTierEvidenceRequirements(t *testing.T) {
	now := time.Now()
	e := NewFreshnessEngine(DefaultPolicies())
	freshID := EvidenceItem{Type: EvidenceIdentityDoc, CollectedAt: now.Add(-30 * 24 * time.Hour)}
	freshAddr := EvidenceItem{Type: EvidenceAddress, CollectedAt: now.Add(-30 * 24 * time.Hour)}
	staleID := EvidenceItem{Type: EvidenceIdentityDoc, CollectedAt: now.Add(-11 * 365 * 24 * time.Hour)}

	// STANDARD needs fresh ID only.
	ok, missing, err := e.SatisfiedForTier(TierStandard, []EvidenceItem{freshID})
	if err != nil || !ok || len(missing) != 0 {
		t.Fatalf("standard with fresh ID: ok=%v missing=%v err=%v", ok, missing, err)
	}
	// ENHANCED needs address too.
	ok, missing, _ = e.SatisfiedForTier(TierEnhanced, []EvidenceItem{freshID})
	if ok || len(missing) != 1 || missing[0] != EvidenceAddress {
		t.Fatalf("enhanced without address: ok=%v missing=%v", ok, missing)
	}
	// Expired ID never satisfies.
	ok, _, _ = e.SatisfiedForTier(TierStandard, []EvidenceItem{staleID})
	if ok {
		t.Fatal("expired ID must not satisfy STANDARD")
	}
	// BUSINESS needs all three.
	ok, missing, _ = e.SatisfiedForTier(TierBusiness, []EvidenceItem{freshID, freshAddr})
	if ok || len(missing) != 1 || missing[0] != EvidenceBusinessOwner {
		t.Fatalf("business missing ownership: ok=%v missing=%v", ok, missing)
	}
}

func TestClassifyAllSortsByUrgency(t *testing.T) {
	now := time.Now()
	e := NewFreshnessEngine(DefaultPolicies())
	items := []EvidenceItem{
		{Type: EvidenceIdentityDoc, CollectedAt: now.Add(-30 * 24 * time.Hour)},    // fresh
		{Type: EvidenceAddress, CollectedAt: now.Add(-13 * 30 * 24 * time.Hour)},   // refresh
		{Type: EvidenceSanctionsScreen, CollectedAt: now.Add(-8 * 24 * time.Hour)}, // expired
	}
	got, err := e.ClassifyAll(items)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got[0].Freshness != Expired || got[1].Freshness != RequiresRefresh || got[2].Freshness != Fresh {
		t.Fatalf("urgency order wrong: %+v", got)
	}
}
