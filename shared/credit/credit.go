// Package credit implements Nexora's credit platform engines:
//
//  6. Limit simulator: what a limit change does to utilisation, projected
//     repayments and affordability pressure — BEFORE the change.
//
//  7. Repayment strategy optimiser: avalanche/snowball/balanced strategies
//     over real APRs and balances, with projected interest differences.
//
//  8. Bureau correction centre: long-running dispute cases with evidence,
//     provider response windows and auto-escalation on silence.
//
//  9. Decision sandbox: replay a candidate credit policy over a sample
//     population and compare approval/risk/fairness metrics against the
//     current policy — before anyone ships it.
//
//  10. Fairness monitor: continuously segment live decisions by disparity.
//     Approval-rate gaps and score-distribution drift between segments are
//     measured, thresholded and alarmed — fairness is a runtime property,
//     not a one-off review.
package credit

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// ── 6. Credit limit simulator ───────────────────────────────────────────────

// LimitProfile is the customer snapshot a simulation starts from.
type LimitProfile struct {
	CurrentLimitMinor  int64   `json:"current_limit_minor"`
	BalanceMinor       int64   `json:"balance_minor"`
	MonthlySpendMinor  int64   `json:"monthly_spend_minor"`
	MonthlyIncomeMinor int64   `json:"monthly_income_minor"`
	APRBps             int64   `json:"apr_bps"`
	MinimumPctBps      int64   `json:"minimum_pct_bps"`   // min payment as % of balance
	PaymentBehaviour   float64 `json:"payment_behaviour"` // 0..1 fraction of min paid
}

// LimitProjection is the simulated outcome of a new limit.
type LimitProjection struct {
	NewLimitMinor                 int64   `json:"new_limit_minor"`
	Utilisation                   float64 `json:"utilisation"` // balance/limit
	UtilisationToday              float64 `json:"utilisation_today"`
	ProjectedMonthlyInterestMinor int64   `json:"projected_monthly_interest_minor"`
	ProjectedMinPaymentMinor      int64   `json:"projected_min_payment_minor"`
	AffordabilityRatio            float64 `json:"affordability_ratio"` // min payment / income
	Verdict                       string  `json:"verdict"`
	Advisory                      bool    `json:"advisory"` // true = do not recommend
}

// SimulateLimit projects a limit change. Interest projection uses the calc
// principle: balance × APR/12, exact rational rounding at the edge (half-up
// on minor units is acceptable for a display-grade projection).
func SimulateLimit(p LimitProfile, newLimitMinor int64) (*LimitProjection, error) {
	if newLimitMinor <= 0 {
		return nil, fmt.Errorf("limit must be positive, got %d", newLimitMinor)
	}
	if p.CurrentLimitMinor <= 0 {
		return nil, fmt.Errorf("profile has no current limit")
	}
	proj := &LimitProjection{NewLimitMinor: newLimitMinor}
	proj.UtilisationToday = float64(p.BalanceMinor) / float64(p.CurrentLimitMinor)
	proj.Utilisation = float64(p.BalanceMinor) / float64(newLimitMinor)
	// Monthly interest = balance × APR / 12.
	proj.ProjectedMonthlyInterestMinor = p.BalanceMinor * p.APRBps / (10000 * 12)
	// Minimum payment = balance × min%, or min payment floor logic simplified.
	minPay := p.BalanceMinor * p.MinimumPctBps / 10000
	// Behavioural model: customer pays `PaymentBehaviour` fraction of the
	// minimum (measured historically), so projected repayment follows.
	proj.ProjectedMinPaymentMinor = int64(float64(minPay) * p.PaymentBehaviour)
	if proj.ProjectedMinPaymentMinor < 0 {
		proj.ProjectedMinPaymentMinor = 0
	}
	proj.AffordabilityRatio = float64(proj.ProjectedMinPaymentMinor) / float64(p.MonthlyIncomeMinor)
	switch {
	case proj.AffordabilityRatio > 0.35:
		proj.Verdict = "projected repayment pressure exceeds 35% of income — not recommended"
		proj.Advisory = true
	case proj.Utilisation > 0.9:
		proj.Verdict = "limit would be immediately near-fully utilised — review spending first"
		proj.Advisory = true
	default:
		proj.Verdict = fmt.Sprintf("utilisation falls from %.0f%% to %.0f%% with no material repayment-pressure increase",
			proj.UtilisationToday*100, proj.Utilisation*100)
	}
	return proj, nil
}

