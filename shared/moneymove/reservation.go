package moneymove

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ReservationStatus is the lifecycle of a funds reservation.
type ReservationStatus string

const (
	ReservationActive   ReservationStatus = "ACTIVE"
	ReservationReleased ReservationStatus = "RELEASED"
	ReservationConsumed ReservationStatus = "CONSUMED"
)

// Reservation is one atomic hold against an account balance.
type Reservation struct {
	ID        string            `json:"id"`
	Account   string            `json:"account"`
	Amount    int64             `json:"amount"`
	Status    ReservationStatus `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
}

// Fund sets the ledger balance for an account (operator/test setup).
func (s *Store) Fund(account string, balance int64) {
	if account == "" || balance < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.balances[account] = balance
	s.logger.Info().Str("account", account).Int64("balance", balance).Msg("moneymove account funded")
}

// Available returns balance minus active reservations. It never goes
// negative: Reserve refuses oversubscription atomically under the mutex.
func (s *Store) Available(account string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.availableLocked(account)
}

func (s *Store) availableLocked(account string) int64 {
	var reserved int64
	for _, r := range s.reservations {
		if r.Account == account && r.Status == ReservationActive {
			reserved += r.Amount
		}
	}
	return s.balances[account] - reserved
}

// Reserve atomically holds amount on account, returning a reservation id.
// The balance check and the insert happen under the single store mutex, so
// concurrent oversubscription has exactly one winner.
func (s *Store) Reserve(account string, amount int64) (*Reservation, error) {
	if account == "" {
		return nil, fmt.Errorf("account is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.availableLocked(account) < amount {
		return nil, ErrInsufficientFunds
	}
	r := &Reservation{
		ID:        uuid.NewString(),
		Account:   account,
		Amount:    amount,
		Status:    ReservationActive,
		CreatedAt: time.Now().UTC(),
	}
	s.reservations[r.ID] = r
	s.logger.Info().Str("reservation_id", r.ID).Str("account", account).Int64("amount", amount).Msg("moneymove reserved")
	cp := *r
	return &cp, nil
}

// Release frees an ACTIVE hold without moving funds.
func (s *Store) Release(id string) (*Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	if r.Status != ReservationActive {
		return nil, ErrReservationNotActive
	}
	r.Status = ReservationReleased
	s.logger.Info().Str("reservation_id", id).Msg("moneymove reservation released")
	cp := *r
	return &cp, nil
}

// Consume settles an ACTIVE hold: the hold closes and the ledger balance
// drops by the reserved amount.
func (s *Store) Consume(id string) (*Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	if r.Status != ReservationActive {
		return nil, ErrReservationNotActive
	}
	if s.balances[r.Account] < r.Amount {
		return nil, ErrInsufficientFunds
	}
	s.balances[r.Account] -= r.Amount
	r.Status = ReservationConsumed
	s.logger.Info().Str("reservation_id", id).Msg("moneymove reservation consumed")
	cp := *r
	return &cp, nil
}

// GetReservation returns a copy of a reservation.
func (s *Store) GetReservation(id string) (*Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	cp := *r
	return &cp, nil
}
