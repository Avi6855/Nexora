package calc

import (
	"errors"
	"testing"
	"time"

	"github.com/nexora/nexora/shared/money"
)

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func TestRateAtWindows(t *testing.T) {
	s := &RateSchedule{ProductID: "savings-flex"}
	if err := s.Add(RateEntry{RateBps: 325, EffectiveFrom: d(2026, 1, 1), EntryVersion: 1}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.CloseOpenEntry(d(2026, 10, 1)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := s.Add(RateEntry{RateBps: 350, EffectiveFrom: d(2026, 10, 1), EntryVersion: 2}); err != nil {
		t.Fatalf("add v2: %v", err)
	}

	got, err := s.RateAt(d(2026, 9, 30))
	if err != nil || got != 325 {
		t.Fatalf("Sep rate = %d, %v; want 325", got, err)
	}
	got, err = s.RateAt(d(2026, 10, 1))
	if err != nil || got != 350 {
		t.Fatalf("Oct rate = %d, %v; want 350 (window is [from, to))", got, err)
	}
	if _, err := s.RateAt(d(2025, 12, 31)); !errors.Is(err, ErrRateGap) {
		t.Fatalf("pre-history must be a gap error, got %v", err)
	}
}

func TestRateOverlapRejected(t *testing.T) {
	s := &RateSchedule{ProductID: "p"}
	if err := s.Add(RateEntry{RateBps: 325, EffectiveFrom: d(2026, 1, 1), EntryVersion: 1}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Adding another open-ended entry without closing the first must fail.
	err := s.Add(RateEntry{RateBps: 350, EffectiveFrom: d(2026, 6, 1), EntryVersion: 2})
	if !errors.Is(err, ErrRateOverlap) {
		t.Fatalf("want ErrRateOverlap, got %v", err)
	}
}

func TestAccrueAcrossRateChange(t *testing.T) {
	s := &RateSchedule{ProductID: "savings-flex"}
	_ = s.Add(RateEntry{RateBps: 325, EffectiveFrom: d(2026, 1, 1), EntryVersion: 1})
	_, _ = s.CloseOpenEntry(d(2026, 10, 1))
	_ = s.Add(RateEntry{RateBps: 350, EffectiveFrom: d(2026, 10, 1), EntryVersion: 2})

	bal := money.MustNewMoney(100_000, "GBP") // £1,000.00
	// 10 days at 3.25% (Sep 21 → Oct 1) + 11 days at 3.50% (Oct 1 → Oct 12).
	total, segs, err := s.Accrue(bal, d(2026, 9, 21), d(2026, 10, 12), DayCountACT365, RoundHalfEven)
	if err != nil {
		t.Fatalf("accrue: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(segs), segs)
	}
	if segs[0].RateBps != 325 || segs[1].RateBps != 350 {
		t.Fatalf("segments must carry their own rates: %+v", segs)
	}

	// Exact expectation: 100000*325*10/(10000*365) + 100000*350*11/(10000*365)
	// = 890.41 + 1054.79 → exact sum 1945.2... → 195 (hundredths; see below).
	wantNumer := mulInto(100_000, 325, 10)
	wantNumer.Add(wantNumer, mulInto(100_000, 350, 11))
	want, _ := divRounded(wantNumer, mulInto(10000, 365), RoundHalfEven)
	if total.Amount != want {
		t.Fatalf("total = %d, want %d", total.Amount, want)
	}
	if want != 195 {
		t.Fatalf("golden expectation drifted: computed %d, corpus says 195", want)
	}
}

func TestAccrueEmptyWindowFails(t *testing.T) {
	s := &RateSchedule{ProductID: "p"}
	_ = s.Add(RateEntry{RateBps: 100, EffectiveFrom: d(2026, 1, 1), EntryVersion: 1})
	if _, _, err := s.Accrue(money.MustNewMoney(100, "GBP"), d(2026, 5, 1), d(2026, 5, 1), DayCountACT365, RoundHalfEven); err == nil {
		t.Fatal("empty window must fail")
	}
	if _, _, err := s.Accrue(money.MustNewMoney(100, "GBP"), d(2026, 5, 2), d(2026, 5, 1), DayCountACT365, RoundHalfEven); err == nil {
		t.Fatal("reversed window must fail")
	}
}

func TestAccrueFailsOnRateGap(t *testing.T) {
	s := &RateSchedule{ProductID: "p"}
	_ = s.Add(RateEntry{RateBps: 100, EffectiveFrom: d(2026, 1, 1), EntryVersion: 1})
	_, _ = s.CloseOpenEntry(d(2026, 2, 1))
	// Feb 1 → Feb 10 has no rate in force.
	if _, _, err := s.Accrue(money.MustNewMoney(100, "GBP"), d(2026, 1, 28), d(2026, 2, 10), DayCountACT365, RoundHalfEven); !errors.Is(err, ErrRateGap) {
		t.Fatalf("want ErrRateGap, got %v", err)
	}
}

func TestRateAtIsHistoricallyStable(t *testing.T) {
	// Simulate the exact regulator question: what rate applied on 30 Sep,
	// BEFORE and AFTER the October change was booked? Answer must not move.
	s1 := &RateSchedule{ProductID: "p"}
	_ = s1.Add(RateEntry{RateBps: 325, EffectiveFrom: d(2026, 1, 1), EntryVersion: 1})
	before, _ := s1.RateAt(d(2026, 9, 30))

	_, _ = s1.CloseOpenEntry(d(2026, 10, 1))
	_ = s1.Add(RateEntry{RateBps: 350, EffectiveFrom: d(2026, 10, 1), EntryVersion: 2})
	after, _ := s1.RateAt(d(2026, 9, 30))

	if before != after {
		t.Fatalf("historical rate changed after a future rate change: %d → %d", before, after)
	}
}
