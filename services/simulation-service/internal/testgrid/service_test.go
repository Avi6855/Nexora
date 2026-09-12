package testgrid

import (
	"testing"
	"time"

	"github.com/rs/zerolog"

	sharedgrid "github.com/nexora/nexora/shared/testgrid"
)

func testService() *Service { return NewService(zerolog.Nop()) }

func TestServiceScenarios(t *testing.T) {
	svc := testService()
	cases, err := svc.Generate(sharedgrid.IncidentPartialWrite, 11, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("want 2, got %d", len(cases))
	}
	if len(svc.ListScenarios("")) != 2 {
		t.Fatalf("list: %d", len(svc.ListScenarios("")))
	}
}

func TestServiceChaosApproveDeny(t *testing.T) {
	svc := testService()
	goodAt := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	rep, err := svc.EvaluateChaos(sharedgrid.ExperimentRequest{Target: "ledger", Fault: "slow_db", BlastRadiusPct: 5, Env: "staging", HasRollbackPlan: true, At: goodAt})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != sharedgrid.VerdictApprove {
		t.Fatalf("must APPROVE: %+v", rep)
	}
	rep, _ = svc.EvaluateChaos(sharedgrid.ExperimentRequest{Target: "ledger", Fault: "slow_db", BlastRadiusPct: 50, Env: "staging", HasRollbackPlan: true, At: goodAt})
	if rep.Verdict != sharedgrid.VerdictDeny {
		t.Fatalf("large radius must DENY: %+v", rep)
	}
}

func TestServiceJourneyGrid(t *testing.T) {
	svc := testService()
	j, err := svc.CreateJourney([]string{"checkout", "settle"}, []string{"none", "duplicate_event"}, map[string]string{
		"checkout\x00none": "success", "settle\x00duplicate_event": "deduped",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.RunGrid(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cells) != 4 || res.Failed != 0 {
		t.Fatalf("grid must pass: %+v", res)
	}
	stored, err := svc.GridResults(j.ID)
	if err != nil || stored.Passed != 4 {
		t.Fatalf("results: %v %+v", err, stored)
	}
}
