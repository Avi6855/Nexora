// Package paycycle is Nexora's reusable payment-cycle platform: rail cut-off
// and holiday intelligence, the beneficiary trust lifecycle, and payment
// approval chains.
//
// It is intentionally execution-free: services call QuoteETA before promising
// an arrival time, drive beneficiaries through VERIFIED/TRUSTED states before
// releasing first payments, and evaluate approval rules before authorising.
// All state here is deterministic and time-parameterised so tests and support
// tooling can replay any decision bit-for-bit.
package paycycle

import (
	"fmt"
	"time"
)

// Rail is a settlement rail with its own calendar and cut-off rules.
type Rail string

const (
	RailFasterPayments Rail = "FASTER_PAYMENTS"
	RailBACS           Rail = "BACS"
	RailCHAPS          Rail = "CHAPS"
	RailSWIFT          Rail = "SWIFT"
	RailCardSettlement Rail = "CARD_SETTLEMENT"
)

// valid reports whether r is a known rail.
func (r Rail) valid() bool {
	switch r {
	case RailFasterPayments, RailBACS, RailCHAPS, RailSWIFT, RailCardSettlement:
		return true
	default:
		return false
	}
}

// cutoffFor returns the daily submission cut-off (hour, minute) in the
// calendar's location. hasCutoff is false for rails that never close
// (FASTER_PAYMENTS settles around the clock).
func cutoffFor(r Rail) (hour, minute int, hasCutoff bool) {
	switch r {
	case RailBACS:
		return 17, 0, true
	case RailCHAPS:
		return 17, 20, true
	case RailSWIFT:
		return 16, 0, true
	case RailCardSettlement:
		return 23, 0, true
	default:
		return 0, 0, false
	}
}

// ETAQuote is the arrival promise for one payment instruction.
type ETAQuote struct {
	Rail        Rail      `json:"rail"`
	RequestedAt time.Time `json:"requested_at"`
	ETA         time.Time `json:"eta"`
	ArrivalDay  string    `json:"arrival_day"` // YYYY-MM-DD in the calendar location
	Late        bool      `json:"late"`
	LateReason  string    `json:"late_reason,omitempty"`
	SameDay     bool      `json:"same_day"`
}

// Calendar holds the timezone and overridable bank-holiday dates shared by
// every rail. Holidays are keyed by YYYY-MM-DD as seen in Location.
type Calendar struct {
	location *time.Location
	holidays map[string]bool
}

// NewCalendar creates a calendar in loc (nil means UTC).
func NewCalendar(loc *time.Location) *Calendar {
	if loc == nil {
		loc = time.UTC
	}
	return &Calendar{location: loc, holidays: map[string]bool{}}
}

// Location returns the calendar's timezone.
func (c *Calendar) Location() *time.Location {
	if c == nil || c.location == nil {
		return time.UTC
	}
	return c.location
}

// SetHolidays replaces the bank-holiday set. Dates must be YYYY-MM-DD;
// malformed entries are ignored so a bad config line can never break every
// quote.
func (c *Calendar) SetHolidays(dates []string) {
	c.holidays = map[string]bool{}
	for _, d := range dates {
		if _, err := time.Parse("2006-01-02", d); err == nil {
			c.holidays[d] = true
		}
	}
}

// AddHoliday marks a single calendar day as a bank holiday.
func (c *Calendar) AddHoliday(t time.Time) {
	c.holidays[t.In(c.Location()).Format("2006-01-02")] = true
}

// IsHoliday reports whether t falls on a configured bank holiday.
func (c *Calendar) IsHoliday(t time.Time) bool {
	return c.holidays[t.In(c.Location()).Format("2006-01-02")]
}

// IsWorkingDay reports whether rail settles on t's calendar day.
// FASTER_PAYMENTS runs 24/7 including bank holidays; BACS/CHAPS/SWIFT close
// weekends and bank holidays; CARD_SETTLEMENT closes Sundays and bank
// holidays.
func (c *Calendar) IsWorkingDay(rail Rail, t time.Time) bool {
	local := t.In(c.Location())
	if c.IsHoliday(t) && rail != RailFasterPayments {
		return false
	}
	switch rail {
	case RailFasterPayments:
		return true
	case RailCardSettlement:
		return local.Weekday() != time.Sunday
	default:
		return local.Weekday() != time.Saturday && local.Weekday() != time.Sunday
	}
}

