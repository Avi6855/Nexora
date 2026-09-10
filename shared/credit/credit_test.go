package credit

import (
	"fmt"
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)

func TestLimitSimulator(t *testing.T) {
	p := LimitProfile{
		CurrentLimitMinor: 200000, BalanceMinor: 80000,
		MonthlySpendMinor: 120000, MonthlyIncomeMinor: 300000,
		APRBps: 2400, MinimumPctBps: 300, PaymentBehaviour: 1.0,
	}
	proj, err := SimulateLimit(p, 400000)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(proj.UtilisationToday-0.40) > 0.001 || math.Abs(proj.Utilisation-0.20) > 0.001 {
		t.Fatalf("utilisation math wrong: %.3f → %.3f", proj.UtilisationToday, proj.Utilisation)
	}
	// Interest: 80000 × 24% / 12 = 1600 minor.
	if proj.ProjectedMonthlyInterestMinor != 1600 {
		t.Fatalf("interest %d, want 1600", proj.ProjectedMonthlyInterestMinor)
	}
	if proj.Advisory {
		t.Fatalf("sane raise must not be advisory: %s", proj.Verdict)
	}
	// A near-maxed balance on a bigger limit trips the affordability check:
	// £38k balance → min payment £1,140 = 38% of £3k income.
	stretched := p
	stretched.BalanceMinor = 3800000
	big, _ := SimulateLimit(stretched, 4000000)
	if !big.Advisory {
		t.Fatalf("maxed balance on £3k income must be advisory: %+v", big)
	}
	if _, err := SimulateLimit(p, 0); err == nil {
		t.Fatal("zero limit must error")
	}
}

func TestRepaymentStrategies(t *testing.T) {
	debts := []Debt{
		{Name: "credit-card", BalanceMinor: 300000, APRBps: 2400, MinPayMinor: 30000},
		{Name: "flex", BalanceMinor: 100000, APRBps: 1800, MinPayMinor: 10000},
		{Name: "overdraft", BalanceMinor: 50000, APRBps: 3900, MinPayMinor: 5000},
	}
	// £200/mo budget: above the £450 total of minimums (feasible) with real
	// surplus for a strategy target — the £60 budget in an earlier draft was
	// below minimums, which made every strategy infeasible and equal.
	results, err := OptimiseRepayment(debts, 20000, 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 strategies, got %d", len(results))
	}
	best, saved, ok := CompareStrategies(results)
	if !ok {
		t.Fatal("comparison requires a baseline")
	}
	if best.Strategy != StrategyAvalanche {
		t.Fatalf("with these APRs avalanche should win, got %s", best.Strategy)
	}
	if saved <= 0 {
		t.Fatalf("avalanche must save vs minimum-only: %d", saved)
	}
	if results[0].Strategy != StrategyAvalanche || results[len(results)-1].Strategy != StrategyMinimum {
		t.Fatalf("results must be ranked by interest: %+v", results)
	}
	// Avalanche order: overdraft (39%) → card (24%) → flex (18%).
	if best.Order[0] != "overdraft" {
		t.Fatalf("avalanche order wrong: %+v", best.Order)
	}
	// Infeasible budget is flagged, not silently wrong.
	r2, _ := OptimiseRepayment(debts, 10000, 120)
	for _, r := range r2 {
		if r.Feasible {
			t.Fatalf("£100/mo cannot cover £45k of minimums: %+v", r)
		}
	}
}

func TestBureauCorrectionFlow(t *testing.T) {
	e := CorrectionEngineNew()
	c := e.Open("corr-1", "cust-1", "LATE_PAYMENT", "Sept late marker incorrect", "customer", t0)
	// Submit without evidence must fail.
	if err := e.Submit("corr-1", t0); err == nil {
		t.Fatal("dispute without evidence must not submit")
	}
	if err := e.AttachEvidence("corr-1", "bank statement PDF"); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit("corr-1", t0); err != nil {
		t.Fatal(err)
	}
	if c.State != CorrAwaiting || c.Deadline.IsZero() {
		t.Fatalf("awaiting provider with deadline: %+v", c)
	}
	// Silence past the window auto-escalates.
	esc := e.Tick(t0.Add(ResponseWindow + time.Hour))
	if len(esc) != 1 || esc[0] != "corr-1" {
		t.Fatalf("must auto-escalate on provider silence: %v", esc)
	}
	if err := e.Resolve("corr-1", true); err != nil {
		t.Fatal(err)
	}
	if c.State != CorrUpdated {
		t.Fatalf("state %s, want FILE_UPDATED", c.State)
	}
	// In-window response resolves without escalation.
	c2 := e.Open("corr-2", "cust-2", "BALANCE", "balance wrong", "proactive-scan", t0)
	_ = e.AttachEvidence("corr-2", "statement")
	_ = e.Submit("corr-2", t0)
	if esc := e.Tick(t0.Add(time.Hour)); len(esc) != 0 {
		t.Fatalf("in-window must not escalate: %v", esc)
	}
	if err := e.Resolve("corr-2", false); err != nil {
		t.Fatal(err)
	}
	if c2.State != CorrRejected {
		t.Fatalf("state %s, want REJECTED", c2.State)
	}
}

