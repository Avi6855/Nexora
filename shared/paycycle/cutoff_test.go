package paycycle

import (
	"strings"
	"testing"
	"time"
)

func london(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("Europe/London tzdata unavailable: %v", err)
	}
	return loc
}

func TestQuoteETAValidation(t *testing.T) {
	c := NewCalendar(time.UTC)
	if _, err := c.QuoteETA("NOPE", 100, time.Now().UTC()); err == nil {
		t.Fatal("unknown rail must fail")
	}
	if _, err := c.QuoteETA(RailBACS, 0, time.Now().UTC()); err == nil {
		t.Fatal("non-positive amount must fail")
	}
	if _, err := c.QuoteETA(RailBACS, -5, time.Now().UTC()); err == nil {
		t.Fatal("negative amount must fail")
	}
}

func TestQuoteETAFasterPaymentsAlwaysPrompt(t *testing.T) {
	c := NewCalendar(time.UTC)
	// Sunday afternoon: instant rails never go late.
	now := time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)
	q, err := c.QuoteETA(RailFasterPayments, 5000, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Late || q.LateReason != "" {
		t.Fatalf("faster payments must not be late, got %+v", q)
	}
	if !q.ETA.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("faster payments ETA must be +15m, got %s", q.ETA)
	}
	if q.ArrivalDay != "2026-09-06" {
		t.Fatalf("arrival day %s, want 2026-09-06", q.ArrivalDay)
	}
}

func TestQuoteETABACSCutoff(t *testing.T) {
	loc := london(t)
	c := NewCalendar(loc)
	monday := time.Date(2026, 9, 7, 10, 0, 0, 0, loc) // Monday 10:00 BST

	before, err := c.QuoteETA(RailBACS, 10000, monday)
	if err != nil {
		t.Fatal(err)
	}
	if before.Late {
		t.Fatalf("before cut-off must not be late: %+v", before)
	}
	// Value date Monday +2 working days = Wednesday.
	if before.ArrivalDay != "2026-09-09" {
		t.Fatalf("BACS arrival %s, want 2026-09-09", before.ArrivalDay)
	}

	after, err := c.QuoteETA(RailBACS, 10000, time.Date(2026, 9, 7, 18, 30, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if !after.Late || !strings.Contains(after.LateReason, "cut-off") {
		t.Fatalf("after cut-off must be late with cut-off reason: %+v", after)
	}
	if after.ArrivalDay != "2026-09-10" {
		t.Fatalf("late BACS arrival %s, want 2026-09-10", after.ArrivalDay)
	}
}

func TestQuoteETAWeekendAndHoliday(t *testing.T) {
	loc := london(t)
	c := NewCalendar(loc)

	// CHAPS on Saturday rolls to Monday.
	sat := time.Date(2026, 9, 5, 10, 0, 0, 0, loc)
	q, err := c.QuoteETA(RailCHAPS, 250000, sat)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Late || !strings.Contains(q.LateReason, "Saturday") {
		t.Fatalf("Saturday CHAPS must cite Saturday: %+v", q)
	}
	if q.ArrivalDay != "2026-09-07" {
		t.Fatalf("Saturday CHAPS arrival %s, want Monday 2026-09-07", q.ArrivalDay)
	}

	// CARD_SETTLEMENT on Sunday hits the canonical example reason.
	sun := time.Date(2026, 9, 6, 12, 0, 0, 0, loc)
	card, err := c.QuoteETA(RailCardSettlement, 1999, sun)
	if err != nil {
		t.Fatal(err)
	}
	if !card.Late {
		t.Fatal("Sunday card settlement must be late")
	}
	if !strings.Contains(card.LateReason, "Sunday") || !strings.Contains(card.LateReason, "next working day") {
		t.Fatalf("Sunday reason must name Sunday + next working day, got %q", card.LateReason)
	}
	if card.ArrivalDay != "2026-09-07" {
		t.Fatalf("Sunday card arrival %s, want Monday 2026-09-07", card.ArrivalDay)
	}

	// Overridable bank holiday: Monday becomes non-working for CHAPS.
	c.SetHolidays([]string{"2026-09-07"})
	hol, err := c.QuoteETA(RailCHAPS, 250000, time.Date(2026, 9, 7, 10, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if !hol.Late || !strings.Contains(hol.LateReason, "bank holiday") {
		t.Fatalf("holiday CHAPS must cite bank holiday: %+v", hol)
	}
	if hol.ArrivalDay != "2026-09-08" {
		t.Fatalf("holiday CHAPS arrival %s, want Tuesday 2026-09-08", hol.ArrivalDay)
	}
	// Faster payments ignore the holiday.
	fp, err := c.QuoteETA(RailFasterPayments, 100, time.Date(2026, 9, 7, 10, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if fp.Late {
		t.Fatalf("faster payments must ignore bank holidays: %+v", fp)
	}
	// Malformed holiday entries are ignored, never fatal.
	c.SetHolidays([]string{"not-a-date", "2026-13-45"})
	if c.IsHoliday(time.Date(2026, 9, 7, 10, 0, 0, 0, loc)) {
		t.Fatal("malformed holidays must not mark days")
	}
}

func TestQuoteETASWIFTAndTimezone(t *testing.T) {
	loc := london(t)
	c := NewCalendar(loc)
	// Monday 10:00 BST -> value Monday, SWIFT +1 working day = Tuesday noon.
	q, err := c.QuoteETA(RailSWIFT, 50000, time.Date(2026, 9, 7, 10, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if q.ArrivalDay != "2026-09-08" {
		t.Fatalf("SWIFT arrival %s, want 2026-09-08", q.ArrivalDay)
	}
	if q.ETA.Hour() != 12 {
		t.Fatalf("SWIFT ETA hour %d, want 12", q.ETA.Hour())
	}
	// Timezone-aware: 16:30 BST Monday is after the 16:00 SWIFT cut-off even
	// though it is still before 16:30 UTC thinking.
	late, err := c.QuoteETA(RailSWIFT, 50000, time.Date(2026, 9, 7, 16, 30, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if !late.Late {
		t.Fatalf("16:30 BST must be after the 16:00 SWIFT cut-off: %+v", late)
	}
}
