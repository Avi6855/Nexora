package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/money"
)

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
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
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

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
