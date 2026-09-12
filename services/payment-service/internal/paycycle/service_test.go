package paycycle

import (
	"testing"
	"time"

	sharedpaycycle "github.com/nexora/nexora/shared/paycycle"
)

func testService() *Service {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		loc = time.UTC
	}
	return NewService(loc)
}

func TestServiceQuoteETA(t *testing.T) {
	s := testService()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, s.calendar.Location())
	q, err := s.QuoteETA("FASTER_PAYMENTS", 1000, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Late {
		t.Fatalf("faster payments must not be late: %+v", q)
	}
	if _, err = s.QuoteETA("NOPE", 1000, now); err == nil {
		t.Fatal("unknown rail must fail")
	}
	if _, err = s.QuoteETA("BACS", 0, now); err == nil {
		t.Fatal("zero amount must fail")
	}
}

func TestServiceBeneficiaryLifecycle(t *testing.T) {
	s := testService()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)

	b, err := s.AddBeneficiary("owner-1", "Landlord", "040004", "12345678", now)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != sharedpaycycle.BeneficiaryCreated {
		t.Fatalf("state %s, want CREATED", b.State)
	}
	if _, err = s.AddBeneficiary("owner-1", "Dupe", "04-00-04", "12345678", now); err == nil {
		t.Fatal("duplicate account must fail")
	}

	if _, err = s.VerifyBeneficiary(b.ID); err != nil {
		t.Fatal(err)
	}
	paid, err := s.RecordPayment(b.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if paid.State != sharedpaycycle.BeneficiaryTrusted {
		t.Fatalf("state %s, want TRUSTED", paid.State)
	}

	changed, risky, err := s.UpdateBeneficiaryDetails(b.ID, "040005", "87654321", now)
	if err != nil || !changed {
		t.Fatalf("detail change must apply: changed=%v err=%v", changed, err)
	}
	if risky.State != sharedpaycycle.BeneficiaryRiskIncreased {
		t.Fatalf("state %s, want RISK_INCREASED", risky.State)
	}
	if _, err = s.RecordPayment(b.ID, now.Add(time.Hour)); err == nil {
		t.Fatal("payment during cooling must fail")
	}

	if _, err = s.MarkDormant(b.ID); err == nil {
		t.Fatal("dormant from RISK_INCREASED must fail")
	}
	if _, err = s.BeneficiaryStatus("ben-missing"); err == nil {
		t.Fatal("unknown beneficiary must fail")
	}
	if _, err = s.RemoveBeneficiary(b.ID); err != nil {
		t.Fatal(err)
	}
	st, err := s.BeneficiaryStatus(b.ID)
	if err != nil || st.State != sharedpaycycle.BeneficiaryRemoved {
		t.Fatalf("removed status wrong: %+v %v", st, err)
	}
}

func TestServiceApprovalChain(t *testing.T) {
	s := testService()
	if _, err := s.AddApprovalRule(sharedpaycycle.ApprovalRule{ID: "big", MinAmountMinor: 100000, Decision: sharedpaycycle.DecisionRequireApproval}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApprovalRule(sharedpaycycle.ApprovalRule{ID: "big", Decision: sharedpaycycle.DecisionBlock}); err == nil {
		t.Fatal("duplicate rule must fail")
	}

	decision, ruleID := s.EvaluatePayment(sharedpaycycle.PaymentIntent{AmountMinor: 150000})
	if decision != sharedpaycycle.DecisionRequireApproval || ruleID != "big" {
		t.Fatalf("got %s/%s, want REQUIRE_APPROVAL/big", decision, ruleID)
	}
	decision, _ = s.EvaluatePayment(sharedpaycycle.PaymentIntent{AmountMinor: 10})
	if decision != sharedpaycycle.DecisionAllow {
		t.Fatalf("got %s, want ALLOW", decision)
	}

	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	req, err := s.RequestApproval(sharedpaycycle.PaymentIntent{AmountMinor: 150000}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := s.DecideApproval(req.ID, true, now.Add(time.Minute))
	if err != nil || dec.Status != sharedpaycycle.ApprovalApproved {
		t.Fatalf("approve must succeed: %+v %v", dec, err)
	}
	got, err := s.GetApproval(req.ID, now.Add(2*time.Minute))
	if err != nil || got.Status != sharedpaycycle.ApprovalApproved {
		t.Fatalf("status must be APPROVED: %+v %v", got, err)
	}
	if _, err = s.GetApproval("apr-999999", now); err == nil {
		t.Fatal("unknown approval must fail")
	}
}
