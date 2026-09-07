// Package calc is Nexora's deterministic financial calculation library.
//
// Every service that needs to answer "how much interest is owed?" must get
// byte-for-byte the same answer — support tooling, ledger, reporting and the
// customer app included. This package is the single source of that arithmetic:
//
//   - All amounts are int64 minor units (pence, cents, yen) — never floats.
//   - Intermediate products that could overflow int64 are computed with
//     math/big and rejected with ErrCalcOverflow rather than silently
//     truncating.
//   - Rounding is explicit at every division point; every calculator declares
//     a version so historical decisions can be pinned to exact semantics.
//   - Day-count conventions, month-end and DST-day behaviour are defined here,
//     not reimplemented per service.
package calc

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/nexora/nexora/shared/money"
)

// ErrCalcOverflow is returned when a product would not fit in int64 minor
// units. Callers must treat it as a hard failure — never fall back to a
// truncated value.
var ErrCalcOverflow = errors.New("calculation overflows int64 minor units")

// Version pins a calculator's exact semantics. Auditors must be able to ask
// "which version computed this figure?" and re-run it bit-for-bit.
type Version string

const (
	// V1 is the reference implementation: interest accrues on ACT day counts
	// with half-even (banker's) rounding applied once, at the final step.
	V1 Version = "calc-v1"
)

// Rounding selects how a quotient is turned back into minor units.
type Rounding string

const (
	RoundHalfUp   Rounding = "HALF_UP"   // 0.5 → away from zero
	RoundHalfEven Rounding = "HALF_EVEN" // banker's rounding (V1 default)
	RoundFloor    Rounding = "FLOOR"     // toward zero (credit-favouring)
	RoundCeil     Rounding = "CEIL"      // away from zero (debit-favouring)
)

// DayCount is the accrual denominator convention.
type DayCount string

const (
	DayCountACT365 DayCount = "ACT/365F" // fixed 365 (leap days ignored)
	DayCountACT360 DayCount = "ACT/360"  // euro-market convention
)

func (d DayCount) denominator() (int64, error) {
	switch d {
	case DayCountACT365:
		return 365, nil
	case DayCountACT360:
		return 360, nil
	default:
		return 0, fmt.Errorf("unsupported day-count convention %q", d)
	}
}

// Validate rejects unknown rounding policies early — a typo in a policy name
// must never silently change into a different rounding behaviour.
func (r Rounding) Validate() error {
	switch r {
	case RoundHalfUp, RoundHalfEven, RoundFloor, RoundCeil:
		return nil
	default:
		return fmt.Errorf("unsupported rounding policy %q", r)
	}
}

// divRounded computes numer/denom with the requested policy. Works for
// negative numerators too: HALF_UP rounds away from zero, FLOOR/CEIL are
// relative to zero so credit-side rounding always favours the customer.
func divRounded(numer, denom *big.Int, r Rounding) (int64, error) {
	if denom.Sign() == 0 {
		return 0, errors.New("division by zero in calculation")
	}
	q, rem := new(big.Int).QuoRem(numer, denom, new(big.Int))
	if rem.Sign() == 0 {
		return fitInt64(q)
	}
	remAbs := new(big.Int).Abs(rem)
	denomAbs := new(big.Int).Abs(denom)
	cmp := remAbs.Cmp(new(big.Int).Rsh(denomAbs, 1)) // 2*rem vs denom
	// QuoRem truncates toward zero, so the remainder carries the numerator's
	// sign. Rounding "up" therefore means AWAY FROM ZERO: the adjustment
	// direction follows the numerator, never the denominator.
	away := int64(numer.Sign())
	switch r {
	case RoundHalfEven:
		if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
			q = new(big.Int).Add(q, big.NewInt(away))
		}
	case RoundHalfUp:
		if cmp >= 0 {
			q = new(big.Int).Add(q, big.NewInt(away))
		}
	case RoundFloor:
		// toward zero — no adjustment
	case RoundCeil:
		q = new(big.Int).Add(q, big.NewInt(away))
	default:
		return 0, fmt.Errorf("unsupported rounding policy %q", r)
	}
	return fitInt64(q)
}

func sign(i *big.Int) int64 {
	if i.Sign() < 0 {
		return -1
	}
	return 1
}

var _ = sign // retained for future exact-comparison helpers

// fitInt64 converts a big.Int into int64 or fails loudly.
func fitInt64(v *big.Int) (int64, error) {
	if !v.IsInt64() {
		return 0, ErrCalcOverflow
	}
	return v.Int64(), nil
}

