package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type PaymentState string

const (
	PaymentStateCreated    PaymentState = "CREATED"
	PaymentStateAuthorized PaymentState = "AUTHORIZED"
	PaymentStateProcessing PaymentState = "PROCESSING"
	PaymentStateUnknown    PaymentState = "UNKNOWN"
	PaymentStateConfirmed  PaymentState = "CONFIRMED"
	PaymentStateSettled    PaymentState = "SETTLED"
	PaymentStateFailed     PaymentState = "FAILED"
	PaymentStateReversed   PaymentState = "REVERSED"
	PaymentStateCancelled  PaymentState = "CANCELLED"
)

var validTransitions = map[PaymentState][]PaymentState{
	PaymentStateCreated:    {PaymentStateAuthorized, PaymentStateFailed, PaymentStateReversed, PaymentStateCancelled},
	PaymentStateAuthorized: {PaymentStateProcessing, PaymentStateFailed, PaymentStateReversed, PaymentStateCancelled},
	PaymentStateProcessing: {PaymentStateConfirmed, PaymentStateFailed, PaymentStateUnknown},
	PaymentStateUnknown:    {PaymentStateConfirmed, PaymentStateFailed},
	PaymentStateConfirmed:  {PaymentStateSettled, PaymentStateReversed},
	PaymentStateSettled:    {},
	PaymentStateFailed:     {},
	PaymentStateReversed:   {},
	PaymentStateCancelled:  {},
}

func (s PaymentState) CanTransitionTo(target PaymentState) bool {
	allowed, ok := validTransitions[s]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == target {
			return true
		}
	}
	return false
}

type PaymentType string

const (
	PaymentTypeCard         PaymentType = "CARD"
	PaymentTypeBankTransfer PaymentType = "BANK_TRANSFER"
	PaymentTypeInternal     PaymentType = "INTERNAL"
)

type Payment struct {
	PaymentID            uuid.UUID            `json:"payment_id"`
	IdempotencyKey       string               `json:"idempotency_key"`
	AccountID            uuid.UUID            `json:"account_id"`
	UserID               uuid.UUID            `json:"user_id"`
	PaymentType          PaymentType          `json:"payment_type"`
	Amount               int64                `json:"amount"`
	Currency             string               `json:"currency"`
	State                PaymentState         `json:"state"`
	FailureReason        string               `json:"failure_reason,omitempty"`
	CounterpartyID       string               `json:"counterparty_id"`
	CounterpartyName     string               `json:"counterparty_name"`
	Reference            string               `json:"reference"`
	FraudScore           float64              `json:"fraud_score,omitempty"`
	FraudAction          string               `json:"fraud_action,omitempty"`
	LedgerTransactionID  string               `json:"ledger_transaction_id,omitempty"`
	ReservationID        string               `json:"reservation_id,omitempty"`
	Metadata             map[string]string    `json:"metadata,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	AuthorizedAt         *time.Time           `json:"authorized_at,omitempty"`
	SettledAt            *time.Time           `json:"settled_at,omitempty"`
	ProviderResponse     *ProviderResponse    `json:"provider_response,omitempty"`
}

type ProviderResponse struct {
	ProviderID      string    `json:"provider_id"`
	TransactionRef  string    `json:"transaction_ref"`
	Status          string    `json:"status"`
	Message         string    `json:"message,omitempty"`
	ResponseCode    string    `json:"response_code,omitempty"`
	ReceivedAt      time.Time `json:"received_at"`
}

func NewPayment(idempotencyKey string, accountID, userID uuid.UUID, paymentType PaymentType, amount int64, currency, counterpartyID, counterpartyName, reference string) *Payment {
	now := time.Now().UTC()
	return &Payment{
		PaymentID:       uuid.New(),
		IdempotencyKey:  idempotencyKey,
		AccountID:       accountID,
		UserID:          userID,
		PaymentType:     paymentType,
		Amount:          amount,
		Currency:        currency,
		State:           PaymentStateCreated,
		CounterpartyID:  counterpartyID,
		CounterpartyName: counterpartyName,
		Reference:       reference,
		Metadata:        make(map[string]string),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (p *Payment) TransitionTo(target PaymentState) error {
	if !p.State.CanTransitionTo(target) {
		return fmt.Errorf("invalid transition from %s to %s", p.State, target)
	}
	p.State = target
	p.UpdatedAt = time.Now().UTC()

	now := time.Now().UTC()
	switch target {
	case PaymentStateAuthorized:
		p.AuthorizedAt = &now
	case PaymentStateSettled:
		p.SettledAt = &now
	}

	return nil
}

type CreatePaymentRequest struct {
	IdempotencyKey  string            `json:"idempotency_key"`
	AccountID       string            `json:"account_id"`
	PaymentType     PaymentType       `json:"payment_type"`
	Amount          int64             `json:"amount"`
	Currency        string            `json:"currency"`
	CounterpartyID  string            `json:"counterparty_id"`
	CounterpartyName string           `json:"counterparty_name"`
	Reference       string            `json:"reference"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type ProcessPaymentRequest struct {
	ProviderID string `json:"provider_id,omitempty"`
}

type ProviderCallbackRequest struct {
	PaymentID      string `json:"payment_id"`
	ProviderID     string `json:"provider_id"`
	TransactionRef string `json:"transaction_ref"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`
	ResponseCode   string `json:"response_code,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