// NextWorkingDay returns midnight on the first working day strictly after
// t's calendar day.
func (c *Calendar) NextWorkingDay(rail Rail, t time.Time) time.Time {
	d := startOfDay(t.In(c.Location())).AddDate(0, 0, 1)
	for i := 0; i < 366; i++ {
		if c.IsWorkingDay(rail, d) {
			return d
		}
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// startOfDay truncates to local midnight.
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// dateAt returns hh:mm on day's calendar date.
func dateAt(day time.Time, hour, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, day.Location())
}

// addWorkingDays advances n working days from a working-day midnight. n == 0
// returns day unchanged.
func (c *Calendar) addWorkingDays(rail Rail, day time.Time, n int) time.Time {
	d := day
	for i := 0; i < n; i++ {
		d = c.NextWorkingDay(rail, d)
	}
	return d
}

// QuoteETA promises an arrival time for amountMinor on rail as of now.
// amountMinor is validated today and reserved for future tiered scheduling
// (e.g. high-value CHAPS routing); the calendar behaviour is amount-agnostic.
//
// Settlement lags (in rail working days from the value date): FASTER_PAYMENTS
// settles ~15 minutes after submission; CHAPS and CARD_SETTLEMENT settle the
// value date itself; SWIFT settles value date +1; BACS settles value date +2.
// Submissions after cut-off or on a closed day roll to the next working day
// and set Late with a human-readable LateReason.
func (c *Calendar) QuoteETA(rail Rail, amountMinor int64, now time.Time) (ETAQuote, error) {
	if !rail.valid() {
		return ETAQuote{}, fmt.Errorf("unknown rail %q", string(rail))
	}
	if amountMinor <= 0 {
		return ETAQuote{}, fmt.Errorf("amount_minor must be positive, got %d", amountMinor)
	}
	loc := c.Location()
	local := now.In(loc)

	working := c.IsWorkingDay(rail, now)
	afterCutoff := false
	cutHour, cutMin, hasCutoff := cutoffFor(rail)
	if hasCutoff && working {
		if local.Hour()*60+local.Minute() > cutHour*60+cutMin {
			afterCutoff = true
		}
	}

	var base time.Time // value-date midnight (always a working day)
	late := false
	reason := ""
	switch {
	case !working:
		late = true
		if c.IsHoliday(now) {
			reason = "bank holiday; arrives next working day"
		} else {
			reason = fmt.Sprintf("receiving rail unavailable %s; arrives next working day", local.Weekday())
		}
		base = c.NextWorkingDay(rail, now)
	case afterCutoff:
		late = true
		reason = fmt.Sprintf("after %s cut-off %02d:%02d; arrives next working day", string(rail), cutHour, cutMin)
		base = c.NextWorkingDay(rail, now)
	default:
		base = startOfDay(local)
	}

	var eta, arrival time.Time
	sameDay := false
	today := local.Format("2006-01-02")
	switch rail {
	case RailFasterPayments:
		eta = now.Add(15 * time.Minute)
		arrival = eta.In(loc)
		sameDay = arrival.Format("2006-01-02") == today
	case RailCHAPS:
		arrival = dateAt(base, 18, 0)
		eta = arrival
		sameDay = !late
	case RailCardSettlement:
		arrival = dateAt(base, 22, 0)
		eta = arrival
		sameDay = !late
	case RailSWIFT:
		arrival = dateAt(c.addWorkingDays(rail, base, 1), 12, 0)
		eta = arrival
		sameDay = false
	default: // RailBACS
		arrival = dateAt(c.addWorkingDays(rail, base, 2), 7, 0)
		eta = arrival
		sameDay = false
	}

	return ETAQuote{
		Rail:        rail,
		RequestedAt: now,
		ETA:         eta,
		ArrivalDay:  arrival.Format("2006-01-02"),
		Late:        late,
		LateReason:  reason,
		SameDay:     sameDay,
	}, nil
}
