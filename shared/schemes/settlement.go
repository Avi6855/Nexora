package schemes

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// SchemeWindow configures settlement for one scheme.
type SchemeWindow struct {
	Scheme    string   `json:"scheme"`
	CycleDays int      `json:"cycle_days"`         // T+0 / T+1 / T+2
	Cutoff    string   `json:"cutoff"`             // "15:00" UTC
	Holidays  []string `json:"holidays,omitempty"` // YYYY-MM-DD
}

// SettlementCalendar holds per-scheme windows.
type SettlementCalendar struct {
	mu       sync.RWMutex
	schemes  map[string]*SchemeWindow
	holidays map[string]map[string]bool // scheme -> date -> true
	logger   zerolog.Logger
}

// NewSettlementCalendar returns an empty calendar.
func NewSettlementCalendar(logger zerolog.Logger) *SettlementCalendar {
	return &SettlementCalendar{
		schemes:  make(map[string]*SchemeWindow),
		holidays: make(map[string]map[string]bool),
		logger:   logger,
	}
}

// AddScheme registers a scheme window. Cutoff must be "HH:MM",
// CycleDays 0..2.
func (c *SettlementCalendar) AddScheme(w SchemeWindow) (*SchemeWindow, error) {
	if strings.TrimSpace(w.Scheme) == "" {
		return nil, fmt.Errorf("%w: scheme is required", ErrSchemeInvalidInput)
	}
	if w.CycleDays < 0 || w.CycleDays > 2 {
		return nil, fmt.Errorf("%w: cycle_days must be 0..2", ErrSchemeInvalidInput)
	}
	if _, err := time.Parse("15:04", w.Cutoff); err != nil {
		return nil, fmt.Errorf("%w: cutoff must be HH:MM", ErrSchemeInvalidInput)
	}
	for _, h := range w.Holidays {
		if _, err := time.Parse(time.DateOnly, h); err != nil {
			return nil, fmt.Errorf("%w: bad holiday %q", ErrSchemeInvalidInput, h)
		}
	}
	cp := w
	cp.Holidays = append([]string(nil), w.Holidays...)
	key := strings.ToUpper(strings.TrimSpace(w.Scheme))
	c.mu.Lock()
	c.schemes[key] = &cp
	set := make(map[string]bool, len(w.Holidays))
	for _, h := range w.Holidays {
		set[h] = true
	}
	c.holidays[key] = set
	c.mu.Unlock()
	c.logger.Info().Str("scheme", key).Int("cycle_days", w.CycleDays).Str("cutoff", w.Cutoff).Msg("settlement scheme added")
	out := cp
	return &out, nil
}

func (c *SettlementCalendar) getScheme(scheme string) (*SchemeWindow, map[string]bool, error) {
	key := strings.ToUpper(strings.TrimSpace(scheme))
	c.mu.RLock()
	defer c.mu.RUnlock()
	w, ok := c.schemes[key]
	if !ok {
		return nil, nil, ErrSchemeNotFound
	}
	cp := *w
	return &cp, c.holidays[key], nil
}

// IsBusinessDay reports whether d (date part, UTC) settles.
func (c *SettlementCalendar) IsBusinessDay(scheme string, d time.Time) (bool, error) {
	_, hol, err := c.getScheme(scheme)
	if err != nil {
		return false, err
	}
	return isBusinessDay(d, hol), nil
}

func isBusinessDay(d time.Time, hol map[string]bool) bool {
	wd := d.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	if hol[d.Format(time.DateOnly)] {
		return false
	}
	return true
}

func nextBusinessDay(d time.Time, hol map[string]bool) time.Time {
	n := d.AddDate(0, 0, 1)
	for !isBusinessDay(n, hol) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

func addBusinessDays(d time.Time, n int, hol map[string]bool) time.Time {
	cur := d
	for i := 0; i < n; i++ {
		cur = nextBusinessDay(cur, hol)
	}
	return cur
}

// NextSettlement returns the next settlement datetime (UTC, at cutoff)
// for a given timestamp. Before-cutoff on a business day settles
// base+cycle; after-cutoff or non-business days roll to the next
// business day first, then add the cycle.
func (c *SettlementCalendar) NextSettlement(scheme string, ts time.Time) (time.Time, error) {
	w, hol, err := c.getScheme(scheme)
	if err != nil {
		return time.Time{}, err
	}
	ts = ts.UTC()
	cut, _ := time.Parse("15:04", w.Cutoff)
	cutoffToday := time.Date(ts.Year(), ts.Month(), ts.Day(), cut.Hour(), cut.Minute(), 0, 0, time.UTC)
	var base time.Time
	switch {
	case !isBusinessDay(ts, hol):
		base = ts
		for !isBusinessDay(base, hol) {
			base = base.AddDate(0, 0, 1)
		}
	case ts.After(cutoffToday):
		base = nextBusinessDay(ts, hol)
	default:
		base = time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.UTC)
	}
	settleDate := addBusinessDays(base, w.CycleDays, hol)
	settle := time.Date(settleDate.Year(), settleDate.Month(), settleDate.Day(), cut.Hour(), cut.Minute(), 0, 0, time.UTC)
	return settle, nil
}
