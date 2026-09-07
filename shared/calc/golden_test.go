// Package calc — golden corpus.
//
// The golden corpus is the financial-correctness contract: every candidate
// change to the calculation library must replay every case with zero
// unexpected differences before it can be considered for deployment. Cases
// deliberately cover the nasty edges: DST days (23h/25h), leap years,
// month-ends, penny rounding at every boundary, and negative adjustments.
package calc

import (
	"math/big"
	"testing"
	"time"

	"github.com/nexora/nexora/shared/money"
)

// goldenCase is one recorded expectation. Expectations were produced by the
// reference implementation and reviewed by hand; a change that alters any of
// them must ship as a new Version and justify the difference.
type goldenCase struct {
	name string
	got  money.Money
	err  error
	want money.Money
}

func mustMoney(t *testing.T, amount int64, currency string) money.Money {
	t.Helper()
	return money.MustNewMoney(amount, currency)
}

// ── Daily interest ──────────────────────────────────────────────────────────

func TestGoldenDailyInterest(t *testing.T) {
	type caseDef struct {
		goldenCase
		rate int64
		days int
		dc   DayCount
	}
	cases := []caseDef{
		// Plain accrual: £100.00 at 4.00% for 1 day = 10000*400*1/(10000*365) = 1.095… → 1
		{goldenCase{name: "penny-precise one day", got: mustMoney(t, 10000, "GBP"), want: mustMoney(t, 1, "GBP")}, 400, 1, DayCountACT365},
		{goldenCase{name: "zero balance", got: mustMoney(t, 0, "GBP"), want: mustMoney(t, 0, "GBP")}, 400, 1, DayCountACT365},
		{goldenCase{name: "one penny for a year", got: mustMoney(t, 1, "GBP"), want: mustMoney(t, 0, "GBP")}, 400, 1, DayCountACT365},
		{goldenCase{name: "large balance no overflow", got: mustMoney(t, 1_000_000_000_00, "GBP"), want: mustMoney(t, 10958904, "GBP")}, 400, 1, DayCountACT365},
		{goldenCase{name: "JPY zero-decimal currency", got: mustMoney(t, 1000000, "JPY"), want: mustMoney(t, 110, "JPY")}, 400, 1, DayCountACT365},
		{goldenCase{name: "ACT/360 convention", got: mustMoney(t, 36500, "GBP"), want: mustMoney(t, 4, "GBP")}, 400, 1, DayCountACT360},
		{goldenCase{name: "negative balance accrues negative", got: mustMoney(t, -10000, "GBP"), want: mustMoney(t, -1, "GBP")}, 400, 1, DayCountACT365},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := New().DailyInterest(c.got, c.rate, c.days, c.dc, RoundHalfEven)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Amount != c.want.Amount || got.Currency != c.want.Currency {
				t.Fatalf("got %s, want %s", got.String(), c.want.String())
			}
		})
	}
}

// The accrual identity: a year of DAILY accruals, summed EXACTLY and rounded
// once, must equal exactly rate*balance/100. Rounding each day instead drifts
// (up to half a penny per day) — the corpus records that drift as forbidden.
func TestGoldenDailyInterestYearSum(t *testing.T) {
	balance := money.MustNewMoney(123456789, "GBP") // £1,234,567.89
	rate := int64(250)                              // 2.50%

	acc, err := NewAccumulator("GBP")
	if err != nil {
		t.Fatalf("accumulator: %v", err)
	}
	for i := 0; i < 365; i++ {
		if err := acc.Add(balance, rate, 1, DayCountACT365); err != nil {
			t.Fatalf("day %d: %v", i, err)
		}
	}
	realized, err := acc.Realize(RoundHalfEven)
	if err != nil {
		t.Fatalf("realize: %v", err)
	}
	want := int64(123456789 * 250) // exact numerator over 10000
	// Exact half-even: 3086419.725 → 3086420 (not truncation's 3086419).
	wantRounded, err := divRounded(big.NewInt(want), big.NewInt(10000), RoundHalfEven)
	if err != nil {
		t.Fatalf("golden expectation: %v", err)
	}
	if realized.Amount != wantRounded {
		t.Fatalf("exact accrual realized %d, want %d", realized.Amount, wantRounded)
	}

	// Document the forbidden alternative: rounding every day drifts.
	drifted := int64(0)
	for i := 0; i < 365; i++ {
		m, _ := New().DailyInterest(balance, rate, 1, DayCountACT365, RoundHalfEven)
		drifted += m.Amount
	}
	if drifted == want {
		t.Fatalf("per-day rounding now matches exact accrual; the drift demo is stale")
	}
}