// mulInto builds numerators without int64 overflow: amount * rateBps * days
// as a big.Int chain.
func mulInto(factors ...int64) *big.Int {
	acc := big.NewInt(1)
	for _, f := range factors {
		acc.Mul(acc, big.NewInt(f))
	}
	return acc
}

// Calculator is the contract the verification engine (shared/verify) compares
// across implementations. Any change to financial maths ships as a new
// Version, is shadow-verified against the reference, and is only promoted
// after the golden corpus passes with an unexpected-difference budget of zero.
type Calculator interface {
	Version() Version

	// DailyInterest accrues balance * rateBps/10000 * days/denominator.
	// Display-grade: rounded per call. Ledger posting must use Accumulator.
	DailyInterest(balance money.Money, annualRateBps int64, days int, dc DayCount, r Rounding) (money.Money, error)
	// MonthlyInterest accrues over a calendar month (leap years handled by
	// the calendar, never by hand-written tables).
	MonthlyInterest(balance money.Money, annualRateBps int64, year int, month time.Month, loc *time.Location, r Rounding) (money.Money, error)
	// Fee applies a tiered fee schedule to an amount.
	Fee(amount money.Money, s FeeSchedule, r Rounding) (money.Money, error)
	// Prorate splits an amount over used/total periods (partial-month fees,
	// refund clawbacks).
	Prorate(amount money.Money, used, total int, r Rounding) (money.Money, error)
}

// Accumulator accrues interest EXACTLY (arbitrary precision) and rounds only
// once, at Realize. This is how the ledger must post interest: rounding every
// daily accrual drifts up to half a penny per day (21p/year on a five-figure
// balance — the golden corpus proves it), which silently breaks the invariant
// that a year of daily interest equals rate*balance/100.
type Accumulator struct {
	currency string
	numer    *big.Int // exact accrued interest, scaled by day-count denominator
	denom    int64
}

// NewAccumulator starts an exact accrual run for one currency.
func NewAccumulator(currency string) (*Accumulator, error) {
	if err := money.Zero(currency).Validate(); err != nil {
		return nil, err
	}
	return &Accumulator{currency: currency, numer: new(big.Int), denom: 1}, nil
}

// Accumulator.Add — exact common-denominator handling, no placeholders.
func (a *Accumulator) Add(balance money.Money, annualRateBps int64, days int, dc DayCount) error {
	if err := balance.Validate(); err != nil {
		return err
	}
	if balance.Currency != a.currency {
		return fmt.Errorf("%w: accumulator %s vs balance %s", money.ErrDifferentCurrency, a.currency, balance.Currency)
	}
	if days < 0 {
		return fmt.Errorf("negative day count %d", days)
	}
	denom, err := dc.denominator()
	if err != nil {
		return err
	}
	// numer/denom holds the exact accrual so far. To add a term expressed
	// over a different denominator, scale both exactly: n/d + x/D = (n*D + x*d)/(d*D).
	if a.denom == 1 {
		a.denom = denom
	} else if a.denom != denom {
		newNumer := new(big.Int).Mul(a.numer, big.NewInt(denom))
		newNumer.Add(newNumer, new(big.Int).Mul(mulInto(balance.Amount, annualRateBps, int64(days)), big.NewInt(a.denom)))
		a.numer = newNumer
		a.denom = a.denom * denom
		return nil
	}
	a.numer.Add(a.numer, mulInto(balance.Amount, annualRateBps, int64(days)))
	return nil
}

// Realize rounds the exact accumulated interest once and returns it.
func (a *Accumulator) Realize(r Rounding) (money.Money, error) {
	if err := r.Validate(); err != nil {
		return money.Money{}, err
	}
	total, err := divRounded(a.numer, mulInto(10000, a.denom), r)
	if err != nil {
		return money.Money{}, err
	}
	return money.Money{Amount: total, Currency: a.currency}, nil
}

// Exact returns the unrounded accrual numerator/denominator for audit.
func (a *Accumulator) Exact() (numer, denom string) {
	return a.numer.String(), fmt.Sprintf("%d", a.denom)
}

// Library is the reference Calculator (V1).
type Library struct{}

// New returns the reference calculator.
func New() Library { return Library{} }

func (Library) Version() Version { return V1 }

func (Library) DailyInterest(balance money.Money, annualRateBps int64, days int, dc DayCount, r Rounding) (money.Money, error) {
	if err := balance.Validate(); err != nil {
		return money.Money{}, err
	}
	if err := r.Validate(); err != nil {
		return money.Money{}, err
	}
	if days < 0 {
		return money.Money{}, fmt.Errorf("negative day count %d", days)
	}
	denom, err := dc.denominator()
	if err != nil {
		return money.Money{}, err
	}
	numer := mulInto(balance.Amount, annualRateBps, int64(days))
	interest, err := divRounded(numer, mulInto(10000, denom), r)
	if err != nil {
		return money.Money{}, err
	}
	return money.Money{Amount: interest, Currency: balance.Currency}, nil
}

