package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrCardNotFound       = errors.New("card not found")
	ErrInvalidCardState   = errors.New("invalid card state for this operation")
	ErrCardAlreadyBlocked = errors.New("card is already blocked")
	ErrCardAlreadyFrozen  = errors.New("card is already frozen")
	ErrCardAlreadyActive  = errors.New("card is already active")
)

type CardType string

const (
	CardTypePhysical CardType = "PHYSICAL"
	CardTypeVirtual  CardType = "VIRTUAL"
)

type CardStatus string

const (
	CardStatusActive  CardStatus = "ACTIVE"
	CardStatusFrozen  CardStatus = "FROZEN"
	CardStatusBlocked CardStatus = "BLOCKED"
	CardStatusExpired CardStatus = "EXPIRED"
)

type SpendingControls struct {
	DailyLimit   int64 `json:"daily_limit"`
	MonthlyLimit int64 `json:"monthly_limit"`
}

type Card struct {
	CardID           uuid.UUID      `json:"card_id"`
	UserID           uuid.UUID      `json:"user_id"`
	AccountID        uuid.UUID      `json:"account_id"`
	CardNumber       string         `json:"card_number,omitempty"`
	CardNumberLast4  string         `json:"card_number_last4"`
	CardType         CardType       `json:"card_type"`
	Status           CardStatus     `json:"status"`
	SpendingLimit    int64          `json:"spending_limit"`
	DailyLimit       int64          `json:"daily_limit"`
	MonthlyLimit     int64          `json:"monthly_limit"`
	SpendingControls SpendingControls `json:"spending_controls"`
	Currency         string         `json:"currency"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func NewCard(userID, accountID uuid.UUID, cardNumberLast4 string, cardType CardType, spendingLimit, dailyLimit, monthlyLimit int64, currency string) *Card {
	now := time.Now().UTC()
	return &Card{
		CardID:          uuid.New(),
		UserID:          userID,
		AccountID:       accountID,
		CardNumberLast4: cardNumberLast4,
		CardType:        cardType,
		Status:          CardStatusActive,
		SpendingLimit:   spendingLimit,
		DailyLimit:      dailyLimit,
		MonthlyLimit:    monthlyLimit,
		SpendingControls: SpendingControls{
			DailyLimit:   dailyLimit,
			MonthlyLimit: monthlyLimit,
		},
		Currency:  currency,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (c *Card) MaskedNumber() string {
	return "****-****-****-" + c.CardNumberLast4
}

func (c *Card) Freeze() error {
	if c.Status == CardStatusBlocked {
		return fmt.Errorf("%w: cannot freeze a blocked card", ErrInvalidCardState)
	}
	if c.Status == CardStatusFrozen {
		return ErrCardAlreadyFrozen
	}
	if c.Status == CardStatusExpired {
		return fmt.Errorf("%w: cannot freeze an expired card", ErrInvalidCardState)
	}
	c.Status = CardStatusFrozen
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (c *Card) Unfreeze() error {
	if c.Status == CardStatusBlocked {
		return fmt.Errorf("%w: cannot unfreeze a blocked card", ErrInvalidCardState)
	}
	if c.Status == CardStatusActive {
		return ErrCardAlreadyActive
	}
	if c.Status == CardStatusExpired {
		return fmt.Errorf("%w: cannot unfreeze an expired card", ErrInvalidCardState)
	}
	c.Status = CardStatusActive
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (c *Card) Block() error {
	if c.Status == CardStatusBlocked {
		return ErrCardAlreadyBlocked
	}
	if c.Status == CardStatusExpired {
		return fmt.Errorf("%w: card is already expired", ErrInvalidCardState)
	}
	c.Status = CardStatusBlocked
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (c *Card) UpdateSpendingLimits(dailyLimit, monthlyLimit int64) {
	c.DailyLimit = dailyLimit
	c.MonthlyLimit = monthlyLimit
	c.SpendingControls.DailyLimit = dailyLimit
	c.SpendingControls.MonthlyLimit = monthlyLimit
	c.UpdatedAt = time.Now().UTC()
}

type CreateCardRequest struct {
	AccountID    string   `json:"account_id"`
	CardType     CardType `json:"card_type"`
	SpendingLimit int64   `json:"spending_limit"`
	DailyLimit    int64   `json:"daily_limit"`
	MonthlyLimit  int64   `json:"monthly_limit"`
	Currency     string   `json:"currency"`
}

type UpdateLimitsRequest struct {
	DailyLimit   int64 `json:"daily_limit"`
	MonthlyLimit int64 `json:"monthly_limit"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
