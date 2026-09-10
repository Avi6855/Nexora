// Package mortgage implements Nexora's homeownership journey engines:
//
//  11. Affordability workspace: a household's true monthly homeownership
//     cost — mortgage payment at current rates, council tax band, insurance,
//     and the deposit runway — computed from stated income and spending.
//
//  12. Document state machine: every requested document moves through its own
//     async lifecycle (REQUESTED→UPLOADED→VALIDATING→VALIDATED / MISSING_
//     EVIDENCE→REVIEWED). Provider outages park work in a queue with retry
//     timers instead of failing the application; the application aggregates
//     document states.
//
//  13. Offer expiry protection: a mortgage offer has a hard validity date.
//     The engine watches remaining tasks and notifies the customer AND the
//     broker ahead of expiry, then drives the renewal workflow if the offer
//     lapses with completion unfinished.
package mortgage

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// ── 11. Home-buying affordability workspace ─────────────────────────────────

// AffordabilityInputs is the customer's stated financial picture.
type AffordabilityInputs struct {
	GrossAnnualIncomeMinor  int64 `json:"gross_annual_income_minor"`
	DepositMinor            int64 `json:"deposit_minor"`
	PropertyPriceMinor      int64 `json:"property_price_minor"`
	MonthlyCommitmentsMinor int64 `json:"monthly_commitments_minor"` // loans, cards
	MonthlySpendMinor       int64 `json:"monthly_spend_minor"`       // measured spending
	CouncilTaxMonthlyMinor  int64 `json:"council_tax_monthly_minor"`
	InsuranceMonthlyMinor   int64 `json:"insurance_monthly_minor"`
	AnnualRateBps           int64 `json:"annual_rate_bps"`
	TermYears               int   `json:"term_years"`
}

// AffordabilityResult is the workspace output.
type AffordabilityResult struct {
	MortgageAmountMinor      int64    `json:"mortgage_amount_minor"`
	LTV                      float64  `json:"ltv"`
	MonthlyPaymentMinor      int64    `json:"monthly_payment_minor"`
	TotalMonthlyCostMinor    int64    `json:"total_monthly_cost_minor"`
	StressTestedPaymentMinor int64    `json:"stress_tested_payment_minor"`
	Affordable               bool     `json:"affordable"`
	Reasons                  []string `json:"reasons,omitempty"`
}

// monthlyPayment: standard amortisation, exact per-month rate = APR/12.
// rate=0 degenerates to straight-line division.
func monthlyPayment(principalMinor int64, annualRateBps int64, months int) int64 {
	if months <= 0 {
		return 0
	}
	r := float64(annualRateBps) / (10000 * 12)
	p := float64(principalMinor)
	if r == 0 {
		return int64(p / float64(months))
	}
	// M = P·r(1+r)^n / ((1+r)^n − 1)
	factor := pow1plus(r, months)
	return int64(p * r * factor / (factor - 1))
}

func pow1plus(r float64, n int) float64 {
	out := 1.0
	for i := 0; i < n; i++ {
		out *= 1 + r
	}
	return out
}

// StressRateBpsAdd is the regulatory-style stress add-on applied to the rate
// for affordability testing.
const StressRateBpsAdd = 300

