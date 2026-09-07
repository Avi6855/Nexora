// Rate schedule support: effective-dated interest rates.
//
// A rate change (3.25% → 3.50%) must NEVER rewrite history: accrual for
// August uses August's rate even after October's change lands. Rates are
// therefore append-only entries with [effective_from, effective_until)
// windows — the same time-interval discipline as policy versions in
// policy-service, applied to pricing.
package calc

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/nexora/nexora/shared/money"
)

var (
	ErrRateGap       = errors.New("no rate in force for the requested instant")
	ErrRateOverlap   = errors.New("rate windows overlap")
	ErrRateNotFound  = errors.New("rate schedule entry not found")
	ErrRateImmutable = errors.New("historical rate entries are immutable")
)

// RateEntry is one effective-dated rate. Basis points: 325 = 3.25%.
type RateEntry struct {
	ProductID      string    `json:"product_id"`
	RateBps        int64     `json:"rate_bps"`
	EffectiveFrom  time.Time `json:"effective_from"`
	EffectiveUntil time.Time `json:"effective_until"` // zero = open-ended
	EntryVersion   int       `json:"entry_version"`
}

// Covers reports whether this rate governed at instant t.
func (r RateEntry) Covers(t time.Time) bool {
	if t.Before(r.EffectiveFrom) {
		return false
	}
	return r.EffectiveUntil.IsZero() || t.Before(r.EffectiveUntil)
}

// RateSchedule is the versioned rate history for one product.
type RateSchedule struct {
	ProductID string
	Entries   []RateEntry
}

// Add appends a new rate. Overlap with an existing open-ended entry is
// rejected with a helpful message — the caller must close the previous
// window first (CloseOpenEntry), exactly like policy version windows.
func (s *RateSchedule) Add(entry RateEntry) error {
	if entry.RateBps < 0 {
		return fmt.Errorf("rate_bps must be non-negative, got %d", entry.RateBps)
	}
	if entry.ProductID == "" {
		entry.ProductID = s.ProductID
	}
	if entry.ProductID != s.ProductID {
		return fmt.Errorf("entry product %s does not match schedule %s", entry.ProductID, s.ProductID)
	}
	if entry.EffectiveFrom.IsZero() {
		return fmt.Errorf("effective_from is required")
	}
	// Overlap check against every entry that could span the new window.
	for _, e := range s.Entries {
		overlaps := false
		if entry.EffectiveUntil.IsZero() {
			overlaps = e.EffectiveUntil.IsZero() || e.EffectiveUntil.After(entry.EffectiveFrom)
		} else {
			overlaps = e.EffectiveUntil.IsZero() || e.EffectiveUntil.After(entry.EffectiveFrom) &&
				entry.EffectiveUntil.After(e.EffectiveFrom)
		}
		if overlaps && e.Covers(entry.EffectiveFrom) || overlaps && entry.Covers(e.EffectiveFrom) {
			return fmt.Errorf("%w: %s@v%d [%s, %s) overlaps new [%s, %s)",
				ErrRateOverlap, e.ProductID, e.EntryVersion,
				e.EffectiveFrom.Format(time.DateOnly), orOpen(e.EffectiveUntil),
				entry.EffectiveFrom.Format(time.DateOnly), orOpen(entry.EffectiveUntil))
		}
	}
	s.Entries = append(s.Entries, entry)
	sort.Slice(s.Entries, func(i, j int) bool {
		return s.Entries[i].EffectiveFrom.Before(s.Entries[j].EffectiveFrom)
	})
	return nil
}

func orOpen(t time.Time) string {
	if t.IsZero() {
		return "∞"
	}
	return t.Format(time.DateOnly)
}

// RateAt returns the rate in force at instant t.
func (s *RateSchedule) RateAt(t time.Time) (int64, error) {
	for _, e := range s.Entries {
		if e.Covers(t) {
			return e.RateBps, nil
		}
	}
	return 0, fmt.Errorf("%w: product %s at %s", ErrRateGap, s.ProductID, t.Format(time.RFC3339))
}

// CloseOpenEntry closes the currently open-ended window at t (used when a
// new rate takes over). Returns the closed entry's version, or an error if
// nothing was open.
func (s *RateSchedule) CloseOpenEntry(t time.Time) (int, error) {
	for i := len(s.Entries) - 1; i >= 0; i-- {
		if s.Entries[i].EffectiveUntil.IsZero() {
			if !t.After(s.Entries[i].EffectiveFrom) {
				return 0, fmt.Errorf("close instant %s is not after the entry's start", t.Format(time.DateOnly))
			}
			s.Entries[i].EffectiveUntil = t
			return s.Entries[i].EntryVersion, nil
		}
	}
	return 0, ErrRateNotFound
}

// Accrue computes interest over [from, to) walking the rate windows —
// split across rate changes, each segment accrued EXACTLY, rounded once at
// the end. This is the customer-correct behaviour: a mid-month rate change
// accrues at both rates for the days each was in force.
func (s *RateSchedule) Accrue(balance money.Money, from, to time.Time, dc DayCount, r Rounding) (money.Money, []RateSegment, error) {
	if !to.After(from) {
		return money.Money{}, nil, fmt.Errorf("accrual window [%s, %s) is empty or reversed",
			from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	denom, err := dc.denominator()
	if err != nil {
		return money.Money{}, nil, err
	}
	numerTotal := new(big.Int)
	var segments []RateSegment
	cursor := from
	for cursor.Before(to) {
		rateBps, err := s.RateAt(cursor)
		if err != nil {
			return money.Money{}, nil, err
		}
		// Segment ends at min(to, next rate boundary, current entry's end).
		// Clamping to the CURRENT entry's EffectiveUntil is essential: without
		// it an accrual window that outlives a closed rate window silently
		// over-runs at the old rate instead of failing with ErrRateGap.
		segEnd := to
		for _, e := range s.Entries {
			if e.EffectiveFrom.After(cursor) && e.EffectiveFrom.Before(segEnd) {
				segEnd = e.EffectiveFrom
			}
			if e.Covers(cursor) && !e.EffectiveUntil.IsZero() && e.EffectiveUntil.Before(segEnd) {
				segEnd = e.EffectiveUntil
			}
		}
		days64 := int64(segEnd.Sub(cursor).Hours() / 24)
		if days64 <= 0 {
			days64 = 1 // sub-day remainder accrues as one day
		}
		// Exact accumulation: amount*rate*days summed across segments over the
		// common denominator; single rounding at realization.
		numerTotal.Add(numerTotal, mulInto(balance.Amount, rateBps, days64))
		segments = append(segments, RateSegment{
			From: cursor, To: segEnd, RateBps: rateBps, Days: int(days64),
		})
		cursor = segEnd
	}
	total, err := divRounded(numerTotal, mulInto(10000, denom), r)
	if err != nil {
		return money.Money{}, nil, err
	}
	return money.Money{Amount: total, Currency: balance.Currency}, segments, nil
}

// RateSegment records one rate-window slice of an accrual (audit trail).
type RateSegment struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	RateBps int64     `json:"rate_bps"`
	Days    int       `json:"days"`
}

type bigIntAlias = big.Int