// ── Monthly interest: leap years & month ends ───────────────────────────────

func TestGoldenMonthlyInterestCalendarEdges(t *testing.T) {
	cases := []struct {
		name   string
		year   int
		month  time.Month
		wantDs int
	}{
		{"leap-year February", 2024, time.February, 29},
		{"non-leap February", 2025, time.February, 28},
		{"century non-leap", 1900, time.February, 28},
		{"400-year leap", 2000, time.February, 29},
		{"month end 31-day", 2025, time.January, 31},
		{"month end 30-day", 2025, time.April, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DaysInMonth(tc.year, tc.month, time.UTC); got != tc.wantDs {
				t.Fatalf("DaysInMonth(%d, %s) = %d, want %d", tc.year, tc.month, got, tc.wantDs)
			}
			m, err := New().MonthlyInterest(money.MustNewMoney(100000, "GBP"), 400, tc.year, tc.month, time.UTC, RoundHalfEven)
			if err != nil {
				t.Fatalf("monthly interest: %v", err)
			}
			// 100.00 GBP at 4% for N days, rounded once at realization.
			want, err := divRounded(big.NewInt(int64(100000*400*tc.wantDs)), big.NewInt(10000*365), RoundHalfEven)
			if err != nil {
				t.Fatalf("golden expectation: %v", err)
			}
			if m.Amount != want {
				t.Fatalf("monthly interest = %d, want %d", m.Amount, want)
			}
		})
	}
}

// ── DST days ────────────────────────────────────────────────────────────────

func TestGoldenDSTDayLengths(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	// 29 March 2026: clocks spring forward 01:00→02:00 → 23-hour day.
	// 25 October 2026: clocks fall back 02:00→01:00 → 25-hour day.
	if got := HoursInDay(time.Date(2026, time.March, 29, 12, 0, 0, 0, london), london); got != 23 {
		t.Fatalf("UK spring-forward day = %d hours, want 23", got)
	}
	if got := HoursInDay(time.Date(2026, time.October, 25, 12, 0, 0, 0, london), london); got != 25 {
		t.Fatalf("UK fall-back day = %d hours, want 25", got)
	}
	if got := HoursInDay(time.Date(2026, time.March, 30, 12, 0, 0, 0, london), london); got != 24 {
		t.Fatalf("normal day = %d hours, want 24", got)
	}
}

// ── Fees ────────────────────────────────────────────────────────────────────

func TestGoldenFees(t *testing.T) {
	schedule := FeeSchedule{
		Currency: "GBP",
		Tiers: []FeeTier{
			{UpToMinorUnits: 1000, RateBps: 100},        // ≤£10: 1%
			{UpToMinorUnits: 10000, FlatMinorUnits: 50}, // ≤£100: flat £0.50
			{UpToMinorUnits: 0, RateBps: 25},            // open: 0.25%
		},
	}
	cases := []goldenCase{
		{name: "tier1 rate", got: mustMoney(t, 999, "GBP"), want: mustMoney(t, 10, "GBP")}, // 999*100/10000 = 9.99 → 10
		{name: "tier1 boundary", got: mustMoney(t, 1000, "GBP"), want: mustMoney(t, 10, "GBP")},
		{name: "tier2 flat", got: mustMoney(t, 5000, "GBP"), want: mustMoney(t, 50, "GBP")},
		{name: "tier3 rate", got: mustMoney(t, 100000, "GBP"), want: mustMoney(t, 250, "GBP")},
		{name: "negative amount still charged", got: mustMoney(t, -200000, "GBP"), want: mustMoney(t, 500, "GBP")},
	}
	runGolden(t, "Fee", cases, func(c goldenCase) (money.Money, error) {
		return New().Fee(c.got, schedule, RoundHalfEven)
	})
}

