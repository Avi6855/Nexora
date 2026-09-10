package mortgage

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)

func TestAffordabilityWorkspace(t *testing.T) {
	in := AffordabilityInputs{
		GrossAnnualIncomeMinor:  7200000, // £72,000 → £600k/mo
		DepositMinor:            5000000, // £50,000
		PropertyPriceMinor:      25000000,
		MonthlyCommitmentsMinor: 30000,
		MonthlySpendMinor:       120000, // £1,200 measured spend
		CouncilTaxMonthlyMinor:  15000,
		InsuranceMonthlyMinor:   3000,
		AnnualRateBps:           450,
		TermYears:               25,
	}
	res, err := ComputeAffordability(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.MortgageAmountMinor != 20000000 {
		t.Fatalf("loan %d, want £200,000", res.MortgageAmountMinor)
	}
	if res.LTV != 0.8 {
		t.Fatalf("LTV %.2f, want 0.80", res.LTV)
	}
	if res.MonthlyPaymentMinor <= 0 || res.StressTestedPaymentMinor <= res.MonthlyPaymentMinor {
		t.Fatalf("stress payment must exceed headline: %+v", res)
	}
	// Comfortable income: affordable with an explanation.
	if !res.Affordable {
		t.Fatalf("should be affordable: %+v", res.Reasons)
	}
	// Thin-income case fails the stress test: £24k income → £200k/mo after
	// commitments; stress cost £1,477 + £1,200 spend exceeds it.
	tight := in
	tight.GrossAnnualIncomeMinor = 2700000 // £27k → £225k/mo
	res2, _ := ComputeAffordability(tight)
	if res2.Affordable {
		t.Fatalf("thin income must fail stress test: %+v", res2)
	}
	// High-LTV refused.
	hiLTV := in
	hiLTV.DepositMinor = 1000000
	res3, _ := ComputeAffordability(hiLTV)
	if res3.Affordable || res3.LTV != 0.96 {
		t.Fatalf("96%% LTV must be refused: %+v", res3)
	}
	if _, err := ComputeAffordability(AffordabilityInputs{PropertyPriceMinor: 0, TermYears: 25}); err == nil {
		t.Fatal("missing property price must error")
	}
}

func TestDocumentStateMachine(t *testing.T) {
	a := NewApplication("app-1", []string{"PAYSLIP", "ID_DOCUMENT"}, t0.Add(14*24*time.Hour))
	if _, ok := a.Docs["PAYSLIP"]; !ok {
		t.Fatal("documents requested")
	}
	// Illegal jump REQUESTED → VALIDATED.
	if err := a.Transition("PAYSLIP", DocValidated); err == nil {
		t.Fatal("must follow the machine")
	}
	if err := a.Upload("PAYSLIP", t0); err != nil {
		t.Fatal(err)
	}
	if a.Docs["PAYSLIP"].State != DocValidating {
		t.Fatalf("upload must auto-start validation, got %s", a.Docs["PAYSLIP"].State)
	}
	// Provider outage parks the task with a retry timer instead of failing.
	a.Provider.Up = false
	a.Provider.RetryIn = 15 * time.Minute
	if err := a.Upload("ID_DOCUMENT", t0); err != nil {
		t.Fatal(err)
	}
	d := a.Docs["ID_DOCUMENT"]
	if d.State != DocValidating || d.Note == "" || d.Attempts != 1 {
		t.Fatalf("provider down must queue: %+v", d)
	}
	if due := a.DueForRetry(t0.Add(time.Minute)); len(due) != 0 {
		t.Fatalf("not yet due: %v", due)
	}
	if due := a.DueForRetry(t0.Add(16 * time.Minute)); len(due) != 1 || due[0] != "ID_DOCUMENT" {
		t.Fatalf("retry must fire after timer: %v", due)
	}
	// Provider recovers; validation completes.
	a.Provider.Up = true
	if err := a.Transition("ID_DOCUMENT", DocValidated); err != nil {
		t.Fatal(err)
	}
	if err := a.Transition("ID_DOCUMENT", DocReviewed); err != nil {
		t.Fatal(err)
	}
	done, total := a.Completeness()
	if done != 1 || total != 2 {
		t.Fatalf("completeness %d/%d", done, total)
	}
	// Missing evidence loops back to uploaded.
	if err := a.Transition("PAYSLIP", DocMissing); err != nil {
		t.Fatal(err)
	}
	if err := a.Upload("PAYSLIP", t0); err != nil {
		t.Fatal(err)
	}
	if a.Docs["PAYSLIP"].State != DocValidating {
		t.Fatalf("re-upload must revalidate, got %s", a.Docs["PAYSLIP"].State)
	}
}

func TestOfferExpiryProtection(t *testing.T) {
	validTo := t0.Add(60 * 24 * time.Hour)
	o := &Offer{ID: "offer-1", ValidTo: validTo, Tasks: []OfferTask{
		{Name: "solicitor instructed", Owner: "BROKER"},
		{Name: "identity confirmed", Owner: "CUSTOMER"},
	}}
	// 60 days out: nothing.
	if w := EvaluateExpiry(o, t0); w.NotifyCustomer || w.RenewalNeeded {
		t.Fatalf("60 days out must be silent: %+v", w)
	}
	// 20 days out with open tasks: customer nudge only.
	if w := EvaluateExpiry(o, t0.Add(40*24*time.Hour)); !w.NotifyCustomer || w.NotifyBroker {
		t.Fatalf("20 days out: customer nudge: %+v", w)
	}
	// 10 days out: both customer and broker.
	if w := EvaluateExpiry(o, t0.Add(50*24*time.Hour)); !w.NotifyCustomer || !w.NotifyBroker {
		t.Fatalf("10 days out must warn both: %+v", w)
	}
	// Tasks complete before expiry → no renewal even after lapse.
	_ = o.MarkTaskDone("solicitor instructed")
	_ = o.MarkTaskDone("identity confirmed")
	if !o.Completed {
		t.Fatal("all tasks done must complete the offer")
	}
	if w := EvaluateExpiry(o, validTo.Add(24*time.Hour)); w.RenewalNeeded {
		t.Fatalf("completed offer never needs renewal: %+v", w)
	}
	// A lapsed offer with open tasks triggers the renewal workflow.
	o2 := &Offer{ID: "offer-2", ValidTo: t0.Add(-24 * time.Hour), Tasks: []OfferTask{
		{Name: "valuation", Owner: "SOLICITOR"},
	}}
	w := EvaluateExpiry(o2, t0)
	if !w.RenewalNeeded || !w.NotifyCustomer || !w.NotifyBroker {
		t.Fatalf("lapsed offer with open tasks must renew: %+v", w)
	}
	if len(w.OpenTasks) != 1 {
		t.Fatalf("open tasks listed: %+v", w.OpenTasks)
	}
}
