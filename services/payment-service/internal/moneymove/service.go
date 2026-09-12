// Package moneymove wires shared/moneymove into payment-service as a live
// HTTP surface under /v1/money-move.
package moneymove

import (
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/moneymove"
)

// Service is the payment-service money-movement store backed by
// shared/moneymove. The shared store is already mutex-guarded; the service
// adds logging and the HTTP-facing façade.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds the service. The logger is used for mutation logs.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(logger), logger: logger}
}

// RecordIntent stores a new intent with its execution plan.
func (s *Service) RecordIntent(customerStatement string, amount int64, payee string, steps []string) (*shared.IntentRecord, error) {
	rec, err := s.store.RecordIntent(customerStatement, amount, payee, steps)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("intent_id", rec.Intent.ID).Msg("moneymove intent recorded")
	return rec, nil
}

// ExecuteIntent records actual legs, flagging divergence.
func (s *Service) ExecuteIntent(id string, legs []string) (*shared.IntentRecord, error) {
	rec, err := s.store.ExecuteIntent(id, legs)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("intent_id", id).Bool("diverged", rec.Diverged).Msg("moneymove intent executed")
	return rec, nil
}

// GetIntent returns one intent record.
func (s *Service) GetIntent(id string) (*shared.IntentRecord, error) {
	return s.store.GetIntent(id)
}

// Divergence reports the divergence flag and reason.
func (s *Service) Divergence(id string) (bool, string, error) {
	return s.store.Divergence(id)
}

// CreateInstruction stores a new v1 instruction.
func (s *Service) CreateInstruction(amount int64, payee string) (*shared.Instruction, error) {
	ins, err := s.store.CreateInstruction(amount, payee)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("instruction_id", ins.ID).Msg("moneymove instruction created")
	return ins, nil
}

// GetInstruction returns the current instruction version.
func (s *Service) GetInstruction(id string) (*shared.Instruction, error) {
	return s.store.GetInstruction(id)
}

// GetVersion returns the current version number.
func (s *Service) GetVersion(id string) (int, error) {
	return s.store.GetVersion(id)
}

// History returns v1..vN snapshots.
func (s *Service) History(id string) ([]shared.Instruction, error) {
	return s.store.History(id)
}

// UpdateInstruction applies an optimistic-concurrency mutation.
func (s *Service) UpdateInstruction(id string, expectedVersion int, amount int64, payee string) (*shared.Instruction, error) {
	ins, err := s.store.UpdateInstruction(id, expectedVersion, amount, payee)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("instruction_id", id).Int("version", ins.Version).Msg("moneymove instruction updated")
	return ins, nil
}

// Evaluate checks preconditions P(payment,state,time).
func (s *Service) Evaluate(payment shared.PreconditionPayment, state shared.PreconditionState, now time.Time) shared.EvaluationResult {
	return s.store.Evaluate(payment, state, now)
}

// Fund sets an account balance (operator/test setup).
func (s *Service) Fund(account string, balance int64) {
	s.store.Fund(account, balance)
}

// Available reports balance minus active holds.
func (s *Service) Available(account string) int64 {
	return s.store.Available(account)
}

// Reserve atomically holds funds.
func (s *Service) Reserve(account string, amount int64) (*shared.Reservation, error) {
	r, err := s.store.Reserve(account, amount)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("reservation_id", r.ID).Msg("moneymove reserved")
	return r, nil
}

// Release frees a hold.
func (s *Service) Release(id string) (*shared.Reservation, error) {
	r, err := s.store.Release(id)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("reservation_id", id).Msg("moneymove reservation released")
	return r, nil
}

// Consume settles a hold.
func (s *Service) Consume(id string) (*shared.Reservation, error) {
	r, err := s.store.Consume(id)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("reservation_id", id).Msg("moneymove reservation consumed")
	return r, nil
}

// GetReservation returns one reservation.
func (s *Service) GetReservation(id string) (*shared.Reservation, error) {
	return s.store.GetReservation(id)
}

// Acquire takes a fencing lease.
func (s *Service) Acquire(resource string, ttl time.Duration) (*shared.Lease, error) {
	l, err := s.store.Acquire(resource, ttl)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("lease_id", l.ID).Msg("moneymove lease acquired")
	return l, nil
}

// Renew extends a lease.
func (s *Service) Renew(id string, token uint64, ttl time.Duration) (*shared.Lease, error) {
	l, err := s.store.Renew(id, token, ttl)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("lease_id", id).Msg("moneymove lease renewed")
	return l, nil
}

// ExecuteUnderLease validates the fencing token before executing.
func (s *Service) ExecuteUnderLease(id string, token uint64) (*shared.Lease, error) {
	l, err := s.store.ExecuteUnderLease(id, token)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("lease_id", id).Msg("moneymove executed under lease")
	return l, nil
}

// GetLease returns one lease.
func (s *Service) GetLease(id string) (*shared.Lease, error) {
	return s.store.GetLease(id)
}

// Decide allocates liquidity across competing overdraft claims.
func (s *Service) Decide(available int64, claims []shared.Claim) ([]shared.Decision, error) {
	return s.store.Decide(available, claims)
}