func TestFeeTierExceeded(t *testing.T) {
	s := FeeSchedule{Currency: "GBP", Tiers: []FeeTier{{UpToMinorUnits: 100, RateBps: 100}}}
	_, err := New().Fee(money.MustNewMoney(101, "GBP"), s, RoundHalfEven)
	if err == nil {
		t.Fatal("expected error when amount exceeds all tiers")
	}
}

// ── Proration ───────────────────────────────────────────────────────────────

func TestGoldenProrate(t *testing.T) {
	type caseDef struct {
		goldenCase
		used int
	}
	cases := []caseDef{
		{goldenCase{name: "full period", got: mustMoney(t, 12000, "GBP"), want: mustMoney(t, 12000, "GBP")}, 12},
		{goldenCase{name: "half month", got: mustMoney(t, 12000, "GBP"), want: mustMoney(t, 6000, "GBP")}, 6},
		{goldenCase{name: "one third rounds down-even", got: mustMoney(t, 100, "GBP"), want: mustMoney(t, 33, "GBP")}, 4},
		{goldenCase{name: "zero used", got: mustMoney(t, 12000, "GBP"), want: mustMoney(t, 0, "GBP")}, 0},
		{goldenCase{name: "negative proration", got: mustMoney(t, -1000, "GBP"), want: mustMoney(t, -500, "GBP")}, 6},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := New().Prorate(c.got, c.used, 12, RoundHalfEven)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Amount != c.want.Amount {
				t.Fatalf("got %d, want %d", got.Amount, c.want.Amount)
			}
		})
	}
}

func TestProrateInvalidRange(t *testing.T) {
	if _, err := New().Prorate(money.MustNewMoney(100, "GBP"), 13, 12, RoundHalfEven); err == nil {
		t.Fatal("expected error for used > total")
	}
	if _, err := New().Prorate(money.MustNewMoney(100, "GBP"), 1, 0, RoundHalfEven); err == nil {
		t.Fatal("expected error for total == 0")
	}
}

// ── Rounding policies at exact-half boundaries ──────────────────────────────

func TestGoldenRoundingPolicies(t *testing.T) {
	// 0.005 → HALF_UP gives 1, HALF_EVEN gives 0, CEIL gives 1, FLOOR gives 0.
	type rp struct {
		p    Rounding
		want int64
	}
	for _, tc := range []rp{
		{RoundHalfUp, 1},
		{RoundHalfEven, 0},
		{RoundFloor, 0},
		{RoundCeil, 1},
	} {
		got, err := divRounded(big.NewInt(1), big.NewInt(2), tc.p) // exact half
		if err != nil {
			t.Fatalf("%s: %v", tc.p, err)
		}
		if got != tc.want {
			t.Fatalf("rounding %s of 0.5 = %d, want %d", tc.p, got, tc.want)
		}
	}
	// Negative half: HALF_UP rounds away from zero → -1.
	got, err := divRounded(big.NewInt(-1), big.NewInt(2), RoundHalfUp)
	if err != nil || got != -1 {
		t.Fatalf("HALF_UP of -0.5 = %d, %v; want -1", got, err)
	}
}

// ── Overflow guards ─────────────────────────────────────────────────────────

func TestGoldenOverflowGuard(t *testing.T) {
	// Max-int balance at a huge rate must fail loudly, never truncate.
	if _, err := New().DailyInterest(money.Money{Amount: 1 << 62, Currency: "GBP"}, 1<<40, 365, DayCountACT365, RoundHalfEven); err == nil {
		t.Fatal("expected ErrCalcOverflow for astronomic accrual")
	}
}

// runGolden replays expectations and renders a failure diff the reviewer can
// paste straight into a golden-corpus update PR.
func runGolden(t *testing.T, group string, cases []goldenCase, run func(goldenCase) (money.Money, error)) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := run(c)
			if c.err != nil {
				if err == nil {
					t.Fatalf("expected error %v, got %v", c.err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Amount != c.want.Amount || got.Currency != c.want.Currency {
				t.Fatalf("%s.%s: got %s, want %s",
					group, c.name, got.String(), c.want.String())
			}
		})
	}
}