// ── 7. Repayment strategy optimiser ─────────────────────────────────────────

// Debt is one obligation.
type Debt struct {
	Name         string `json:"name"`
	BalanceMinor int64  `json:"balance_minor"`
	APRBps       int64  `json:"apr_bps"`
	MinPayMinor  int64  `json:"min_pay_minor"`
}

// StrategyName enumerates the classic payoff strategies.
type StrategyName string

const (
	StrategyAvalanche StrategyName = "AVALANCHE" // highest APR first
	StrategySnowball  StrategyName = "SNOWBALL"  // smallest balance first
	StrategyMinimum   StrategyName = "MINIMUM"   // pay minimums only
)

// StrategyResult is the simulation of one strategy.
type StrategyResult struct {
	Strategy           StrategyName `json:"strategy"`
	TotalInterestMinor int64        `json:"total_interest_minor"`
	MonthsToClear      int          `json:"months_to_clear"`
	Order              []string     `json:"payoff_order"`
	Feasible           bool         `json:"feasible"`
}

// OptimiseRepayment simulates each strategy with the same monthly budget and
// ranks by total interest. Simulation is monthly: interest accrues on the
// outstanding balance, the budget pays minimums first and the remainder hits
// the strategy's target debt.
func OptimiseRepayment(debts []Debt, budgetMinor int64, maxMonths int) ([]StrategyResult, error) {
	if len(debts) == 0 {
		return nil, fmt.Errorf("no debts provided")
	}
	totalMin := int64(0)
	for _, d := range debts {
		totalMin += d.MinPayMinor
	}
	var out []StrategyResult
	for _, name := range []StrategyName{StrategyAvalanche, StrategySnowball, StrategyMinimum} {
		res := simulate(debts, budgetMinor, name, maxMonths)
		out = append(out, res)
	}
	_ = totalMin
	sort.SliceStable(out, func(i, j int) bool { return out[i].TotalInterestMinor < out[j].TotalInterestMinor })
	return out, nil
}

func simulate(debts []Debt, budget int64, strat StrategyName, maxMonths int) StrategyResult {
	state := make([]Debt, len(debts))
	copy(state, debts)
	order := payoffOrder(state, strat)
	res := StrategyResult{Strategy: strat, Order: order}
	res.Feasible = budget >= minSum(state)
	for month := 0; month < maxMonths; month++ {
		if allCleared(state) {
			break
		}
		// 1. Accrue monthly interest — and COUNT it. The strategy comparison
		// is exactly this number; the original draft accrued without recording,
		// so every strategy scored 0 and the ranking was meaningless.
		for i := range state {
			if state[i].BalanceMinor > 0 {
				accrued := state[i].BalanceMinor * state[i].APRBps / (10000 * 12)
				state[i].BalanceMinor += accrued
				res.TotalInterestMinor += accrued
			}
		}
		// 2. Pay minimums where affordable; infeasible budgets degrade to
		// paying what exists.
		remaining := budget
		for i := range state {
			if state[i].BalanceMinor <= 0 {
				continue
			}
			pay := state[i].MinPayMinor
			if pay > remaining {
				pay = remaining
			}
			if pay > state[i].BalanceMinor {
				pay = state[i].BalanceMinor
			}
			state[i].BalanceMinor -= pay
			remaining -= pay
		}
		// 3. Extra budget goes to the strategy target.
		for _, name := range order {
			for i := range state {
				if state[i].Name == name && state[i].BalanceMinor > 0 && remaining > 0 {
					pay := remaining
					if pay > state[i].BalanceMinor {
						pay = state[i].BalanceMinor
					}
					state[i].BalanceMinor -= pay
					remaining -= pay
				}
			}
		}
		res.MonthsToClear = month + 1
	}
	// A budget below total minimums never converges: balances grow instead of
	// shrink. Mark that honestly instead of reporting a misleading month count.
	if !allCleared(state) {
		res.MonthsToClear = 0
		res.Feasible = false
	}
	return res
}

func minSum(debts []Debt) int64 {
	s := int64(0)
	for _, d := range debts {
		s += d.MinPayMinor
	}
	return s
}

func allCleared(debts []Debt) bool {
	for _, d := range debts {
		if d.BalanceMinor > 0 {
			return false
		}
	}
	return true
}

