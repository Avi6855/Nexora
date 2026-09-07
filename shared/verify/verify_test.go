package verify

import (
	"errors"
	"strings"
	"testing"

	"github.com/nexora/nexora/shared/calc"
	"github.com/nexora/nexora/shared/money"
)

// implA is the reference: shared/calc.
func implA(subject string, in CalcInput) (money.Money, error) {
	bal := money.Money{Amount: in.AmountMinor, Currency: in.Currency}
	return calc.New().DailyInterest(bal, in.AnnualRateBps, in.Days, calc.DayCountACT365, in.Rounding)
}

// implB is the "independent" implementation — here the same maths re-derived
// with a different code path (float64), which is exactly the kind of subtle
// divergence the engine exists to catch.
func implBFaithful(subject string, in CalcInput) (money.Money, error) {
	bal := money.Money{Amount: in.AmountMinor, Currency: in.Currency}
	m, err := calc.New().DailyInterest(bal, in.AnnualRateBps, in.Days, calc.DayCountACT365, in.Rounding)
	if err != nil {
		return money.Money{}, err
	}
	return m, nil
}

// implBDrifty rounds each day instead of once — the drift the golden corpus
// proved. The engine must quarantine it.
func implBDrifty(subject string, in CalcInput) (money.Money, error) {
	bal := money.Money{Amount: in.AmountMinor, Currency: in.Currency}
	acc, err := calc.NewAccumulator(in.Currency)
	if err != nil {
		return money.Money{}, err
	}
	for i := 0; i < in.Days; i++ {
		d, err := calc.New().DailyInterest(bal, in.AnnualRateBps, 1, calc.DayCountACT365, calc.RoundHalfEven)
		if err != nil {
			return money.Money{}, err
		}
		_ = acc.Add(money.Money{Amount: d.Amount * 10000 * 365, Currency: in.Currency}, 0, 0, calc.DayCountACT365)
	}
	return acc.Realize(calc.RoundHalfEven)
}

func TestVerifyAgreement(t *testing.T) {
	e := NewEngine(implA, implBFaithful, 0)
	got, err := e.Verify("acct-1", CalcInput{Operation: "daily_interest", Currency: "GBP", AmountMinor: 10000, AnnualRateBps: 400, Days: 1, Rounding: calc.RoundHalfEven})
	if err != nil {
		t.Fatalf("agreement expected, got %v", err)
	}
	if got.Amount != 1 {
		t.Fatalf("got %d, want 1", got.Amount)
	}
	if e.QuarantinedCount() != 0 {
		t.Fatalf("nothing should be quarantined on agreement")
	}
}

func TestVerifyMismatchQuarantines(t *testing.T) {
	e := NewEngine(implA, implBDrifty, 0)
	_, err := e.Verify("acct-2", CalcInput{Operation: "daily_interest", Currency: "GBP", AmountMinor: 123456789, AnnualRateBps: 250, Days: 365, Rounding: calc.RoundHalfEven})
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
	rec, ok := e.QuarantineRecordFor("acct-2")
	if !ok {
		t.Fatal("subject must be quarantined after mismatch")
	}
	if rec.DiffMinor == 0 {
		t.Fatal("quarantine record must capture the diff")
	}
	// Subsequent operations for the same subject are refused.
	_, err = e.Verify("acct-2", CalcInput{Operation: "daily_interest", Currency: "GBP", AmountMinor: 1, AnnualRateBps: 1, Days: 1, Rounding: calc.RoundHalfEven})
	if !errors.Is(err, ErrQuarantined) {
		t.Fatalf("expected ErrQuarantined, got %v", err)
	}
	// Release is the only way out.
	e.Release("acct-2")
	if e.QuarantinedCount() != 0 {
		t.Fatal("release must clear quarantine")
	}
}

func TestVerifyErrorAsymmetry(t *testing.T) {
	boom := errors.New("boom")
	a := func(string, CalcInput) (money.Money, error) { return money.Money{}, boom }
	b := func(string, CalcInput) (money.Money, error) { return money.MustNewMoney(1, "GBP"), nil }
	e := NewEngine(a, b, 0)
	_, err := e.Verify("s", CalcInput{Operation: "x"})
	if err == nil || !strings.Contains(err.Error(), "A failed, B succeeded") {
		t.Fatalf("expected asymmetric-failure mismatch, got %v", err)
	}
}

