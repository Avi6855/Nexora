// Package household wires the shared bills + mandates + closure library
// into account-service as a live HTTP surface.
package household

import (
	"github.com/rs/zerolog"

	sharedhh "github.com/nexora/nexora/shared/household"
)

// Service is the household live state: one shared store.
type Service struct {
	store  *sharedhh.Store
	logger zerolog.Logger
}

// NewService returns an empty household service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: sharedhh.NewStore(), logger: logger}
}

// AddBill catalogues a plan.
func (s *Service) AddBill(provider string, amountMinor int64, cadence string) (*sharedhh.Bill, error) {
	b, err := s.store.AddBill(provider, amountMinor, cadence)
	if err != nil {
		s.logger.Warn().Err(err).Str("provider", provider).Msg("household bill add failed")
		return nil, err
	}
	s.logger.Info().Str("bill_id", b.ID).Str("provider", b.Provider).Msg("household bill added")
	return b, nil
}

// CompareBills detects cheaper-comparable market plans.
func (s *Service) CompareBills(id string, market []sharedhh.MarketPlan) ([]sharedhh.MarketPlan, error) {
	out, err := s.store.CompareBills(id, market)
	if err != nil {
		s.logger.Warn().Err(err).Str("bill_id", id).Msg("household bill compare failed")
		return nil, err
	}
	return out, nil
}

// BillAction records cancel/switch/renew/ignore with async task states.
func (s *Service) BillAction(id, action string) (*sharedhh.Bill, error) {
	b, err := s.store.BillAction(id, action)
	if err != nil {
		s.logger.Warn().Err(err).Str("bill_id", id).Str("action", action).Msg("household bill action failed")
		return nil, err
	}
	s.logger.Info().Str("bill_id", id).Str("action", action).Msg("household bill action")
	return b, nil
}

// AddMandate records one detected direct debit.
func (s *Service) AddMandate(merchant, reference string, amountMinor int64) (*sharedhh.Mandate, error) {
	m, err := s.store.AddMandate(merchant, reference, amountMinor)
	if err != nil {
		s.logger.Warn().Err(err).Str("merchant", merchant).Msg("household mandate add failed")
		return nil, err
	}
	s.logger.Info().Str("mandate_id", m.ID).Str("merchant", m.Merchant).Msg("household mandate detected")
	return m, nil
}

// ListMandates returns the detection list.
func (s *Service) ListMandates() []*sharedhh.Mandate {
	return s.store.ListMandates()
}

// MigrateMandate starts a per-mandate migration task.
func (s *Service) MigrateMandate(id, targetAccount string) (*sharedhh.Mandate, error) {
	m, err := s.store.MigrateMandate(id, targetAccount)
	if err != nil {
		s.logger.Warn().Err(err).Str("mandate_id", id).Msg("household mandate migration failed")
		return nil, err
	}
	s.logger.Info().Str("mandate_id", id).Str("target", targetAccount).Msg("household mandate migration started")
	return m, nil
}

// VerifyFirstCollection completes the migration via first-collection proof.
func (s *Service) VerifyFirstCollection(id string) (*sharedhh.Mandate, error) {
	m, err := s.store.VerifyFirstCollection(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("mandate_id", id).Msg("household mandate verify failed")
		return nil, err
	}
	s.logger.Info().Str("mandate_id", id).Msg("household mandate first collection verified")
	return m, nil
}

// ScanClosure runs the pre-close safety scan.
func (s *Service) ScanClosure(in sharedhh.ClosureInput) sharedhh.ClosureResult {
	return sharedhh.ScanClosure(in)
}