func (l Library) MonthlyInterest(balance money.Money, annualRateBps int64, year int, month time.Month, loc *time.Location, r Rounding) (money.Money, error) {
	if loc == nil {
		loc = time.UTC
	}
	days := DaysInMonth(year, month, loc)
	return l.DailyInterest(balance, annualRateBps, days, DayCountACT365, r)
}

// FeeSchedule is an ordered tier list. The FIRST tier whose UpToMinorUnits is
// zero (open-ended) or >= amount wins; rate is applied as basis points, or a
// flat MinorUnits fee when RateBps is 0.
type FeeSchedule struct {
	Currency string
	Tiers    []FeeTier
}

type FeeTier struct {
	UpToMinorUnits int64 // 0 = open-ended top tier
	RateBps        int64 // basis points; ignored when FlatMinorUnits > 0
	FlatMinorUnits int64 // flat fee in minor units
}

func (Library) Fee(amount money.Money, s FeeSchedule, r Rounding) (money.Money, error) {
	if err := amount.Validate(); err != nil {
		return money.Money{}, err
	}
	if s.Currency != amount.Currency {
		return money.Money{}, fmt.Errorf("%w: schedule %s vs amount %s", money.ErrDifferentCurrency, s.Currency, amount.Currency)
	}
	if len(s.Tiers) == 0 {
		return money.Money{}, errors.New("fee schedule has no tiers")
	}
	var tier *FeeTier
	for i := range s.Tiers {
		t := &s.Tiers[i]
		if t.UpToMinorUnits == 0 || abs64(amount.Amount) <= t.UpToMinorUnits {
			tier = t
			break
		}
	}
	if tier == nil {
		return money.Money{}, fmt.Errorf("amount %d exceeds every fee tier", amount.Amount)
	}
	var fee int64
	if tier.FlatMinorUnits > 0 {
		fee = tier.FlatMinorUnits
	} else {
		var err error
		fee, err = divRounded(mulInto(abs64(amount.Amount), tier.RateBps), big.NewInt(10000), r)
		if err != nil {
			return money.Money{}, err
		}
	}
	// Fees are always non-negative charges regardless of the sign of the
	// underlying movement (a negative adjustment still incurs its fee).
	if fee < 0 {
		fee = -fee
	}
	return money.Money{Amount: fee, Currency: amount.Currency}, nil
}

func (Library) Prorate(amount money.Money, used, total int, r Rounding) (money.Money, error) {
	if err := amount.Validate(); err != nil {
		return money.Money{}, err
	}
	if err := r.Validate(); err != nil {
		return money.Money{}, err
	}
	if total <= 0 {
		return money.Money{}, fmt.Errorf("total periods must be positive, got %d", total)
	}
	if used < 0 || used > total {
		return money.Money{}, fmt.Errorf("used periods %d out of range [0,%d]", used, total)
	}
	part, err := divRounded(mulInto(amount.Amount, int64(used)), big.NewInt(int64(total)), r)
	if err != nil {
		return money.Money{}, err
	}
	return money.Money{Amount: part, Currency: amount.Currency}, nil
}

// DaysInMonth returns calendar days in month/year as seen in loc — leap years
// and month ends come from the calendar, never hand-maintained tables.
func DaysInMonth(year int, month time.Month, loc *time.Location) int {
	if loc == nil {
		loc = time.UTC
	}
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	return first.AddDate(0, 1, -1).Day()
}

// HoursInDay returns 23, 24 or 25 for the local calendar day — the DST-safe
// primitive for second-based accrual and "daily cut-off" schedulers. Financial
// jobs must never assume every day is 24h: on European/London DST transition
// days a 01:30 job may not exist or may run twice.
func HoursInDay(day time.Time, loc *time.Location) int {
	if loc == nil {
		loc = time.UTC
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	return int(end.Sub(start).Hours())
}

// BusinessDaysBetween counts calendar days from a to b exclusive of b,
// expressed in a's location. Kept calendar-pure on purpose: holiday calendars
// belong to the platform calendar service, not to arithmetic.
func BusinessDaysBetween(a, b time.Time, loc *time.Location) int {
	if loc == nil {
		loc = time.UTC
	}
	a = time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, loc)
	b = time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, loc)
	return int(b.Sub(a).Hours() / 24)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
