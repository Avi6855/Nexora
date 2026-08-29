package domain

import (
	"time"

	"github.com/google/uuid"
)

type TransferStatus string

const (
	TransferStatusPending   TransferStatus = "PENDING"
	TransferStatusCompleted TransferStatus = "COMPLETED"
	TransferStatusFailed    TransferStatus = "FAILED"
	TransferStatusReversed  TransferStatus = "REVERSED"
)

type Transfer struct {
	TransferID      uuid.UUID      `json:"transfer_id"`
	IdempotencyKey  string         `json:"idempotency_key"`
	FromAccountID   uuid.UUID      `json:"from_account_id"`
	ToAccountID     uuid.UUID      `json:"to_account_id"`
	Amount          int64          `json:"amount"`
	Currency        string         `json:"currency"`
	Status          TransferStatus `json:"status"`
	Description     string         `json:"description"`
	CreatedAt       time.Time      `json:"created_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

func NewTransfer(idempotencyKey string, fromAccountID, toAccountID uuid.UUID, amount int64, currency, description string) *Transfer {
	now := time.Now().UTC()
	return &Transfer{
		TransferID:     uuid.New(),
		IdempotencyKey: idempotencyKey,
		FromAccountID:  fromAccountID,
		ToAccountID:    toAccountID,
		Amount:         amount,
		Currency:       currency,
		Status:         TransferStatusPending,
		Description:    description,
		CreatedAt:      now,
	}
}

type CreateTransferRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	FromAccountID  string `json:"from_account_id"`
	ToAccountID    string `json:"to_account_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	Description    string `json:"description"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
