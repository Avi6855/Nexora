// Package multicurrency wires the shared multi-currency ledger into
// ledger-service as a live HTTP surface.
package multicurrency

import (
	"time"

	"github.com/rs/zerolog"

	sharedmc "github.com/nexora/nexora/shared/multicurrency"
)

// Service is the multi-currency live state: one shared ledger.
type Service struct {
	ledger *sharedmc.Ledger
	logger zerolog.Logger
}

// NewService returns an empty multi-currency service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{ledger: sharedmc.NewLedger(), logger: logger}
}

// Credit adds funds in one currency.
func (s *Service) Credit(account, currency string, amount int64) error {
	if err := s.ledger.Credit(account, currency, amount); err != nil {
		s.logger.Warn().Err(err).Str("account", account).Str("currency", currency).Msg("multi-currency credit failed")
		return err
	}
	s.logger.Info().Str("account", account).Str("currency", currency).Int64("amount", amount).Msg("multi-currency credit")
	return nil
}

// Debit removes funds in one currency.
func (s *Service) Debit(account, currency string, amount int64) error {
	if err := s.ledger.Debit(account, currency, amount); err != nil {
		s.logger.Warn().Err(err).Str("account", account).Str("currency", currency).Msg("multi-currency debit failed")
		return err
	}
	s.logger.Info().Str("account", account).Str("currency", currency).Int64("amount", amount).Msg("multi-currency debit")
	return nil
}

// Balances returns all currency balances for an account.
func (s *Service) Balances(account string) map[string]int64 {
	return s.ledger.Balances(account)
}

// SnapshotRates captures an FX rate snapshot.
func (s *Service) SnapshotRates(rates map[string]float64, at time.Time) (string, error) {
	id, err := s.ledger.SnapshotRates(rates, at)
	if err != nil {
		s.logger.Warn().Err(err).Msg("fx snapshot failed")
		return "", err
	}
	s.logger.Info().Str("snapshot_id", id).Msg("fx snapshot captured")
	return id, nil
}

// Convert moves funds across currencies with spread + audit.
func (s *Service) Convert(account, from, to string, amount int64, snapshotID string, spreadBps int64) (int64, error) {
	converted, err := s.ledger.Convert(account, from, to, amount, snapshotID, spreadBps, time.Now().UTC())
	if err != nil {
		s.logger.Warn().Err(err).Str("account", account).Str("snapshot_id", snapshotID).Msg("multi-currency conversion failed")
		return 0, err
	}
	s.logger.Info().Str("account", account).Str("from", from).Str("to", to).Int64("converted", converted).Msg("multi-currency conversion")
	return converted, nil
}

// ValuateTotal aggregates into the base currency pinned to a snapshot.
func (s *Service) ValuateTotal(account, base, snapshotID string) (int64, error) {
	total, err := s.ledger.ValuateTotal(account, base, snapshotID)
	if err != nil {
		s.logger.Warn().Err(err).Str("account", account).Str("snapshot_id", snapshotID).Msg("valuation failed")
		return 0, err
	}
	return total, nil
}

// Audit returns conversion audit entries.
func (s *Service) Audit(account string) []sharedmc.AuditEntry {
	return s.ledger.Audit(account)
}