// ComputeAffordability assembles the workspace numbers. Affordability is
// judged on the STRESS-TESTED payment, not the headline rate: a customer who
// only affords today's rate cannot afford the house.
func ComputeAffordability(in AffordabilityInputs) (*AffordabilityResult, error) {
	if in.PropertyPriceMinor <= 0 || in.TermYears <= 0 {
		return nil, errors.New("property price and term are required")
	}
	deposit := in.DepositMinor
	if deposit > in.PropertyPriceMinor {
		return nil, errors.New("deposit exceeds property price")
	}
	loan := in.PropertyPriceMinor - deposit
	months := in.TermYears * 12
	payment := monthlyPayment(loan, in.AnnualRateBps, months)
	stressPayment := monthlyPayment(loan, in.AnnualRateBps+StressRateBpsAdd, months)
	totalMonthly := payment + in.CouncilTaxMonthlyMinor + in.InsuranceMonthlyMinor

	res := &AffordabilityResult{
		MortgageAmountMinor:      loan,
		LTV:                      float64(loan) / float64(in.PropertyPriceMinor),
		MonthlyPaymentMinor:      payment,
		TotalMonthlyCostMinor:    totalMonthly,
		StressTestedPaymentMinor: stressPayment,
		Affordable:               true, // explicit; the gates below flip it
	}
	// Income available after existing commitments, before measured spending.
	monthlyIncome := in.GrossAnnualIncomeMinor / 12
	available := monthlyIncome - in.MonthlyCommitmentsMinor
	if stressPayment+in.CouncilTaxMonthlyMinor+in.InsuranceMonthlyMinor > available-in.MonthlySpendMinor {
		res.Affordable = false
		res.Reasons = append(res.Reasons, "stress-tested payment exceeds income after commitments and measured spending")
	}
	if res.LTV > 0.95 {
		res.Affordable = false
		res.Reasons = append(res.Reasons, "LTV above 95% — increase deposit")
	}
	if res.Affordable {
		res.Reasons = append(res.Reasons, fmt.Sprintf("estimated monthly cost £%d.%02d, stress-tested at %d bps above current rate",
			res.TotalMonthlyCostMinor/100, res.TotalMonthlyCostMinor%100, StressRateBpsAdd))
	}
	return res, nil
}

// ── 12. Mortgage application document state machine ─────────────────────────

// DocState is one document's async lifecycle.
type DocState string

const (
	DocRequested  DocState = "REQUESTED"
	DocUploaded   DocState = "UPLOADED"
	DocValidating DocState = "VALIDATING"
	DocValidated  DocState = "VALIDATED"
	DocMissing    DocState = "MISSING_EVIDENCE"
	DocReviewed   DocState = "REVIEWED"
	DocRejected   DocState = "REJECTED"
)

var docTransitions = map[DocState][]DocState{
	DocRequested:  {DocUploaded},
	DocUploaded:   {DocValidating, DocMissing},
	DocValidating: {DocValidated, DocMissing, DocRejected},
	DocMissing:    {DocUploaded},
	DocValidated:  {DocReviewed, DocMissing},
	DocRejected:   {DocUploaded},
	DocReviewed:   {},
}

// ProviderState tracks the external validation provider's availability.
type ProviderState struct {
	Name    string
	Up      bool
	RetryIn time.Duration
}

// Application aggregates document state machines with a provider-aware queue.
type Application struct {
	ID       string
	Docs     map[string]*DocumentTask
	Provider ProviderState
}

