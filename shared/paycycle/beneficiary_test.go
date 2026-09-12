package paycycle

import (
	"errors"
	"testing"
	"time"
)

func TestBeneficiaryLifecycle(t *testing.T) {
	s := NewStore()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)

	b, err := s.Add("owner-1", "Landlord", "040004", "12345678", now)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != BeneficiaryCreated {
		t.Fatalf("new beneficiary state %s, want CREATED", b.State)
	}
	if !b.FirstPaymentEver {
		t.Fatal("first-payment-ever flag must be set on add")
	}
	if b.Fingerprint != Fingerprint("04-00-04", "12345678") {
		t.Fatal("fingerprint must be normalisation-independent")
	}

	// Paying before verification is refused.
	if _, err = s.MarkPaid(b.ID, now); !errors.Is(err, ErrBeneficiaryState) {
		t.Fatalf("unverified payment must fail with state error, got %v", err)
	}

	v, err := s.Verify(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != BeneficiaryVerified {
		t.Fatalf("after verify %s, want VERIFIED", v.State)
	}

	paid, err := s.MarkPaid(b.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if paid.State != BeneficiaryTrusted {
		t.Fatalf("after first payment %s, want TRUSTED", paid.State)
	}
	if paid.FirstPaymentEver {
		t.Fatal("first-payment-ever flag must clear after first payment")
	}
	if paid.Payments != 1 || paid.LastPaidAt == nil {
		t.Fatalf("payment counters not recorded: %+v", paid)
	}

	// Second payment keeps trust.
	again, err := s.MarkPaid(b.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.State != BeneficiaryTrusted || again.Payments != 2 {
		t.Fatalf("repeat payment must stay TRUSTED: %+v", again)
	}
}

func TestBeneficiaryDetailChangeResetsTrust(t *testing.T) {
	s := NewStore()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	b, err := s.Add("owner-1", "Supplier", "040004", "11111111", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarkPaid(b.ID, now); err != nil {
		t.Fatal(err)
	}

	// Identical details: no change, no state touched.
	changed, same, err := s.UpdateDetails(b.ID, "04-00-04", "11111111", now)
	if err != nil || changed {
		t.Fatalf("identical details must report unchanged: changed=%v err=%v", changed, err)
	}
	if same.State != BeneficiaryTrusted {
		t.Fatalf("unchanged update must keep TRUSTED, got %s", same.State)
	}

	// Changed details: trust resets, cooling starts.
	changed, risky, err := s.UpdateDetails(b.ID, "040005", "22222222", now)
	if err != nil || !changed {
		t.Fatalf("changed details must report changed: changed=%v err=%v", changed, err)
	}
	if risky.State != BeneficiaryRiskIncreased {
		t.Fatalf("after detail change %s, want RISK_INCREASED", risky.State)
	}
	if !risky.InCooling(now.Add(time.Hour)) {
		t.Fatal("cooling period must be active after detail change")
	}
	if risky.InCooling(now.Add(25 * time.Hour)) {
		t.Fatal("cooling period must expire after 24h")
	}

	// Payments during cooling are refused.
	if _, err = s.MarkPaid(b.ID, now.Add(time.Hour)); !errors.Is(err, ErrBeneficiaryCooling) {
		t.Fatalf("cooling payment must fail with cooling error, got %v", err)
	}

	// Reverification path: RISK_INCREASED -> REVERIFICATION -> VERIFIED.
	step1, err := s.Verify(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if step1.State != BeneficiaryReverification {
		t.Fatalf("first verify after risk %s, want REVERIFICATION", step1.State)
	}
	step2, err := s.Verify(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if step2.State != BeneficiaryVerified {
		t.Fatalf("second verify %s, want VERIFIED", step2.State)
	}
	if step2.InCooling(now.Add(25 * time.Hour)) {
		t.Fatal("cooling must clear once reverified")
	}
	if _, err = s.MarkPaid(b.ID, now.Add(25*time.Hour)); err != nil {
		t.Fatalf("payment after reverification must succeed: %v", err)
	}
}

func TestBeneficiaryDormantAndRemove(t *testing.T) {
	s := NewStore()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	b, err := s.Add("owner-1", "Old Friend", "040004", "33333333", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(b.ID); err != nil {
		t.Fatal(err)
	}

	d, err := s.TouchDormant(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != BeneficiaryDormant {
		t.Fatalf("after dormant %s, want DORMANT", d.State)
	}
	// Dormant is idempotent.
	if _, err = s.TouchDormant(b.ID); err != nil {
		t.Fatalf("repeat dormant must be idempotent: %v", err)
	}
	// Dormant reactivates through verification then payment.
	if _, err = s.Verify(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarkPaid(b.ID, now); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(b.ID)
	if st.State != BeneficiaryTrusted {
		t.Fatalf("reactivated beneficiary %s, want TRUSTED", st.State)
	}

	// Remove tombstones; further transitions fail; unknown ids 404.
	rm, err := s.Remove(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rm.State != BeneficiaryRemoved {
		t.Fatalf("after remove %s, want REMOVED", rm.State)
	}
	if _, err = s.Remove(b.ID); !errors.Is(err, ErrBeneficiaryState) {
		t.Fatalf("double remove must fail with state error, got %v", err)
	}
	if _, err = s.MarkPaid(b.ID, now); !errors.Is(err, ErrBeneficiaryState) {
		t.Fatalf("paying removed must fail, got %v", err)
	}
	if _, err = s.Status("ben-missing"); !errors.Is(err, ErrBeneficiaryNotFound) {
		t.Fatalf("unknown beneficiary must 404, got %v", err)
	}
	if _, err = s.Verify("ben-missing"); !errors.Is(err, ErrBeneficiaryNotFound) {
		t.Fatalf("verify unknown must 404, got %v", err)
	}
}

func TestBeneficiaryValidationAndDuplicates(t *testing.T) {
	s := NewStore()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	if _, err := s.Add("o", "", "040004", "12345678", now); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := s.Add("o", "X", "", "12345678", now); err == nil {
		t.Fatal("empty sort code must fail")
	}
	if _, err := s.Add("o", "X", "040004", "12345678", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("o", "Dupe", "04-00-04", "12345678", now); !errors.Is(err, ErrBeneficiaryExists) {
		t.Fatalf("duplicate account must fail with exists error, got %v", err)
	}
}
