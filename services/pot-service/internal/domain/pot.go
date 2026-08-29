package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPotNotFound       = errors.New("pot not found")
	ErrInsufficientFunds = errors.New("insufficient pot balance")
	ErrInvalidAmount     = errors.New("amount must be positive")
	ErrPotClosed         = errors.New("pot is closed")
	ErrPotNotClosed      = errors.New("pot must be closed before deleting")
	ErrBalanceNonZero    = errors.New("pot balance must be zero before deleting")
)

type PotStatus string

const (
	PotStatusActive PotStatus = "ACTIVE"
	PotStatusClosed PotStatus = "CLOSED"
)

type Pot struct {
	PotID          uuid.UUID `json:"pot_id"`
	UserID         uuid.UUID `json:"user_id"`
	Name           string    `json:"name"`
	TargetAmount   int64     `json:"target_amount"`
	CurrentAmount  int64     `json:"current_amount"`
	Currency       string    `json:"currency"`
	Status         PotStatus `json:"status"`
	RoundUpEnabled bool      `json:"round_up_enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func NewPot(userID uuid.UUID, name string, targetAmount int64, currency string, roundUp bool) *Pot {
	now := time.Now().UTC()
	return &Pot{
		PotID:          uuid.New(),
		UserID:         userID,
		Name:           name,
		TargetAmount:   targetAmount,
		CurrentAmount:  0,
		Currency:       currency,
		Status:         PotStatusActive,
		RoundUpEnabled: roundUp,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func (p *Pot) Deposit(amount int64) error {
	if p.Status == PotStatusClosed {
		return fmt.Errorf("%w: cannot deposit to a closed pot", ErrPotClosed)
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	p.CurrentAmount += amount
	p.UpdatedAt = time.Now().UTC()
	return nil
}

func (p *Pot) Withdraw(amount int64) error {
	if p.Status == PotStatusClosed {
		return fmt.Errorf("%w: cannot withdraw from a closed pot", ErrPotClosed)
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if p.CurrentAmount < amount {
		return fmt.Errorf("%w: requested %d, available %d", ErrInsufficientFunds, amount, p.CurrentAmount)
	}
	p.CurrentAmount -= amount
	p.UpdatedAt = time.Now().UTC()
	return nil
}

func (p *Pot) Close() error {
	if p.Status == PotStatusClosed {
		return errors.New("pot is already closed")
	}
	p.Status = PotStatusClosed
	p.UpdatedAt = time.Now().UTC()
	return nil
}

func (p *Pot) Delete() error {
	if p.Status != PotStatusClosed {
		return ErrPotNotClosed
	}
	if p.CurrentAmount != 0 {
		return ErrBalanceNonZero
	}
	return nil
}

type CreatePotRequest struct {
	Name         string `json:"name"`
	TargetAmount int64  `json:"target_amount"`
	Currency     string `json:"currency"`
	RoundUp      bool   `json:"round_up"`
}

type AmountRequest struct {
	Amount int64 `json:"amount"`
}

type RenameRequest struct {
	Name string `json:"name"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