func payoffOrder(debts []Debt, strat StrategyName) []string {
	sorted := append([]Debt(nil), debts...)
	switch strat {
	case StrategyAvalanche:
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].APRBps > sorted[j].APRBps })
	case StrategySnowball:
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].BalanceMinor < sorted[j].BalanceMinor })
	default:
		return nil // minimum-only has no extra-payment target
	}
	var names []string
	for _, d := range sorted {
		names = append(names, d.Name)
	}
	return names
}

// CompareStrategies returns the headline: interest saved vs minimum-only.
func CompareStrategies(results []StrategyResult) (best StrategyResult, savedMinor int64, ok bool) {
	var baseline *StrategyResult
	for _, r := range results {
		if r.Strategy == StrategyMinimum {
			baseline = &r
		}
		if r.Strategy != StrategyMinimum && (ok == false || r.TotalInterestMinor < best.TotalInterestMinor) {
			best = r
			ok = true
		}
	}
	if baseline == nil || !ok {
		return best, 0, false
	}
	return best, baseline.TotalInterestMinor - best.TotalInterestMinor, true
}

// ── 8. Bureau data correction centre ────────────────────────────────────────

// CorrectionState is the dispute case lifecycle.
type CorrectionState string

const (
	CorrOpened    CorrectionState = "OPENED"
	CorrEvidence  CorrectionState = "EVIDENCE_COLLECTED"
	CorrSubmitted CorrectionState = "SUBMITTED_TO_BUREAU"
	CorrAwaiting  CorrectionState = "AWAITING_PROVIDER"
	CorrUpdated   CorrectionState = "FILE_UPDATED"
	CorrRejected  CorrectionState = "REJECTED"
	CorrEscalated CorrectionState = "ESCALATED"
)

// ResponseWindow is how long the provider has to respond before auto-escalation.
const ResponseWindow = 28 * 24 * time.Hour

// CorrectionCase is one disputed entry on a customer's credit file.
type CorrectionCase struct {
	ID          string          `json:"id"`
	CustomerID  string          `json:"customer_id"`
	Field       string          `json:"field"`       // BALANCE, LATE_PAYMENT, ACCOUNT_STATUS
	ReportedBy  string          `json:"reported_by"` // customer, proactive-scan
	Detail      string          `json:"detail"`
	State       CorrectionState `json:"state"`
	Evidence    []string        `json:"evidence"`
	SubmittedAt time.Time       `json:"submitted_at"`
	Deadline    time.Time       `json:"response_deadline"`
}

// CorrectionEngine runs bureau dispute cases.
type CorrectionEngine struct {
	cases map[string]*CorrectionCase
}

// NewCorrectionEngine creates the engine.
func CorrectionEngineNew() *CorrectionEngine {
	return &CorrectionEngine{cases: map[string]*CorrectionCase{}}
}

// Open starts a dispute case.
func (e *CorrectionEngine) Open(id, customerID, field, detail, reportedBy string, now time.Time) *CorrectionCase {
	c := &CorrectionCase{ID: id, CustomerID: customerID, Field: field, Detail: detail,
		ReportedBy: reportedBy, State: CorrOpened}
	e.cases[id] = c
	return c
}

// AttachEvidence moves OPENED → EVIDENCE_COLLECTED once at least one item exists.
func (e *CorrectionEngine) AttachEvidence(id, item string) error {
	c, ok := e.cases[id]
	if !ok {
		return fmt.Errorf("case %s not found", id)
	}
	if c.State != CorrOpened && c.State != CorrEvidence {
		return fmt.Errorf("cannot attach evidence in state %s", c.State)
	}
	c.Evidence = append(c.Evidence, item)
	c.State = CorrEvidence
	return nil
}

// Submit sends the dispute to the bureau. Evidence is mandatory — a dispute
// without evidence is noise that erodes provider responsiveness.
func (e *CorrectionEngine) Submit(id string, now time.Time) error {
	c, ok := e.cases[id]
	if !ok {
		return fmt.Errorf("case %s not found", id)
	}
	if c.State != CorrEvidence {
		return fmt.Errorf("submit requires EVIDENCE_COLLECTED, got %s", c.State)
	}
	c.State = CorrSubmitted
	c.SubmittedAt = now
	c.Deadline = now.Add(ResponseWindow)
	c.State = CorrAwaiting
	return nil
}

