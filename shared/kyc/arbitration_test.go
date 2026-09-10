package kyc

import (
	"errors"
	"testing"
	"time"
)

func provider(name string, countries, docs []string, cost float64, conf float64, lat float64, avail bool) ProviderCapability {
	return ProviderCapability{
		Name: name, Countries: countries, DocumentTypes: docs,
		CostPerCheckGBP: cost, ExpectedConfidence: conf,
		ExpectedLatencyMs: lat, Available: avail,
	}
}

func TestArbitrateCoverageFirst(t *testing.T) {
	a := NewProviderArbiter()
	a.Register(provider("cheapGB", []string{"GB"}, []string{"PASSPORT"}, 0.10, 0.80, 5000, true))
	a.Register(provider("global", []string{"GB", "IN", "US"}, []string{"PASSPORT", "DRIVING_LICENCE"}, 1.50, 0.95, 2000, true))

	got, err := a.Arbitrate(VerificationRequest{Country: "GB", DocumentType: "PASSPORT"})
	if err != nil {
		t.Fatalf("arbitrate: %v", err)
	}
	// Default scoring weights confidence: global (0.95) beats cheapGB (0.80).
	if got.Provider != "global" {
		t.Fatalf("expected global, got %s (%s)", got.Provider, got.Reason)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got.Candidates))
	}
}

func TestArbitrateNoCoverage(t *testing.T) {
	a := NewProviderArbiter()
	a.Register(provider("gbOnly", []string{"GB"}, []string{"PASSPORT"}, 0.5, 0.9, 1000, true))
	_, err := a.Arbitrate(VerificationRequest{Country: "JP", DocumentType: "PASSPORT"})
	if !errors.Is(err, ErrNoProviderAvailable) {
		t.Fatalf("expected ErrNoProviderAvailable, got %v", err)
	}
}

func TestArbitrateFailover(t *testing.T) {
	a := NewProviderArbiter()
	a.Register(provider("primary", []string{"GB"}, []string{"PASSPORT"}, 0.5, 0.95, 1000, true))
	a.Register(provider("backup", []string{"GB"}, []string{"PASSPORT"}, 0.5, 0.80, 3000, true))

	got, _ := a.Arbitrate(VerificationRequest{Country: "GB", DocumentType: "PASSPORT"})
	if got.Provider != "primary" {
		t.Fatalf("expected primary, got %s", got.Provider)
	}
	a.Failover("primary")
	got, err := a.Arbitrate(VerificationRequest{Country: "GB", DocumentType: "PASSPORT"})
	if err != nil {
		t.Fatalf("failover should keep service: %v", err)
	}
	if got.Provider != "backup" {
		t.Fatalf("expected backup after failover, got %s", got.Provider)
	}
}

func TestArbitrateCostPreference(t *testing.T) {
	a := NewProviderArbiter()
	a.Register(provider("premium", []string{"GB"}, []string{"PASSPORT"}, 2.00, 0.99, 1000, true))
	a.Register(provider("budget", []string{"GB"}, []string{"PASSPORT"}, 0.05, 0.90, 5000, true))

	got, _ := a.Arbitrate(VerificationRequest{Country: "GB", DocumentType: "PASSPORT", PreferCost: true})
	if got.Provider != "budget" {
		t.Fatalf("cost preference should pick budget, got %s", got.Provider)
	}
}

func TestQueueAssignSkillAndLoad(t *testing.T) {
	b := NewQueueBalancer()
	reviewers := []Reviewer{
		{Name: "generalist", Languages: []string{"en"}, MaxComplexity: 5, CurrentLoad: 0, Capacity: 10},
		{Name: "specialist", Languages: []string{"en"}, ExpertiseCountries: []string{"GB"}, MaxComplexity: 5, CurrentLoad: 3, Capacity: 10},
	}
	c := ReviewCase{CaseID: "c1", Complexity: 3, Country: "GB", Language: "en",
		SLADeadline: time.Now().Add(48 * time.Hour)}
	got, err := b.Assign(c, reviewers)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Headroom (10 vs 7) outweighs expertise (10) in the default scoring, so
	// the idle generalist wins. The test pins that documented behaviour.
	if got.Reviewer != "generalist" {
		t.Fatalf("expected generalist (more headroom), got %s", got.Reviewer)
	}
}

