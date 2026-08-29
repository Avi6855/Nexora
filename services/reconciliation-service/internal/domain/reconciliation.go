package domain

import (
	"time"

	"github.com/google/uuid"
)

type ReconciliationStatus string

const (
	ReconciliationStatusPending     ReconciliationStatus = "PENDING"
	ReconciliationStatusMatched     ReconciliationStatus = "MATCHED"
	ReconciliationStatusDiscrepancy ReconciliationStatus = "DISCREPANCY"
	ReconciliationStatusResolved    ReconciliationStatus = "RESOLVED"
	ReconciliationStatusEscalated   ReconciliationStatus = "ESCALATED"
)

type ResolutionType string

const (
	ResolutionTypeAutoMatch        ResolutionType = "AUTO_MATCH"
	ResolutionTypeManualReview     ResolutionType = "MANUAL_REVIEW"
	ResolutionTypeProviderOverride ResolutionType = "PROVIDER_OVERRIDE"
	ResolutionTypeInternalOverride ResolutionType = "INTERNAL_OVERRIDE"
	ResolutionTypeWriteOff         ResolutionType = "WRITE_OFF"
	ResolutionTypeReversal         ResolutionType = "REVERSAL"
)

type ReconciliationCase struct {
	CaseID            uuid.UUID              `json:"case_id"`
	PaymentID         uuid.UUID              `json:"payment_id"`
	InternalState     string                 `json:"internal_state"`
	ExternalState     string                 `json:"external_state"`
	InternalAmount    int64                  `json:"internal_amount"`
	ExternalAmount    int64                  `json:"external_amount"`
	Currency          string                 `json:"currency"`
	Status            ReconciliationStatus   `json:"status"`
	Resolution        ResolutionType         `json:"resolution,omitempty"`
	DiscrepancyReason string                 `json:"discrepancy_reason,omitempty"`
	ProviderRef       string                 `json:"provider_ref,omitempty"`
	AttemptCount      int                    `json:"attempt_count"`
	MaxAttempts       int                    `json:"max_attempts"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	ResolvedAt        *time.Time             `json:"resolved_at,omitempty"`
	AuditTrail        []ReconciliationAudit  `json:"audit_trail,omitempty"`
}

type ReconciliationAudit struct {
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	Details   string    `json:"details"`
	Timestamp time.Time `json:"timestamp"`
}

type ReconciliationRecord struct {
	RecordID          uuid.UUID            `json:"record_id"`
	TransactionID     uuid.UUID            `json:"transaction_id"`
	Source            string               `json:"source"`
	Target            string               `json:"target"`
	Amount            int64                `json:"amount"`
	Currency          string               `json:"currency"`
	Status            ReconciliationStatus `json:"status"`
	DiscrepancyReason string               `json:"discrepancy_reason,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	ResolvedAt        *time.Time           `json:"resolved_at,omitempty"`
}

func NewReconciliationCase(paymentID uuid.UUID, internalState, externalState string, internalAmount, externalAmount int64, currency string) *ReconciliationCase {
	now := time.Now().UTC()
	return &ReconciliationCase{
		CaseID:         uuid.New(),
		PaymentID:      paymentID,
		InternalState:  internalState,
		ExternalState:  externalState,
		InternalAmount: internalAmount,
		ExternalAmount: externalAmount,
		Currency:       currency,
		Status:         ReconciliationStatusPending,
		AttemptCount:   0,
		MaxAttempts:    3,
		CreatedAt:      now,
		UpdatedAt:      now,
		AuditTrail:     make([]ReconciliationAudit, 0),
	}
}

func NewReconciliationRecord(transactionID uuid.UUID, source, target string, amount int64, currency string) *ReconciliationRecord {
	return &ReconciliationRecord{
		RecordID:      uuid.New(),
		TransactionID: transactionID,
		Source:        source,
		Target:        target,
		Amount:        amount,
		Currency:      currency,
		Status:        ReconciliationStatusPending,
		CreatedAt:     time.Now().UTC(),
	}
}

type ProviderPaymentStatus struct {
	PaymentID       string `json:"payment_id"`
	Status          string `json:"status"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	TransactionRef  string `json:"transaction_ref"`
	ProviderMessage string `json:"provider_message,omitempty"`
}

type CreateReconciliationRequest struct {
	TransactionID string `json:"transaction_id"`
	Source        string `json:"source"`
	Target        string `json:"target"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
}

type ReconcilePaymentRequest struct {
	PaymentID        string `json:"payment_id"`
	ProviderResponse string `json:"provider_response"`
}

type ReconcileUnknownPaymentRequest struct {
	PaymentID string `json:"payment_id"`
}

type RunScheduledReconciliationRequest struct {
	BatchSize int `json:"batch_size"`
}

type ReconcileResult struct {
	CaseID          uuid.UUID          `json:"case_id"`
	PaymentID       uuid.UUID          `json:"payment_id"`
	Matched         bool               `json:"matched"`
	InternalState   string             `json:"internal_state"`
	ExternalState   string             `json:"external_state"`
	Resolution      ResolutionType     `json:"resolution,omitempty"`
	Discrepancy     string             `json:"discrepancy_reason,omitempty"`
	ReconciledAt    time.Time          `json:"reconciled_at"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