// Tick advances time: awaiting cases past their deadline auto-escalate —
// the customer must never be the one who has to chase.
func (e *CorrectionEngine) Tick(now time.Time) (escalated []string) {
	for id, c := range e.cases {
		if c.State == CorrAwaiting && now.After(c.Deadline) {
			c.State = CorrEscalated
			escalated = append(escalated, id)
		}
	}
	sort.Strings(escalated)
	return escalated
}

// Resolve records the provider outcome.
func (e *CorrectionEngine) Resolve(id string, updated bool) error {
	c, ok := e.cases[id]
	if !ok {
		return fmt.Errorf("case %s not found", id)
	}
	switch c.State {
	case CorrAwaiting, CorrEscalated:
		if updated {
			c.State = CorrUpdated
		} else {
			c.State = CorrRejected
		}
		return nil
	default:
		return fmt.Errorf("cannot resolve case in state %s", c.State)
	}
}

// ── 9. Credit decision simulation sandbox ───────────────────────────────────

// Applicant is one sandboxed population row.
type Applicant struct {
	ID             string  `json:"id"`
	Segment        string  `json:"segment"` // fairness segment (age band, region…)
	Score          float64 `json:"score"`   // 0..1 internal score
	Arrears12m     int     `json:"arrears_12m"`
	IncomeMinor    int64   `json:"income_minor"`
	RequestedMinor int64   `json:"requested_minor"`
	// ObservedDefault marks customers who later defaulted (backtest data).
	ObservedDefault bool `json:"observed_default"`
}

// Policy is a credit decision policy.
type Policy struct {
	Name       string  `json:"name"`
	MinScore   float64 `json:"min_score"`
	MaxArrears int     `json:"max_arrears_12m"`
	// MinIncomeCover: requested amount must not exceed income × factor.
	MinIncomeCover float64 `json:"min_income_cover"`
}

// Decide applies the policy to one applicant.
func (p Policy) Decide(a Applicant) bool {
	if a.Score < p.MinScore || a.Arrears12m > p.MaxArrears {
		return false
	}
	if a.IncomeMinor > 0 && float64(a.RequestedMinor)/float64(a.IncomeMinor) > p.MaxIncomeCoverFrac() {
		return false
	}
	return true
}

func (p Policy) MaxIncomeCoverFrac() float64 {
	if p.MinIncomeCover <= 0 {
		return math.MaxFloat64
	}
	return 1 / p.MinIncomeCover
}

// SandboxResult compares a candidate policy to the incumbent over one population.
type SandboxResult struct {
	Policy        string             `json:"policy"`
	ApprovalRate  float64            `json:"approval_rate"`
	BaselineRate  float64            `json:"baseline_approval_rate"`
	DefaultRate   float64            `json:"simulated_default_rate"`
	SegmentGaps   map[string]float64 `json:"segment_approval_gaps"`
	MaxSegmentGap float64            `json:"max_segment_gap"`
	SafeToRollout bool               `json:"safe_to_rollout"`
	Reasons       []string           `json:"reasons"`
}

// DefaultRateGate and SegmentGapGate are the sandbox guardrails.
const (
	DefaultRateGate = 0.05
	SegmentGapGate  = 0.10
)

