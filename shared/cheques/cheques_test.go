package cheques

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)

func goodCheque() Cheque {
	return Cheque{
		ID: "chq-1", AccountID: "acc-1", PayerName: "J Smith",
		DepositAmountMinor: 25000,
		Image:              ImageQuality{BlurScore: 0.1, GlareCoverage: 0.02, ResolutionDPI: 300},
		OCR: &OCRResult{
			AmountMinor: 25000, AmountConfidence: 0.97,
			Payee: "Nexora customer", PayeeConfidence: 0.9,
			MICRSortCode: "040004", MICRAccountNumber: "12345678", MICRValid: true,
		},
		AccountAgeDays: 200, At: t0,
	}
}

func TestImageQualityGates(t *testing.T) {
	c := goodCheque()
	c.Image.BlurScore = 0.8
	v := Evaluate(c)
	if v.Action != ActionRetake {
		t.Fatalf("blurry image must RETAKE: %+v", v)
	}
	if v.CustomerMsg == "" {
		t.Fatal("customer message required on retake")
	}
	// Glare alone is enough.
	c = goodCheque()
	c.Image.GlareCoverage = 0.4
	if v := Evaluate(c); v.Action != ActionRetake {
		t.Fatalf("glare must RETAKE: %+v", v)
	}
	// Missing corners.
	c = goodCheque()
	c.Image.CornersMissing = true
	if v := Evaluate(c); v.Action != ActionRetake {
		t.Fatalf("missing corners must RETAKE: %+v", v)
	}
}

func TestAmountConfidenceGate(t *testing.T) {
	c := goodCheque()
	c.OCR.AmountConfidence = 0.55
	if v := Evaluate(c); v.Action != ActionRetake {
		t.Fatalf("low amount confidence must RETAKE, not clear: %+v", v)
	}
}

func TestAmountMismatchIsManualNotClear(t *testing.T) {
	c := goodCheque()
	c.OCR.AmountMinor = 24000 // image says £240, user typed £250
	v := Evaluate(c)
	if v.Action != ActionManual {
		t.Fatalf("amount mismatch must go to manual review: %+v", v)
	}
	if len(v.FraudReasons) == 0 {
		t.Fatal("mismatch must record its reason")
	}
}

func TestDuplicatePresentmentTriggersRefundHold(t *testing.T) {
	c := goodCheque()
	c.PreviouslySeen = true
	v := Evaluate(c)
	if v.Action != ActionHoldRefund {
		t.Fatalf("duplicate presentment must HOLD_REFUND: %+v", v)
	}
}

func TestNewAccountHighValueEscalates(t *testing.T) {
	c := goodCheque()
	c.AccountAgeDays = 5
	c.OCR.AmountMinor = 60000
	c.DepositAmountMinor = 60000
	v := Evaluate(c)
	if v.Action != ActionManual {
		t.Fatalf("new account + high value must review: %+v", v)
	}
	if v.FraudScore < fraudManualReviewScore {
		t.Fatalf("score must reach manual threshold: %d", v.FraudScore)
	}
}

func TestHappyPathLifecycle(t *testing.T) {
	l := NewLifecycle("chq-1", t0)
	for i, want := range []State{StateValidating, StateSubmitted, StateClearing, StateSettled} {
		if err := l.Advance(t0.Add(time.Duration(i+1)*time.Hour), "step"); err != nil {
			t.Fatal(err)
		}
		if l.State != want {
			t.Fatalf("state %s, want %s", l.State, want)
		}
	}
	if err := l.Advance(t0, "again"); err == nil {
		t.Fatal("cannot advance a settled cheque")
	}
}

func TestReturnedExceptionWorkflow(t *testing.T) {
	l := NewLifecycle("chq-1", t0)
	_ = l.Advance(t0, "")
	_ = l.Advance(t0, "")
	steps, err := l.RaiseException(ExceptReturned, t0, "REFER_TO_DRAWER")
	if err != nil {
		t.Fatal(err)
	}
	if l.State != StateReturned {
		t.Fatalf("state %s, want RETURNED", l.State)
	}
	if len(steps) == 0 {
		t.Fatal("automated workflow steps required")
	}
	// The reversal settles: lifecycle closes with funds withdrawn.
	if err := l.ResolveAfterException(t0, true); err != nil {
		t.Fatal(err)
	}
	if l.State != StateKilled {
		t.Fatalf("after reversal settle, state %s, want KILLED", l.State)
	}
	if len(l.Timeline()) != 5 {
		t.Fatalf("timeline should have 5 events, has %d", len(l.Timeline()))
	}
}

func TestMismatchResumeAndDuplicateKill(t *testing.T) {
	// Mismatch resolved → resumes clearing.
	l := NewLifecycle("chq-2", t0)
	if _, err := l.RaiseException(ExceptMismatch, t0, "payee disagreement"); err != nil {
		t.Fatal(err)
	}
	if err := l.ResolveAfterException(t0, true); err != nil {
		t.Fatal(err)
	}
	if l.State != StateClearing {
		t.Fatalf("resolved mismatch must resume clearing, got %s", l.State)
	}
	if err := l.Advance(t0, "settle"); err != nil {
		t.Fatal(err)
	}
	if l.State != StateSettled {
		t.Fatalf("resumed cheque must reach SETTLED, got %s", l.State)
	}

	// Duplicate → killed.
	l2 := NewLifecycle("chq-3", t0)
	if _, err := l2.RaiseException(ExceptDuplicate, t0, "same STAN+sort code+account"); err != nil {
		t.Fatal(err)
	}
	if err := l2.ResolveAfterException(t0, true); err != nil {
		t.Fatal(err)
	}
	if l2.State != StateKilled {
		t.Fatalf("duplicate must KILL, got %s", l2.State)
	}
}

func TestDamagedWorkflow(t *testing.T) {
	l := NewLifecycle("chq-4", t0)
	if _, err := l.RaiseException(ExceptDamaged, t0, "torn corner detected"); err != nil {
		t.Fatal(err)
	}
	if l.State != StateDamaged {
		t.Fatalf("state %s, want DAMAGED", l.State)
	}
	if err := l.ResolveAfterException(t0, true); err != nil {
		t.Fatal(err)
	}
	if l.State != StateKilled {
		t.Fatalf("damaged closes as KILLED, got %s", l.State)
	}
}

func TestExceptionOnSettledRejected(t *testing.T) {
	l := NewLifecycle("chq-5", t0)
	for i := 0; i < 4; i++ {
		_ = l.Advance(t0, "")
	}
	if _, err := l.RaiseException(ExceptReturned, t0, "late return"); err == nil {
		t.Fatal("cannot return an already-settled cheque in this model")
	}
}