func TestDecisionSandbox(t *testing.T) {
	pop := make([]Applicant, 0, 400)
	for i := 0; i < 200; i++ {
		pop = append(pop, Applicant{ID: fmt.Sprintf("a%d", i), Segment: "SEG-A",
			Score: 0.6 + float64(i)/1000, Arrears12m: i / 50, IncomeMinor: 250000, RequestedMinor: 50000})
	}
	for i := 0; i < 200; i++ {
		pop = append(pop, Applicant{ID: fmt.Sprintf("b%d", i), Segment: "SEG-B",
			Score: 0.6 + float64(i)/1000, Arrears12m: i / 50, IncomeMinor: 250000, RequestedMinor: 50000})
	}
	baseline := Policy{Name: "current", MinScore: 0.60, MaxArrears: 3, MinIncomeCover: 0}
	candidate := Policy{Name: "relaxed", MinScore: 0.61, MaxArrears: 3, MinIncomeCover: 0}
	res := RunSandbox(candidate, baseline, pop)
	if res.DefaultRate > DefaultRateGate {
		t.Fatalf("sane candidate must stay under the default gate: %f", res.DefaultRate)
	}
	if math.Abs(res.MaxSegmentGap) > SegmentGapGate {
		t.Fatalf("identical segments must show no gap: %+v", res.SegmentGaps)
	}
	if !res.SafeToRollout {
		t.Fatalf("candidate should be safe: %+v", res.Reasons)
	}
	// A policy that treats segments differently trips the fairness gate.
	biased := Policy{Name: "biased", MinScore: 0.60, MaxArrears: 3, MinIncomeCover: 0}
	// SEG-B gets low scores → approvals differ wildly between segments.
	biasedPop := append([]Applicant(nil), pop[:200]...)
	for i := 0; i < 200; i++ {
		biasedPop = append(biasedPop, Applicant{ID: fmt.Sprintf("b%d", i), Segment: "SEG-B",
			Score: 0.1, Arrears12m: 0, IncomeMinor: 250000, RequestedMinor: 50000})
	}
	res = RunSandbox(biased, baseline, biasedPop)
	if res.SafeToRollout {
		t.Fatalf("biased policy must not be safe: %+v", res.Reasons)
	}
}

func TestFairnessMonitorLive(t *testing.T) {
	m := NewFairnessMonitor(0.08, 30, 24*time.Hour)
	// Fleet ≈ 80% approval; SEG-A tracks it, SEG-B sits at 55%.
	for i := 0; i < 100; i++ {
		m.Observe(FairnessObs{Segment: "SEG-A", Approved: i < 80, Score: 0.7, At: t0})
	}
	for i := 0; i < 100; i++ {
		m.Observe(FairnessObs{Segment: "SEG-B", Approved: i < 55, Score: 0.7, At: t0})
	}
	// Deviation-from-fleet monitoring alarms BOTH sides of the split:
	// SEG-A over-approves (+12.5pp) and SEG-B under-approves (−12.5pp).
	alerts := m.Evaluate(t0.Add(time.Hour))
	if len(alerts) != 2 {
		t.Fatalf("both segments must alarm: %+v", alerts)
	}
	var segB *FairnessAlert
	for i := range alerts {
		if alerts[i].Segment == "SEG-B" {
			segB = &alerts[i]
		}
	}
	if segB == nil || math.Abs(segB.Disparity-(0.55-0.675)) > 0.01 {
		t.Fatalf("SEG-B disparity math: %+v", alerts)
	}
	// Old observations outside the window don't count.
	m2 := NewFairnessMonitor(0.08, 30, time.Hour)
	m2.Observe(FairnessObs{Segment: "OLD", Approved: false, At: t0.Add(-3 * time.Hour)})
	if alerts := m2.Evaluate(t0); len(alerts) != 0 {
		t.Fatalf("out-of-window observations must be ignored: %+v", alerts)
	}
	// Small samples are held back until they reach the minimum.
	m3 := NewFairnessMonitor(0.08, 30, 24*time.Hour)
	for i := 0; i < 10; i++ {
		m3.Observe(FairnessObs{Segment: "TINY", Approved: false, At: t0})
	}
	if alerts := m3.Evaluate(t0.Add(time.Minute)); len(alerts) != 0 {
		t.Fatalf("below minimum sample size must not alarm: %+v", alerts)
	}
}
