package testgrid

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func gridNop() zerolog.Logger { return zerolog.Nop() }

var gridT0 = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func TestScenarioGenerator(t *testing.T) {
	g := NewGenerator(gridNop())
	made, err := g.Generate(IncidentDuplicateEvent, 42, 3, gridT0)
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 3 {
		t.Fatalf("want 3 cases, got %d", len(made))
	}
	// Deterministic: same seed regenerates same payload shape.
	made2, err := g.Generate(IncidentDuplicateEvent, 42, 3, gridT0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range made {
		if made[i].Payload["seed"] != made2[i].Payload["seed"] {
			t.Fatalf("seeded generation must be deterministic: %+v vs %+v", made[i].Payload, made2[i].Payload)
		}
	}
	if len(g.List("")) != 6 {
		t.Fatalf("list must return 6, got %d", len(g.List("")))
	}
	if len(g.List(IncidentDuplicateEvent)) != 6 {
		t.Fatalf("filtered list wrong: %d", len(g.List(IncidentDuplicateEvent)))
	}
	if len(g.List(IncidentSlowDB)) != 0 {
		t.Fatal("other kinds must filter out")
	}
	// Run each: well-formed synthetic cases pass.
	for _, c := range made {
		out, err := g.Run(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Passed {
			t.Fatalf("synthetic case must pass: %+v", out)
		}
	}
	// Catalog coverage: every incident kind generates.
	for _, k := range AllIncidentKinds {
		if _, err := g.Generate(k, 7, 1, gridT0); err != nil {
			t.Fatalf("kind %s: %v", k, err)
		}
	}
	if _, err := g.Generate("nope", 1, 1, gridT0); !errors.Is(err, ErrUnknownIncident) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := g.Run("missing"); !errors.Is(err, ErrScenarioNotFound) {
		t.Fatalf("missing run: %v", err)
	}
}

func TestChaosPolicyEngine(t *testing.T) {
	e := NewPolicyEngine(DefaultChaosPolicy(), gridNop())
	goodAt := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC) // Mon 10:00 UTC
	rep, err := e.Evaluate(ExperimentRequest{
		Target: "ledger", Fault: "dependency_timeout", BlastRadiusPct: 5,
		Env: "staging", HasRollbackPlan: true, At: goodAt,
	}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictApprove {
		t.Fatalf("good request must APPROVE: %+v", rep)
	}
	// Prod env denied.
	rep, _ = e.Evaluate(ExperimentRequest{Target: "ledger", Fault: "slow_db", BlastRadiusPct: 5, Env: "prod", HasRollbackPlan: true, At: goodAt}, time.Time{})
	if rep.Verdict != VerdictDeny {
		t.Fatalf("prod must DENY: %+v", rep)
	}
	// Radius over cap denied.
	rep, _ = e.Evaluate(ExperimentRequest{Target: "ledger", Fault: "slow_db", BlastRadiusPct: 50, Env: "staging", HasRollbackPlan: true, At: goodAt}, time.Time{})
	if rep.Verdict != VerdictDeny {
		t.Fatalf("large radius must DENY: %+v", rep)
	}
	// Missing rollback denied.
	rep, _ = e.Evaluate(ExperimentRequest{Target: "ledger", Fault: "slow_db", BlastRadiusPct: 5, Env: "staging", HasRollbackPlan: false, At: goodAt}, time.Time{})
	if rep.Verdict != VerdictDeny {
		t.Fatalf("missing rollback must DENY: %+v", rep)
	}
	// Outside business hours denied.
	night := time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC)
	rep, _ = e.Evaluate(ExperimentRequest{Target: "ledger", Fault: "slow_db", BlastRadiusPct: 5, Env: "staging", HasRollbackPlan: true, At: night}, time.Time{})
	if rep.Verdict != VerdictDeny {
		t.Fatalf("night run must DENY: %+v", rep)
	}
	if len(rep.Reasons) == 0 {
		t.Fatal("DENY needs guard reasons")
	}
	if _, err := e.Evaluate(ExperimentRequest{}, time.Time{}); err == nil {
		t.Fatal("empty request must error")
	}
}

func TestJourneyGrid(t *testing.T) {
	s := NewGridStore(gridNop())
	steps := []string{"checkout", "settle"}
	faults := []string{"none", "dependency_timeout", "duplicate_event"}
	exp := map[string]string{
		"checkout\x00none":               "success",
		"checkout\x00dependency_timeout": "retry",
		"settle\x00duplicate_event":      "deduped",
	}
	j, err := s.CreateJourney(steps, faults, exp, gridT0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.RunGrid(j.ID, gridT0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cells) != 2*3 {
		t.Fatalf("want 6 cells, got %d", len(res.Cells))
	}
	if res.Failed != 0 || res.Passed != 6 {
		t.Fatalf("matching expectations must all pass: %+v", res)
	}
	stored, err := s.Results(j.ID)
	if err != nil || stored.Passed != 6 {
		t.Fatalf("results: %v %+v", err, stored)
	}
	// Mismatched expectation fails that cell only.
	s2 := NewGridStore(gridNop())
	j2, _ := s2.CreateJourney([]string{"checkout"}, []string{"slow_db"}, map[string]string{"checkout\x00slow_db": "success"}, gridT0)
	res2, _ := s2.RunGrid(j2.ID, gridT0)
	if res2.Passed != 0 || res2.Failed != 1 {
		t.Fatalf("wrong expectation must fail: %+v", res2)
	}
	if _, err := s2.Results("missing"); !errors.Is(err, ErrJourneyNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
	j3, _ := s2.CreateJourney([]string{"a"}, []string{"none"}, nil, gridT0)
	if _, err := s2.Results(j3.ID); !errors.Is(err, ErrJourneyNotRun) {
		t.Fatalf("unrun journey: %v", err)
	}
	if _, err := s2.CreateJourney(nil, faults, nil, gridT0); err == nil {
		t.Fatal("empty steps must error")
	}
}
