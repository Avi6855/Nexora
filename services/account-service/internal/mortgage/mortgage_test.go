package mortgage

import (
	"testing"
	"time"

	sharedmortgage "github.com/nexora/nexora/shared/mortgage"
)

func TestEstimateAffordability(t *testing.T) {
	svc := NewService()
	res, err := svc.EstimateAffordability(sharedmortgage.AffordabilityInputs{
		GrossAnnualIncomeMinor:  6000000,
		DepositMinor:            5000000,
		PropertyPriceMinor:      20000000,
		MonthlyCommitmentsMinor: 20000,
		MonthlySpendMinor:       150000,
		CouncilTaxMonthlyMinor:  15000,
		InsuranceMonthlyMinor:   5000,
		AnnualRateBps:           450,
		TermYears:               25,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if res.MortgageAmountMinor != 15000000 {
		t.Fatalf("loan = %d, want 15000000", res.MortgageAmountMinor)
	}
	if res.MonthlyPaymentMinor <= 0 || res.StressTestedPaymentMinor <= res.MonthlyPaymentMinor {
		t.Fatalf("bad payments: %+v", res)
	}
	// LTV breach must flip affordability off.
	bad, err := svc.EstimateAffordability(sharedmortgage.AffordabilityInputs{
		GrossAnnualIncomeMinor: 6000000,
		DepositMinor:           10000,
		PropertyPriceMinor:     20000000,
		AnnualRateBps:          450,
		TermYears:              25,
	})
	if err != nil {
		t.Fatalf("ltv estimate: %v", err)
	}
	if bad.Affordable {
		t.Fatalf("expected unaffordable for LTV %.3f", bad.LTV)
	}
	if _, err := svc.EstimateAffordability(sharedmortgage.AffordabilityInputs{}); err == nil {
		t.Fatalf("expected error for empty inputs")
	}
}

func TestApplicationLifecycle(t *testing.T) {
	svc := NewService()
	now := time.Now()
	app, err := svc.OpenApplication("app-1", []string{"PAYSLIP", "ID_DOCUMENT"}, now.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Park validation: provider down queues retries instead of failing.
	app.Provider.Up = false
	app.Provider.RetryIn = time.Minute

	if err := svc.UploadDocument("app-1", "PAYSLIP", now); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := app.Docs["PAYSLIP"].State; got != sharedmortgage.DocValidating {
		t.Fatalf("state = %s, want VALIDATING", got)
	}
	if app.Docs["PAYSLIP"].NextRetry.IsZero() {
		t.Fatalf("expected parked retry timer")
	}
	due, err := svc.RetryDue("app-1", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("retries: %v", err)
	}
	if len(due) != 1 || due[0] != "PAYSLIP" {
		t.Fatalf("due = %v, want [PAYSLIP]", due)
	}
	early, _ := svc.RetryDue("app-1", now)
	if len(early) != 0 {
		t.Fatalf("early due = %v, want empty", early)
	}
	// Drive to reviewed and check completeness via the service view.
	if err := app.Transition("PAYSLIP", sharedmortgage.DocValidated); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := app.Transition("PAYSLIP", sharedmortgage.DocReviewed); err != nil {
		t.Fatalf("review: %v", err)
	}
	prog, err := svc.ApplicationProgress("app-1")
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if prog.Done != 1 || prog.Total != 2 {
		t.Fatalf("progress = %d/%d, want 1/2", prog.Done, prog.Total)
	}
	if err := svc.StartValidation("app-1", "ID_DOCUMENT", now); err == nil {
		t.Fatalf("expected illegal transition REQUESTED->VALIDATING")
	}
	if _, err := svc.ApplicationProgress("missing"); err == nil {
		t.Fatalf("expected not-found for progress")
	}
}

func TestOfferExpiry(t *testing.T) {
	svc := NewService()
	now := time.Now()
	tasks := []sharedmortgage.OfferTask{
		{Name: "sign", Owner: "CUSTOMER"},
		{Name: "valuation", Owner: "BROKER"},
	}
	if _, err := svc.CreateOffer("off-1", now.Add(60*24*time.Hour), tasks); err != nil {
		t.Fatalf("create: %v", err)
	}
	far, err := svc.CheckExpiry("off-1", now)
	if err != nil {
		t.Fatalf("expiry: %v", err)
	}
	if far.NotifyCustomer || far.NotifyBroker || far.RenewalNeeded {
		t.Fatalf("far warning should be quiet: %+v", far)
	}
	near, _ := svc.CheckExpiry("off-1", now.Add(50*24*time.Hour))
	if !near.NotifyCustomer || !near.NotifyBroker {
		t.Fatalf("10-day warning must notify both: %+v", near)
	}
	if _, err := svc.CompleteOfferTask("off-1", "sign"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	lapsed, _ := svc.CheckExpiry("off-1", now.Add(61*24*time.Hour))
	if !lapsed.RenewalNeeded || !lapsed.NotifyCustomer {
		t.Fatalf("lapsed offer needs renewal: %+v", lapsed)
	}
	if err := func() error {
		_, err := svc.CompleteOfferTask("off-1", "nope")
		return err
	}(); err == nil {
		t.Fatalf("expected error for unknown task")
	}
	if _, err := svc.CheckExpiry("missing", now); err == nil {
		t.Fatalf("expected not-found for expiry")
	}
}