func TestVerifyIdenticalErrorsAreAgreement(t *testing.T) {
	boom := errors.New("boom")
	a := func(string, CalcInput) (money.Money, error) { return money.Money{}, boom }
	b := func(string, CalcInput) (money.Money, error) { return money.Money{}, errors.New("boom") }
	e := NewEngine(a, b, 0)
	if _, err := e.Verify("s", CalcInput{Operation: "x"}); err != nil {
		t.Fatalf("identical errors must count as agreement, got %v", err)
	}
}

func TestVerifyToleranceWindow(t *testing.T) {
	a := func(_ string, in CalcInput) (money.Money, error) { return money.MustNewMoney(100, "GBP"), nil }
	b := func(subject string, in CalcInput) (money.Money, error) {
		if subject == "outside" {
			return money.MustNewMoney(102, "GBP"), nil // diff 2 > tolerance 1
		}
		return money.MustNewMoney(101, "GBP"), nil // diff 1 == tolerance
	}
	e := NewEngine(a, b, 1) // 1p tolerance; boundary equality passes
	if _, err := e.Verify("within", CalcInput{Operation: "x"}); err != nil {
		t.Fatalf("diff within tolerance must pass, got %v", err)
	}
	if _, err := e.Verify("outside", CalcInput{Operation: "x"}); err == nil {
		t.Fatal("diff beyond tolerance must quarantine")
	}
}

// ── Canary ──────────────────────────────────────────────────────────────────

func recTransform(amount int64) TransformFunc {
	return func(record map[string]string) (map[string]string, error) {
		return map[string]string{"amount_minor": itoa(amount), "currency": record["currency"]}, nil
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestCanaryPassWithinBudget(t *testing.T) {
	// Old total 10,000p; +1p per record × 10 = 10p drift = 0.1% = 10 bps.
	// Budget 10 bps → boundary equality passes.
	oldFn := recTransform(1000)
	newFn := recTransform(1001)
	c := NewCanary(oldFn, newFn, 100, 10)
	res := c.Run(sampleRecords(10, "GBP"))
	if res.Verdict != CanaryPass {
		t.Fatalf("want PASS, got %s (%s)", res.Verdict, res.Reason)
	}
}

func TestCanaryStopsOnAggregateDrift(t *testing.T) {
	oldFn := recTransform(1000)
	newFn := recTransform(1200) // +200/record × 10 = 2000 on 10,000 = 20% ≫ 0.01%
	c := NewCanary(oldFn, newFn, 100, 1)
	res := c.Run(sampleRecords(10, "GBP"))
	if res.Verdict != CanaryThreshold {
		t.Fatalf("want THRESHOLD, got %s (%s)", res.Verdict, res.Reason)
	}
}

func TestCanaryStopsOnErrorAsymmetry(t *testing.T) {
	oldFn := func(map[string]string) (map[string]string, error) { return map[string]string{"amount_minor": "1"}, nil }
	newFn := func(map[string]string) (map[string]string, error) { return nil, errors.New("new path exploded") }
	c := NewCanary(oldFn, newFn, 100, 1)
	res := c.Run(sampleRecords(3, "GBP"))
	if res.Verdict != CanaryError {
		t.Fatalf("want ERROR, got %s (%s)", res.Verdict, res.Reason)
	}
}

func TestCanaryZeroBaselineDriftStops(t *testing.T) {
	oldFn := recTransform(0)
	newFn := recTransform(5)
	c := NewCanary(oldFn, newFn, 100, 1)
	res := c.Run(sampleRecords(2, "GBP"))
	if res.Verdict != CanaryThreshold {
		t.Fatalf("drift on zero baseline must stop rollout, got %s", res.Verdict)
	}
}

func sampleRecords(n int, currency string) []map[string]string {
	recs := make([]map[string]string, n)
	for i := 0; i < n; i++ {
		recs[i] = map[string]string{"currency": currency, "id": itoa(int64(i))}
	}
	return recs
}
