package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/money"
)

// ErrAccountLocked is returned when an operation is refused because the
// account is in emergency lockdown (money out blocked, money in allowed).
var ErrAccountLocked = errors.New("account is locked: outbound payments are temporarily disabled")

type AccountType string

const (
	AccountTypeCurrent AccountType = "CURRENT"
	AccountTypeSavings AccountType = "SAVINGS"
)

type AccountStatus string

const (
	AccountStatusActive   AccountStatus = "ACTIVE"
	AccountStatusFrozen   AccountStatus = "FROZEN"
	AccountStatusClosed   AccountStatus = "CLOSED"
	AccountStatusPending  AccountStatus = "PENDING"
)

type Account struct {
	AccountID        uuid.UUID     `json:"account_id"`
	UserID           uuid.UUID     `json:"user_id"`
	AccountType      AccountType   `json:"account_type"`
	Currency         string        `json:"currency"`
	AvailableBalance money.Money   `json:"available_balance"`
	CurrentBalance   money.Money   `json:"current_balance"`
	ReservedBalance  money.Money   `json:"reserved_balance"`
	Status           AccountStatus `json:"status"`
	// LockdownEnabled is the emergency "freeze money out" switch (Monzo-style
	// lockdown): when set, outbound card payments, bank transfers and cash
	// withdrawals are refused at the enforcement points. Money IN, Direct
	// Debits and internal savings sweeps still work.
	LockdownEnabled bool       `json:"lockdown_enabled"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func NewAccount(userID uuid.UUID, accountType AccountType, currency string) (*Account, error) {
	avail, err := money.NewMoney(0, currency)
	if err != nil {
		return nil, err
	}
	cur, err := money.NewMoney(0, currency)
	if err != nil {
		return nil, err
	}
	res, err := money.NewMoney(0, currency)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	return &Account{
		AccountID:        uuid.New(),
		UserID:           userID,
		AccountType:      accountType,
		Currency:         currency,
		AvailableBalance: avail,
		CurrentBalance:   cur,
		ReservedBalance:  res,
		Status:           AccountStatusActive,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

type CreateAccountRequest struct {
	UserID      string      `json:"user_id"`
	AccountType AccountType `json:"account_type"`
	Currency    string      `json:"currency"`
}

// SetLockdownRequest is the emergency-lockdown toggle payload.
type SetLockdownRequest struct {
	LockdownEnabled bool `json:"lockdown_enabled"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
