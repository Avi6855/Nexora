package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type TransferStatus string

const (
	TransferStatusPending   TransferStatus = "PENDING"
	TransferStatusUnknown   TransferStatus = "UNKNOWN"
	TransferStatusCompleted TransferStatus = "COMPLETED"
	TransferStatusFailed    TransferStatus = "FAILED"
	TransferStatusReversed  TransferStatus = "REVERSED"
)

// validTransitions is the transfer state machine. UNKNOWN exists because a
// booking that timed out is not a booking that was refused: the ledger may have
// moved the money and only the answer was lost. Recording UNKNOWN as FAILED
// tells the customer their transfer did not happen when it may well have.
var validTransitions = map[TransferStatus][]TransferStatus{
	TransferStatusPending:   {TransferStatusCompleted, TransferStatusFailed, TransferStatusUnknown},
	TransferStatusUnknown:   {TransferStatusCompleted, TransferStatusFailed},
	TransferStatusCompleted: {TransferStatusReversed},
	TransferStatusFailed:    {},
	TransferStatusReversed:  {},
}

// ErrNotIndeterminate marks a resolution asked for on a transfer whose outcome
// was never in doubt.
var ErrNotIndeterminate = errors.New("transfer outcome is not indeterminate")

func (s TransferStatus) CanTransitionTo(target TransferStatus) bool {
	for _, allowed := range validTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

type ResolutionOutcome string

const (
	// ResolutionOutcomeConfirmed means the money did move. The transfer is
	// completed, not re-booked twice: the ledger booking is idempotent on the
	// transfer's own key.
	ResolutionOutcomeConfirmed ResolutionOutcome = "CONFIRMED"
	// ResolutionOutcomeFailed means the money never moved.
	ResolutionOutcomeFailed ResolutionOutcome = "FAILED"
)

func (o ResolutionOutcome) Valid() bool {
	return o == ResolutionOutcomeConfirmed || o == ResolutionOutcomeFailed
}

// ResolveUnknownRequest is the internal reconciliation write-back body for a
// transfer whose booking outcome was indeterminate.
type ResolveUnknownRequest struct {
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	ResolvedBy string `json:"resolved_by,omitempty"`
}

type Transfer struct {
	TransferID     uuid.UUID      `json:"transfer_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	FromAccountID  uuid.UUID      `json:"from_account_id"`
	ToAccountID    uuid.UUID      `json:"to_account_id"`
	Amount         int64          `json:"amount"`
	Currency       string         `json:"currency"`
	Status         TransferStatus `json:"status"`
	Description    string         `json:"description"`
	CreatedAt      time.Time      `json:"created_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
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

func (t *Transfer) TransitionTo(target TransferStatus) error {
	if !t.Status.CanTransitionTo(target) {
		return fmt.Errorf("invalid transfer transition from %s to %s", t.Status, target)
	}
	t.Status = target
	if target == TransferStatusCompleted {
		now := time.Now().UTC()
		t.CompletedAt = &now
	}
	return nil
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
