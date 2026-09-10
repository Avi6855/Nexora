package credit

import (
	"testing"
	"time"

	shared "github.com/nexora/nexora/shared/credit"
)

func TestSimulateLimit(t *testing.T) {
	s := NewService()
	profile := shared.LimitProfile{
		CurrentLimitMinor:  100000,
		BalanceMinor:       20000,
		MonthlySpendMinor:  40000,
		MonthlyIncomeMinor: 300000,
		APRBps:             2495,
		MinimumPctBps:      300,
		PaymentBehaviour:   1,
	}
	proj, err := s.SimulateLimit(profile, 200000)
	if err != nil {
		t.Fatalf("SimulateLimit: %v", err)
	}
	if proj.Utilisation >= proj.UtilisationToday {
		t.Fatalf("higher limit should lower utilisation: %+v", proj)
	}
	if proj.Advisory {
		t.Fatalf("healthy increase should not be advisory: %+v", proj)
	}
	if _, err := s.SimulateLimit(profile, 0); err == nil {
		t.Fatal("zero limit should error")
	}
}

func TestRecommendRepayment(t *testing.T) {
	s := NewService()
	debts := []shared.Debt{
		{Name: "card-a", BalanceMinor: 50000, APRBps: 2999, MinPayMinor: 2000},
		{Name: "card-b", BalanceMinor: 10000, APRBps: 1999, MinPayMinor: 1000},
	}
	rec, err := s.RecommendRepayment(debts, 10000, 60)
	if err != nil {
		t.Fatalf("RecommendRepayment: %v", err)
	}
	if len(rec.Results) != 3 {
		t.Fatalf("expected 3 strategies, got %d", len(rec.Results))
	}
	if !rec.Found {
		t.Fatal("expected headline comparison vs minimum-only")
	}
	if rec.Saved < 0 {
		t.Fatalf("best should not cost more than minimum-only, saved=%d", rec.Saved)
	}
	if _, err := s.RecommendRepayment(nil, 10000, 60); err == nil {
		t.Fatal("empty debts should error")
	}
}

func TestCorrectionLifecycle(t *testing.T) {
	s := NewService()
	now := time.Now().UTC()
	c, err := s.OpenCorrection("c1", "cust-1", "BALANCE", "wrong balance", "customer", now)
	if err != nil {
		t.Fatalf("OpenCorrection: %v", err)
	}
	if c.State != shared.CorrOpened {
		t.Fatalf("expected OPENED, got %s", c.State)
	}
	if err := s.SubmitCorrection("c1", now); err == nil {
		t.Fatal("submit without evidence should fail")
	}
	if err := s.AttachEvidence("c1", "statement.pdf"); err != nil {
		t.Fatalf("AttachEvidence: %v", err)
	}
	if err := s.SubmitCorrection("c1", now); err != nil {
		t.Fatalf("SubmitCorrection: %v", err)
	}
	escalated := s.TickCorrections(now.Add(29 * 24 * time.Hour))
	found := false
	for _, id := range escalated {
		if id == "c1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("overdue case should escalate, got %v", escalated)
	}
	if err := s.ResolveCorrection("c1", true); err != nil {
		t.Fatalf("ResolveCorrection: %v", err)
	}
}

func TestDecisionSandbox(t *testing.T) {
	s := NewService()
	candidate := shared.Policy{Name: "candidate", MinScore: 0.5, MaxArrears: 1, MinIncomeCover: 3}
	baseline := shared.Policy{Name: "baseline", MinScore: 0.3, MaxArrears: 2, MinIncomeCover: 2}
	pop := []shared.Applicant{
		{ID: "a1", Segment: "young", Score: 0.9, Arrears12m: 0, IncomeMinor: 300000, RequestedMinor: 50000},
		{ID: "a2", Segment: "young", Score: 0.4, Arrears12m: 0, IncomeMinor: 300000, RequestedMinor: 50000},
		{ID: "a3", Segment: "senior", Score: 0.8, Arrears12m: 0, IncomeMinor: 300000, RequestedMinor: 50000, ObservedDefault: true},
		{ID: "a4", Segment: "senior", Score: 0.2, Arrears12m: 3, IncomeMinor: 300000, RequestedMinor: 50000},
	}
	res, err := s.RunDecisionSandbox(candidate, baseline, pop)
	if err != nil {
		t.Fatalf("RunDecisionSandbox: %v", err)
	}
	if res.Policy != "candidate" {
		t.Fatalf("expected policy candidate, got %s", res.Policy)
	}
	if res.ApprovalRate == 0 || res.BaselineRate == 0 {
		t.Fatalf("expected non-zero rates, got %+v", res)
	}
	if _, err := s.RunDecisionSandbox(shared.Policy{}, baseline, pop); err == nil {
		t.Fatal("nameless candidate should error")
	}
}

func TestFairnessMonitor(t *testing.T) {
	s := NewService()
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		s.ObserveDecision(shared.FairnessObs{Segment: "young", Approved: true, Score: 0.8, At: now})
	}
	for i := 0; i < 10; i++ {
		s.ObserveDecision(shared.FairnessObs{Segment: "senior", Approved: false, Score: 0.4, At: now})
	}
	alerts := s.EvaluateFairness(now)
	if len(alerts) == 0 {
		t.Fatal("expected fairness alerts for divergent segments")
	}
	seen := map[string]bool{}
	for _, a := range alerts {
		seen[a.Segment] = true
	}
	if !seen["young"] && !seen["senior"] {
		t.Fatalf("expected segment alert, got %+v", alerts)
	}
}
