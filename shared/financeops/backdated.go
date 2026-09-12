package financeops

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BackdatedEvent carries the effective/recorded/processed triple. EffectiveAt
// is when the money moved economically; RecordedAt is when the event was
// captured; ProcessedAt is when the ledger applied it.
type BackdatedEvent struct {
	ID          string    `json:"id"`
	Account     string    `json:"account"`
	Amount      int64     `json:"amount"`
	EffectiveAt time.Time `json:"effective_at"`
	RecordedAt  time.Time `json:"recorded_at"`
	ProcessedAt time.Time `json:"processed_at"`
}

// PostBackdatedEvent records an event with a (possibly past) effective date.
// Recorded and processed default to now; the triple is preserved verbatim.
func (s *Store) PostBackdatedEvent(account string, amount int64, effectiveAt time.Time) (*BackdatedEvent, error) {
	return s.PostBackdatedEventAt(account, amount, effectiveAt, time.Now().UTC())
}

// PostBackdatedEventAt is PostBackdatedEvent with an explicit clock.
func (s *Store) PostBackdatedEventAt(account string, amount int64, effectiveAt, now time.Time) (*BackdatedEvent, error) {
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	if amount == 0 {
		return nil, fmt.Errorf("amount must be non-zero")
	}
	if effectiveAt.IsZero() {
		return nil, fmt.Errorf("effective_at is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := &BackdatedEvent{
		ID:          uuid.NewString(),
		Account:     account,
		Amount:      amount,
		EffectiveAt: effectiveAt,
		RecordedAt:  now,
		ProcessedAt: now,
	}
	s.backdated = append(s.backdated, ev)
	s.logger.Info().Str("event_id", ev.ID).Str("account", account).Msg("financeops backdated event posted")
	cp := *ev
	return &cp, nil
}

// BalanceAsOf recomputes the derived balance for account as of the given
// date, summing events with EffectiveAt <= asOf (economic time, not arrival).
func (s *Store) BalanceAsOf(account string, asOf time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, ev := range s.backdated {
		if ev.Account == account && !ev.EffectiveAt.After(asOf) {
			total += ev.Amount
		}
	}
	return total
}

// BackdatedEvents returns a copy of all backdated events in posting order.
func (s *Store) BackdatedEvents() []BackdatedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BackdatedEvent, 0, len(s.backdated))
	for _, ev := range s.backdated {
		out = append(out, *ev)
	}
	return out
}