// DocumentTask is one requested document.
type DocumentTask struct {
	Kind      string    `json:"kind"` // PAYSLIP, ID_DOCUMENT, BANK_STATEMENT
	State     DocState  `json:"state"`
	Deadline  time.Time `json:"deadline,omitempty"`
	Attempts  int       `json:"attempts"`
	NextRetry time.Time `json:"next_retry,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// NewApplication creates an application with requested documents.
func NewApplication(id string, kinds []string, deadline time.Time) *Application {
	a := &Application{ID: id, Docs: map[string]*DocumentTask{},
		Provider: ProviderState{Name: "kyc-validator", Up: true}}
	for _, k := range kinds {
		a.Docs[k] = &DocumentTask{Kind: k, State: DocRequested, Deadline: deadline}
	}
	return a
}

// Transition moves a document if the transition is legal.
func (a *Application) Transition(kind string, to DocState) error {
	d, ok := a.Docs[kind]
	if !ok {
		return fmt.Errorf("no document %s requested", kind)
	}
	for _, allowed := range docTransitions[d.State] {
		if allowed == to {
			d.State = to
			d.Note = ""
			return nil
		}
	}
	return fmt.Errorf("illegal document transition %s: %s → %s", kind, d.State, to)
}

// Upload marks a document uploaded.
func (a *Application) Upload(kind string, now time.Time) error {
	if err := a.Transition(kind, DocUploaded); err != nil {
		return err
	}
	// Async validation starts automatically unless the provider is down.
	return a.StartValidation(kind, now)
}

// StartValidation begins async provider validation, or parks the task in the
// retry queue when the provider is unavailable — the application never fails
// because a third party is down.
func (a *Application) StartValidation(kind string, now time.Time) error {
	d, ok := a.Docs[kind]
	if !ok {
		return fmt.Errorf("no document %s", kind)
	}
	if err := a.Transition(kind, DocValidating); err != nil {
		return err
	}
	if !a.Provider.Up {
		d.Attempts++
		d.NextRetry = now.Add(a.Provider.RetryIn)
		d.Note = "queued: validation provider unavailable"
	}
	return nil
}

// DueForRetry lists parked documents whose retry timer has elapsed.
func (a *Application) DueForRetry(now time.Time) []string {
	var due []string
	kinds := make([]string, 0, len(a.Docs))
	for k := range a.Docs {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		d := a.Docs[k]
		if d.State == DocValidating && !d.NextRetry.IsZero() && !d.NextRetry.After(now) {
			due = append(due, k)
		}
	}
	return due
}

// Completeness reports how many documents are reviewed — the underwriter's view.
func (a *Application) Completeness() (done, total int) {
	total = len(a.Docs)
	for _, d := range a.Docs {
		if d.State == DocReviewed {
			done++
		}
	}
	return done, total
}

// ── 13. Mortgage offer expiry protection ────────────────────────────────────

// OfferTask is one task that must complete before drawdown.
type OfferTask struct {
	Name  string `json:"name"`
	Done  bool   `json:"done"`
	Owner string `json:"owner"` // CUSTOMER, BROKER, SOLICITOR
}

// Offer is a mortgage offer with expiry protection.
type Offer struct {
	ID        string      `json:"id"`
	ValidTo   time.Time   `json:"valid_to"`
	Tasks     []OfferTask `json:"tasks"`
	Completed bool        `json:"completed"`
}

// ExpiryWarning is what the notification engine sends.
type ExpiryWarning struct {
	OfferID        string   `json:"offer_id"`
	DaysRemaining  int      `json:"days_remaining"`
	OpenTasks      []string `json:"open_tasks"`
	NotifyCustomer bool     `json:"notify_customer"`
	NotifyBroker   bool     `json:"notify_broker"`
	RenewalNeeded  bool     `json:"renewal_needed"`
}

// EvaluateExpiry checks the offer at `now` and produces the correct warning
// level: nothing → customer+broker nudge → renewal workflow.
func EvaluateExpiry(o *Offer, now time.Time) *ExpiryWarning {
	days := int(o.ValidTo.Sub(now).Hours() / 24)
	w := &ExpiryWarning{OfferID: o.ID, DaysRemaining: days}
	for _, t := range o.Tasks {
		if !t.Done {
			w.OpenTasks = append(w.OpenTasks, t.Name+" ("+t.Owner+")")
		}
	}
	switch {
	case o.Completed:
		// Nothing to do.
	case days < 0:
		w.RenewalNeeded = len(w.OpenTasks) > 0
		if w.RenewalNeeded {
			w.NotifyCustomer = true
			w.NotifyBroker = true
		}
	case days <= 14:
		w.NotifyCustomer = true
		w.NotifyBroker = true
	case days <= 30:
		w.NotifyCustomer = true
	}
	return w
}

// MarkTaskDone completes a task by name.
func (o *Offer) MarkTaskDone(name string) error {
	for i := range o.Tasks {
		if o.Tasks[i].Name == name {
			o.Tasks[i].Done = true
			o.checkComplete()
			return nil
		}
	}
	return fmt.Errorf("task %s not found", name)
}

func (o *Offer) checkComplete() {
	for _, t := range o.Tasks {
		if !t.Done {
			return
		}
	}
	o.Completed = true
}
