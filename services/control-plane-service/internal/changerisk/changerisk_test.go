package changerisk

import (
	"strings"
	"testing"
)

func TestLowRiskChange(t *testing.T) {
	s := NewScorer()
	a := s.Assess(ChangeRequest{
		PRID: "PR-1", Author: "dev", Title: "rename internal field",
		Services: []ServiceFacts{{
			Name: "insights-service", Criticality: "DEFERABLE",
			DirectDependents: 0, TransitiveDependents: 0,
			TestCoveragePct: 95, AuthorFamiliarity: 12,
		}},
	})
	if a.Level != RiskLow {
		t.Fatalf("clean small change must be LOW: %s (%.0f) %v", a.Level, a.Score, a.Drivers)
	}
	if len(a.Gates) == 0 || a.Gates[0] != "standard CI" {
		t.Fatalf("low-risk gates wrong: %v", a.Gates)
	}
}

func TestPaymentPathChangeIsHigh(t *testing.T) {
	s := NewScorer()
	a := s.Assess(ChangeRequest{
		PRID: "PR-2", Title: "tweak payment state machine",
		Services: []ServiceFacts{{
			Name: "payment-service", Criticality: "PAYMENT_PATH",
			PaymentPathChanged: true, TestCoveragePct: 90, AuthorFamiliarity: 5,
		}},
	})
	if a.Level == RiskLow {
		t.Fatalf("payment path change must exceed LOW: %s (%.0f)", a.Level, a.Score)
	}
	found := false
	for _, g := range a.Gates {
		if strings.Contains(g, "financial invariant") {
			found = true
		}
	}
	if !found {
		t.Fatalf("payment path must require financial invariant tests: %v", a.Gates)
	}
}

func TestSchemaChangeAddsGate(t *testing.T) {
	s := NewScorer()
	a := s.Assess(ChangeRequest{
		PRID: "PR-3", Title: "migrate accounts schema",
		Services: []ServiceFacts{{
			Name: "account-service", Criticality: "IMPORTANT",
			SchemaChanged: true, TestCoveragePct: 85, AuthorFamiliarity: 3,
		}},
	})
	found := false
	for _, g := range a.Gates {
		if strings.Contains(g, "expand-migrate-contract") {
			found = true
		}
	}
	if !found {
		t.Fatalf("schema change must require migration plan: %v", a.Gates)
	}
}

func TestBlastRadiusScales(t *testing.T) {
	s := NewScorer()
	small := s.Assess(ChangeRequest{PRID: "a", Services: []ServiceFacts{{
		Name: "svc", Criticality: "IMPORTANT", TransitiveDependents: 2,
		TestCoveragePct: 90, AuthorFamiliarity: 2,
	}}})
	large := s.Assess(ChangeRequest{PRID: "b", Services: []ServiceFacts{{
		Name: "svc", Criticality: "IMPORTANT", TransitiveDependents: 120,
		TestCoveragePct: 90, AuthorFamiliarity: 2,
	}}})
	if large.Score <= small.Score {
		t.Fatalf("larger blast radius must score higher: %.0f vs %.0f", large.Score, small.Score)
	}
	// Saturation: 1000 dependents must not exceed 20 points for blast radius.
	huge := s.Assess(ChangeRequest{PRID: "c", Services: []ServiceFacts{{
		Name: "svc", Criticality: "IMPORTANT", TransitiveDependents: 1000,
		TestCoveragePct: 90, AuthorFamiliarity: 2,
	}}})
	if huge.Score-large.Score > 1 {
		t.Fatalf("blast radius must saturate: %.0f vs %.0f", huge.Score, large.Score)
	}
}

func TestIncidentHistoryAndCoverage(t *testing.T) {
	s := NewScorer()
	a := s.Assess(ChangeRequest{PRID: "PR-4", Services: []ServiceFacts{{
		Name: "fraud-service", Criticality: "IMPORTANT",
		IncidentsLast90d: 5, TestCoveragePct: 40, AuthorFamiliarity: 0,
		TransitiveDependents: 10,
	}}})
	joined := strings.Join(a.Drivers, "; ")
	if !strings.Contains(joined, "incidents") || !strings.Contains(joined, "coverage") || !strings.Contains(joined, "no prior merged") {
		t.Fatalf("drivers must name all contributing factors: %v", a.Drivers)
	}
	if a.Level == RiskLow {
		t.Fatalf("5 incidents + 40%% coverage + new author must exceed LOW: %s (%.0f)", a.Level, a.Score)
	}
}

func TestAIGeneratedFlagged(t *testing.T) {
	s := NewScorer()
	without := s.Assess(ChangeRequest{PRID: "x", Services: []ServiceFacts{{
		Name: "svc", Criticality: "DEFERABLE", TestCoveragePct: 90, AuthorFamiliarity: 1,
	}}})
	with := s.Assess(ChangeRequest{PRID: "x", AIGenerated: true, Services: []ServiceFacts{{
		Name: "svc", Criticality: "DEFERABLE", TestCoveragePct: 90, AuthorFamiliarity: 1,
	}}})
	if with.Score <= without.Score {
		t.Fatal("AI-authored changes must carry provenance-review weight")
	}
	if !strings.Contains(strings.Join(with.Drivers, "; "), "AI-authored") {
		t.Fatalf("driver must name AI provenance: %v", with.Drivers)
	}
}

func TestCriticalCompositeChange(t *testing.T) {
	s := NewScorer()
	a := s.Assess(ChangeRequest{
		PRID: "PR-5", Title: "the big one",
		Services: []ServiceFacts{
			{Name: "payment-service", Criticality: "PAYMENT_PATH", PaymentPathChanged: true,
				SchemaChanged: true, TransitiveDependents: 80, IncidentsLast90d: 2,
				TestCoveragePct: 60, AuthorFamiliarity: 0},
			{Name: "ledger-service", Criticality: "PAYMENT_PATH", SchemaChanged: true,
				TransitiveDependents: 60, TestCoveragePct: 70, AuthorFamiliarity: 0},
		},
	})
	if a.Level != RiskCritical {
		t.Fatalf("composite payment+schema+blast change must be CRITICAL: %s (%.0f)", a.Level, a.Score)
	}
	found := false
	for _, g := range a.Gates {
		if strings.Contains(g, "compatibility certification") {
			found = true
		}
	}
	if !found {
		t.Fatalf("critical changes must require stand-in compatibility certification: %v", a.Gates)
	}
}

func TestScoreCappedAt100(t *testing.T) {
	s := NewScorer()
	a := s.Assess(ChangeRequest{PRID: "PR-6", Services: []ServiceFacts{
		{
			Name: "everything", Criticality: "PAYMENT_PATH", PaymentPathChanged: true,
			SchemaChanged: true, TransitiveDependents: 5000, IncidentsLast90d: 50,
			TestCoveragePct: 0, AuthorFamiliarity: 0,
		},
		{
			Name: "more", Criticality: "PAYMENT_PATH", PaymentPathChanged: true, SchemaChanged: true,
			TransitiveDependents: 5000, IncidentsLast90d: 50, TestCoveragePct: 0, AuthorFamiliarity: 0,
		},
	}})
	if a.Score > 100 {
		t.Fatalf("score must cap at 100, got %.0f", a.Score)
	}
}
