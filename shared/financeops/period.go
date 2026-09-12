package financeops

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Lock marks a period closed. Later corrections for a locked period are
// auto-redirected to the next open period and never mutate the locked one.
func (s *Store) Lock(period string) error {
	if period == "" {
		return fmt.Errorf("period is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.periods[period] = true
	s.logger.Info().Str("period", period).Msg("financeops period locked")
	return nil
}

// IsLocked reports whether a period is locked.
func (s *Store) IsLocked(period string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.periods[period]
}

// Correction is a compensating posting redirected to an open period.
type Correction struct {
	ID              string    `json:"id"`
	RequestedPeriod string    `json:"requested_period"`
	ActualPeriod    string    `json:"actual_period"`
	Redirected      bool      `json:"redirected"`
	DebitAccount    string    `json:"debit_account"`
	CreditAccount   string    `json:"credit_account"`
	Amount          int64     `json:"amount"`
	CreatedAt       time.Time `json:"created_at"`
}

// PostCorrection posts a correction for requestedPeriod. When the requested
// period is locked, the entry is auto-redirected to the next open period.
func (s *Store) PostCorrection(requestedPeriod, debitAccount, creditAccount string, amount int64) (*Correction, error) {
	if requestedPeriod == "" {
		return nil, fmt.Errorf("period is required")
	}
	if debitAccount == "" || creditAccount == "" {
		return nil, fmt.Errorf("debit_account and credit_account are required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	actual := requestedPeriod
	redirected := false
	if s.periods[requestedPeriod] {
		actual = s.nextOpenLocked(requestedPeriod)
		redirected = true
	}
	if _, ok := s.periods[actual]; !ok {
		s.periods[actual] = false
	}
	c := &Correction{
		ID:              uuid.NewString(),
		RequestedPeriod: requestedPeriod,
		ActualPeriod:    actual,
		Redirected:      redirected,
		DebitAccount:    debitAccount,
		CreditAccount:   creditAccount,
		Amount:          amount,
		CreatedAt:       time.Now().UTC(),
	}
	s.logger.Info().Str("requested", requestedPeriod).Str("actual", actual).Bool("redirected", redirected).Msg("financeops correction posted")
	// Also record the correction as a ledger entry in the actual period so the
	// locked period is never mutated.
	e := &Entry{
		ID:            c.ID,
		DebitAccount:  debitAccount,
		CreditAccount: creditAccount,
		Amount:        amount,
		Period:        actual,
		Reference:     "correction-for:" + requestedPeriod,
		CreatedAt:     c.CreatedAt,
	}
	s.entries[e.ID] = e
	return c, nil
}

// openPeriodForLocked returns period when open, else the next open period.
// Callers must hold s.mu.
func (s *Store) openPeriodForLocked(period string) string {
	if !s.periods[period] {
		return period
	}
	return s.nextOpenLocked(period)
}

// nextOpenLocked finds the smallest known open period after start, or
// generates successor periods until an open one is found. Callers hold s.mu.
func (s *Store) nextOpenLocked(start string) string {
	candidates := make([]string, 0, len(s.periods))
	for p, locked := range s.periods {
		if p > start && !locked {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) > 0 {
		sort.Strings(candidates)
		return candidates[0]
	}
	next := nextPeriod(start)
	for {
		if locked, known := s.periods[next]; !known || !locked {
			return next
		}
		next = nextPeriod(next)
	}
}

// nextPeriod increments a YYYY-MM period, falling back to a suffixed child
// for free-form period names.
func nextPeriod(p string) string {
	parts := strings.Split(p, "-")
	if len(parts) == 2 {
		if year, err1 := strconv.Atoi(parts[0]); err1 == nil {
			if month, err2 := strconv.Atoi(parts[1]); err2 == nil && month >= 1 && month <= 12 {
				month++
				if month > 12 {
					month = 1
					year++
				}
				return fmt.Sprintf("%04d-%02d", year, month)
			}
		}
	}
	return p + "-next"
}