// RunSandbox replays a candidate policy over the population and evaluates
// approval shift, simulated default rate (observed defaults among approvals)
// and fairness gaps between segments.
func RunSandbox(candidate Policy, baseline Policy, population []Applicant) *SandboxResult {
	res := &SandboxResult{Policy: candidate.Name, SegmentGaps: map[string]float64{}}
	if len(population) == 0 {
		res.Reasons = append(res.Reasons, "empty population")
		return res
	}
	approvedCand := 0
	approvedBase := 0
	defaultsAmongApproved := 0
	segTotal := map[string]int{}
	segApproved := map[string]int{}
	for _, a := range population {
		base := baseline.Decide(a)
		cand := candidate.Decide(a)
		if base {
			approvedBase++
		}
		if cand {
			approvedCand++
			segApproved[a.Segment]++
			if a.ObservedDefault {
				defaultsAmongApproved++
			}
		}
		segTotal[a.Segment]++
	}
	n := float64(len(population))
	res.ApprovalRate = float64(approvedCand) / n
	res.BaselineRate = float64(approvedBase) / n
	if approvedCand > 0 {
		res.DefaultRate = float64(defaultsAmongApproved) / float64(approvedCand)
	}
	// Segment approval gaps vs the overall rate.
	for seg, total := range segTotal {
		res.SegmentGaps[seg] = res.ApprovalRate - float64(segApproved[seg])/float64(total)
		if math.Abs(res.SegmentGaps[seg]) > math.Abs(res.MaxSegmentGap) {
			res.MaxSegmentGap = res.SegmentGaps[seg]
		}
	}
	if math.Abs(res.ApprovalRate-res.BaselineRate) > 0.25 {
		res.Reasons = append(res.Reasons, fmt.Sprintf("approval rate shifts %.0f%% → %.0f%% (>25pp) — staged rollout required", res.BaselineRate*100, res.ApprovalRate*100))
	}
	if res.DefaultRate > DefaultRateGate {
		res.Reasons = append(res.Reasons, fmt.Sprintf("simulated default rate %.1f%% exceeds gate %.1f%%", res.DefaultRate*100, DefaultRateGate*100))
	}
	if math.Abs(res.MaxSegmentGap) > SegmentGapGate {
		res.Reasons = append(res.Reasons, fmt.Sprintf("segment approval gap %.0fpp exceeds fairness gate %.0fpp", res.MaxSegmentGap*100, SegmentGapGate*100))
	}
	res.SafeToRollout = len(res.Reasons) == 0
	return res
}

// ── 10. Credit fairness monitor (live) ──────────────────────────────────────

// FairnessObs is one production decision observation.
type FairnessObs struct {
	Segment  string    `json:"segment"`
	Approved bool      `json:"approved"`
	Score    float64   `json:"score"`
	At       time.Time `json:"at"`
}

// FairnessAlert fires when a segment's approval rate diverges from the fleet.
type FairnessAlert struct {
	Segment      string    `json:"segment"`
	ApprovalRate float64   `json:"segment_approval_rate"`
	FleetRate    float64   `json:"fleet_approval_rate"`
	Disparity    float64   `json:"disparity"`
	At           time.Time `json:"at"`
}

// FairnessMonitor tracks per-segment decision rates over a rolling window.
type FairnessMonitor struct {
	disparityGate float64
	minPerSegment int
	obs           []FairnessObs
	window        time.Duration
}

// NewFairnessMonitor builds a monitor with the given disparity gate (e.g.
// 0.08 = 8pp gap alarms) and minimum sample size per segment.
func NewFairnessMonitor(disparityGate float64, minPerSegment int, window time.Duration) *FairnessMonitor {
	return &FairnessMonitor{disparityGate: disparityGate, minPerSegment: minPerSegment, window: window}
}

// Observe records one production decision.
func (m *FairnessMonitor) Observe(o FairnessObs) {
	m.obs = append(m.obs, o)
}

// Evaluate computes fleet vs segment approval rates inside the window and
// returns alerts for segments breaching the disparity gate with sufficient
// sample size. Sample-size discipline matters: alarming on 3 observations
// trains teams to ignore alerts.
func (m *FairnessMonitor) Evaluate(now time.Time) []FairnessAlert {
	cutoff := now.Add(-m.window)
	var inWindow []FairnessObs
	for _, o := range m.obs {
		if o.At.After(cutoff) {
			inWindow = append(inWindow, o)
		}
	}
	if len(inWindow) == 0 {
		return nil
	}
	fleetApproved := 0
	segTotal := map[string]int{}
	segApproved := map[string]int{}
	for _, o := range inWindow {
		if o.Approved {
			fleetApproved++
			segApproved[o.Segment]++
		}
		segTotal[o.Segment]++
	}
	fleet := float64(fleetApproved) / float64(len(inWindow))
	var alerts []FairnessAlert
	segs := make([]string, 0, len(segTotal))
	for s := range segTotal {
		segs = append(segs, s)
	}
	sort.Strings(segs)
	for _, s := range segs {
		if segTotal[s] < m.minPerSegment {
			continue
		}
		rate := float64(segApproved[s]) / float64(segTotal[s])
		disp := rate - fleet
		if math.Abs(disp) > m.disparityGate {
			alerts = append(alerts, FairnessAlert{
				Segment: s, ApprovalRate: rate, FleetRate: fleet, Disparity: disp, At: now,
			})
		}
	}
	return alerts
}