func TestQueueAssignUrgentSLA(t *testing.T) {
	b := NewQueueBalancer()
	reviewers := []Reviewer{
		{Name: "busyExpert", Languages: []string{"en"}, ExpertiseCountries: []string{"GB"}, MaxComplexity: 5, CurrentLoad: 9, Capacity: 10},
		{Name: "idle", Languages: []string{"en"}, MaxComplexity: 5, CurrentLoad: 0, Capacity: 10},
	}
	c := ReviewCase{CaseID: "c2", Complexity: 4, Country: "GB", Language: "en",
		SLADeadline: time.Now().Add(2 * time.Hour)} // < 24h → urgent
	got, err := b.Assign(c, reviewers)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got.Reviewer != "busyExpert" {
		t.Fatalf("urgent SLA should prefer expertise, got %s", got.Reviewer)
	}
}

func TestQueueAssignNoEligible(t *testing.T) {
	b := NewQueueBalancer()
	reviewers := []Reviewer{
		{Name: "junior", Languages: []string{"en"}, MaxComplexity: 2, CurrentLoad: 0, Capacity: 5},
	}
	c := ReviewCase{CaseID: "c3", Complexity: 5, Country: "GB", Language: "en"}
	if _, err := b.Assign(c, reviewers); err == nil {
		t.Fatal("expected error for case exceeding reviewer certification")
	}
}

func TestQueueRebalanceOverflow(t *testing.T) {
	b := NewQueueBalancer()
	reviewers := []Reviewer{
		{Name: "overloaded", MaxComplexity: 5, CurrentLoad: 15, Capacity: 10},
		{Name: "free", MaxComplexity: 5, CurrentLoad: 0, Capacity: 10},
	}
	moves := b.Rebalance(nil, reviewers)
	if len(moves) != 5 {
		t.Fatalf("expected 5 moves to shed overflow of 5, got %d", len(moves))
	}
	if moves[0].From != "overloaded" || moves[0].To != "free" {
		t.Fatalf("unexpected move %+v", moves[0])
	}
}

func TestExpirationGuardRetention(t *testing.T) {
	g := NewExpirationGuard(map[EvidenceType]time.Duration{
		EvidenceIdentityDoc: 10 * 365 * 24 * time.Hour,
	})
	collected := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC) // 2.4y < 10y

	d := g.CanExpire("cust-1", EvidenceIdentityDoc, collected, now)
	if d.Allowed {
		t.Fatalf("retention minimum must block: %+v", d)
	}
	now = time.Date(2031, 1, 2, 0, 0, 0, 0, time.UTC) // > 10y
	d = g.CanExpire("cust-1", EvidenceIdentityDoc, collected, now)
	if !d.Allowed {
		t.Fatalf("past retention with no holds should allow: %+v", d)
	}
}

func TestExpirationGuardLegalHold(t *testing.T) {
	g := NewExpirationGuard(map[EvidenceType]time.Duration{})
	collected := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	g.PlaceHold(Hold{ID: HoldLegal, SubjectID: "cust-9", Reason: "FCA s.165 request",
		IssuedBy: "legal", IssuedAt: now.Add(-time.Hour)})

	d := g.CanExpire("cust-9", EvidenceIdentityDoc, collected, now)
	if d.Allowed || len(d.BlockedBy) != 1 || d.BlockedBy[0] != string(HoldLegal) {
		t.Fatalf("legal hold must block: %+v", d)
	}

	// Another subject is unaffected.
	if d := g.CanExpire("cust-8", EvidenceIdentityDoc, collected, now); !d.Allowed {
		t.Fatalf("hold is subject-scoped: %+v", d)
	}

	// Evidence-type-scoped hold blocks only that type.
	g2 := NewExpirationGuard(map[EvidenceType]time.Duration{})
	g2.PlaceHold(Hold{ID: HoldInvestigation, SubjectID: "cust-9",
		Evidence: []EvidenceType{EvidenceAddress}, IssuedBy: "fraud", IssuedAt: now})
	if d := g2.CanExpire("cust-9", EvidenceAddress, collected, now); d.Allowed {
		t.Fatal("scoped hold must block its type")
	}
	if d := g2.CanExpire("cust-9", EvidenceIdentityDoc, collected, now); !d.Allowed {
		t.Fatalf("scoped hold must not block other types: %+v", d)
	}
}

func TestExpirationGuardHoldExpiry(t *testing.T) {
	g := NewExpirationGuard(map[EvidenceType]time.Duration{})
	collected := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	g.PlaceHold(Hold{ID: HoldRegulatoryReq, SubjectID: "cust-9",
		IssuedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour)})

	if d := g.CanExpire("cust-9", EvidenceIdentityDoc, collected, now); !d.Allowed {
		t.Fatalf("expired hold must not block: %+v", d)
	}
}
