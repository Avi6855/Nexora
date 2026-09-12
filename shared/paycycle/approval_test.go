package paycycle

import (
	"errors"
	"testing"
	"time"
)

func mustRule(t *testing.T, e *Engine, r ApprovalRule) ApprovalRule {
	t.Helper()
	got, err := e.AddRule(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestApprovalEvaluateSeverity(t *testing.T) {
	e := NewEngine()
	mustRule(t, e, ApprovalRule{ID: "big", MinAmountMinor: 100000, Decision: DecisionRequireApproval})
	mustRule(t, e, ApprovalRule{ID: "sanctioned", Country: "KP", Decision: DecisionBlock})
	mustRule(t, e, ApprovalRule{ID: "slow", MinAmountMinor: 50000, Decision: DecisionDelay})

	// Block beats approval beats delay.
	d, id := e.Evaluate(PaymentIntent{AmountMinor: 200000, Country: "KP"})
	if d != DecisionBlock || id != "sanctioned" {
		t.Fatalf("got %s/%s, want BLOCK/sanctioned", d, id)
	}
	d, id = e.Evaluate(PaymentIntent{AmountMinor: 200000, Country: "GB"})
	if d != DecisionRequireApproval || id != "big" {
		t.Fatalf("got %s/%s, want REQUIRE_APPROVAL/big", d, id)
	}
	d, _ = e.Evaluate(PaymentIntent{AmountMinor: 60000, Country: "GB"})
	if d != DecisionDelay {
		t.Fatalf("got %s, want DELAY", d)
	}
	// Nothing matches -> ALLOW with empty rule.
	d, id = e.Evaluate(PaymentIntent{AmountMinor: 100, Country: "GB"})
	if d != DecisionAllow || id != "" {
		t.Fatalf("got %s/%s, want ALLOW/empty", d, id)
	}
}

func TestApprovalRuleFilters(t *testing.T) {
	e := NewEngine()
	mustRule(t, e, ApprovalRule{ID: "amazon", Merchant: "amazon", MinAmountMinor: 10000, Decision: DecisionRequireApproval})
	mustRule(t, e, ApprovalRule{ID: "first", NewBeneficiaryOnly: true, MinAmountMinor: 5000, Decision: DecisionRequireApproval})

	// Merchant is a case-insensitive substring.
	if d, _ := e.Evaluate(PaymentIntent{AmountMinor: 20000, Merchant: "AMAZON UK"}); d != DecisionRequireApproval {
		t.Fatalf("amazon substring must match, got %s", d)
	}
	if d, _ := e.Evaluate(PaymentIntent{AmountMinor: 20000, Merchant: "Tesco"}); d != DecisionAllow {
		t.Fatalf("non-matching merchant must allow, got %s", d)
	}
	// New-beneficiary gate.
	if d, _ := e.Evaluate(PaymentIntent{AmountMinor: 6000, IsNewBeneficiary: true}); d != DecisionRequireApproval {
		t.Fatalf("new beneficiary must require approval, got %s", d)
	}
	if d, _ := e.Evaluate(PaymentIntent{AmountMinor: 6000}); d != DecisionAllow {
		t.Fatalf("known beneficiary below other floors must allow, got %s", d)
	}

	// Unknown decisions and duplicate ids are rejected.
	if _, err := e.AddRule(ApprovalRule{ID: "bad", Decision: "MAYBE"}); err == nil {
		t.Fatal("unknown decision must fail")
	}
	if _, err := e.AddRule(ApprovalRule{ID: "amazon", Decision: DecisionBlock}); !errors.Is(err, ErrApprovalExists) {
		t.Fatalf("duplicate rule id must fail with exists error, got %v", err)
	}
}

func TestApprovalRequestLifecycle(t *testing.T) {
	e := NewEngine()
	mustRule(t, e, ApprovalRule{ID: "big", MinAmountMinor: 100000, Decision: DecisionRequireApproval})
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)

	req, err := e.RequestApproval(PaymentIntent{AmountMinor: 150000, Country: "GB"}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != ApprovalPending || req.Decision != DecisionRequireApproval || req.RuleID != "big" {
		t.Fatalf("request not recorded correctly: %+v", req)
	}
	if !req.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expiry %s, want +1h", req.ExpiresAt)
	}

	// Approve path.
	got, err := e.DecideApproval(req.ID, true, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ApprovalApproved {
		t.Fatalf("status %s, want APPROVED", got.Status)
	}
	// Deciding twice is a conflict.
	if _, err = e.DecideApproval(req.ID, true, now.Add(31*time.Minute)); !errors.Is(err, ErrApprovalState) {
		t.Fatalf("double decide must fail with state error, got %v", err)
	}

	// Reject path.
	req2, _ := e.RequestApproval(PaymentIntent{AmountMinor: 150000}, time.Hour, now)
	dec, err := e.DecideApproval(req2.ID, false, now.Add(time.Minute))
	if err != nil || dec.Status != ApprovalRejected {
		t.Fatalf("reject must succeed: %+v %v", dec, err)
	}

	// Expiry path: deciding after the window flips to EXPIRED.
	req3, _ := e.RequestApproval(PaymentIntent{AmountMinor: 1}, time.Hour, now)
	if _, err = e.DecideApproval(req3.ID, true, now.Add(2*time.Hour)); !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("late decide must expire, got %v", err)
	}
	st, err := e.ApprovalStatus(req3.ID, now.Add(3*time.Hour))
	if err != nil || st.Status != ApprovalExpired {
		t.Fatalf("status after expiry must be EXPIRED: %+v %v", st, err)
	}
	// Lazy expiry via status read.
	req4, _ := e.RequestApproval(PaymentIntent{AmountMinor: 1}, time.Hour, now)
	st4, err := e.ApprovalStatus(req4.ID, now.Add(2*time.Hour))
	if err != nil || st4.Status != ApprovalExpired {
		t.Fatalf("lazy expiry must flip to EXPIRED: %+v %v", st4, err)
	}
	// Unknown request ids 404.
	if _, err = e.ApprovalStatus("apr-999999", now); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("unknown approval must 404, got %v", err)
	}
	if _, err = e.DecideApproval("apr-999999", true, now); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("decide unknown must 404, got %v", err)
	}
}
