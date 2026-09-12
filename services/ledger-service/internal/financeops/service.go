// Package financeops wires shared/financeops into ledger-service as a live
// HTTP surface under /v1/finance-ops.
package financeops

import (
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/financeops"
)

// Service is the ledger-service finance-ops store backed by
// shared/financeops.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds the service. The logger is used for mutation logs.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(logger), logger: logger}
}

// CreateEntry posts an original balanced entry.
func (s *Service) CreateEntry(debitAccount, creditAccount string, amount int64, period string) (*shared.Entry, error) {
	e, err := s.store.CreateEntry(debitAccount, creditAccount, amount, period)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("entry_id", e.ID).Msg("financeops entry posted")
	return e, nil
}

// GetEntry returns one entry.
func (s *Service) GetEntry(id string) (*shared.Entry, error) {
	return s.store.GetEntry(id)
}

// RequestAdjustment opens a PENDING adjustment.
func (s *Service) RequestAdjustment(originalID, reason string) (*shared.Adjustment, error) {
	a, err := s.store.RequestAdjustment(originalID, reason)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("adjustment_id", a.ID).Msg("financeops adjustment requested")
	return a, nil
}

// ApproveAdjustment posts the compensating entry and approves.
func (s *Service) ApproveAdjustment(id string) (*shared.Adjustment, *shared.Entry, error) {
	a, e, err := s.store.ApproveAdjustment(id)
	if err != nil {
		return nil, nil, err
	}
	s.logger.Info().Str("adjustment_id", id).Msg("financeops adjustment approved")
	return a, e, nil
}

// GetAdjustment returns one adjustment.
func (s *Service) GetAdjustment(id string) (*shared.Adjustment, error) {
	return s.store.GetAdjustment(id)
}

// VerifyBalanced verifies the compensating pair.
func (s *Service) VerifyBalanced(id string) (bool, string, error) {
	return s.store.VerifyBalanced(id)
}

// PostBackdatedEvent records an event with a past effective date.
func (s *Service) PostBackdatedEvent(account string, amount int64, effectiveAt time.Time) (*shared.BackdatedEvent, error) {
	ev, err := s.store.PostBackdatedEvent(account, amount, effectiveAt)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("event_id", ev.ID).Msg("financeops backdated event posted")
	return ev, nil
}

// BalanceAsOf recomputes the derived balance as of a date.
func (s *Service) BalanceAsOf(account string, asOf time.Time) int64 {
	return s.store.BalanceAsOf(account, asOf)
}

// BackdatedEvents lists all backdated events.
func (s *Service) BackdatedEvents() []shared.BackdatedEvent {
	return s.store.BackdatedEvents()
}

// CheckClose evaluates the close checklist.
func (s *Service) CheckClose(c shared.Checklist) shared.CloseResult {
	return s.store.CheckClose(c)
}

// Lock closes a period.
func (s *Service) Lock(period string) error {
	if err := s.store.Lock(period); err != nil {
		return err
	}
	s.logger.Info().Str("period", period).Msg("financeops period locked")
	return nil
}

// IsLocked reports the lock flag.
func (s *Service) IsLocked(period string) bool {
	return s.store.IsLocked(period)
}

// PostCorrection posts a correction, redirecting out of locked periods.
func (s *Service) PostCorrection(requestedPeriod, debitAccount, creditAccount string, amount int64) (*shared.Correction, error) {
	c, err := s.store.PostCorrection(requestedPeriod, debitAccount, creditAccount, amount)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("requested", requestedPeriod).Str("actual", c.ActualPeriod).Msg("financeops correction posted")
	return c, nil
}

// PutRule stores a new posting-rule version.
func (s *Service) PutRule(txType, debitAccount, creditAccount string) (*shared.PostingRule, error) {
	r, err := s.store.PutRule(txType, debitAccount, creditAccount)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("type", txType).Msg("financeops posting rule stored")
	return r, nil
}

// Rollback drops the latest rule version.
func (s *Service) Rollback(txType string) (*shared.PostingRule, error) {
	r, err := s.store.Rollback(txType)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("type", txType).Msg("financeops posting rule rolled back")
	return r, nil
}

// Resolve returns a rule version (0 = latest).
func (s *Service) Resolve(txType string, version int) (*shared.PostingRule, error) {
	return s.store.Resolve(txType, version)
}

// PostSubledger appends to an isolated domain ledger.
func (s *Service) PostSubledger(domain, debitAccount, creditAccount string, amount int64) (*shared.SubledgerEntry, error) {
	e, err := s.store.PostSubledger(domain, debitAccount, creditAccount, amount)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("domain", domain).Msg("financeops subledger posted")
	return e, nil
}

// TrialBalance returns one domain trial.
func (s *Service) TrialBalance(domain string) (shared.TrialBalance, error) {
	return s.store.TrialBalance(domain)
}

// Consolidated folds every domain into the general view.
func (s *Service) Consolidated() shared.ConsolidatedView {
	return s.store.Consolidated()
}
