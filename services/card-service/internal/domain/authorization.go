package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAuthNotFound           = errors.New("authorization not found")
	ErrAuthNotApprovable      = errors.New("authorization is not in an approvable state")
	ErrAuthNotCapturable      = errors.New("authorization is not in a capturable state")
	ErrAuthNotVoidable        = errors.New("authorization is not in a voidable state")
	ErrAuthCardMismatch       = errors.New("authorization does not belong to this card")
	ErrAuthorizationDeclined  = errors.New("authorization declined")
	ErrInsufficientFundsDecline = errors.New("insufficient funds")
	ErrRiskServiceUnavailable = errors.New("risk decisioning unavailable; failing closed")
)

type AuthorizationStatus string

const (
	AuthStatusPending  AuthorizationStatus = "PENDING"
	AuthStatusApproved AuthorizationStatus = "APPROVED"
	AuthStatusDeclined AuthorizationStatus = "DECLINED"
	AuthStatusCaptured AuthorizationStatus = "CAPTURED"
	AuthStatusVoided   AuthorizationStatus = "VOIDED"
)

type AuthorizationDecision string

const (
	AuthDecisionApprove AuthorizationDecision = "APPROVE"
	AuthDecisionReview  AuthorizationDecision = "REVIEW"
	AuthDecisionDecline AuthorizationDecision = "DECLINE"
)

// CardAuthorization is the persisted record of one merchant presentment
// (tap / online card use). Every field is real: merchant context comes from
// the request, decision/risk from the fraud engine over stored history, and
// the reservation is the live funds hold in the ledger.
type CardAuthorization struct {
	AuthorizationID  uuid.UUID            `json:"authorization_id"`
	CardID           uuid.UUID            `json:"card_id"`
	UserID           uuid.UUID            `json:"user_id"`
	AccountID        uuid.UUID            `json:"account_id"`
	Amount           int64                `json:"amount"`
	Currency         string               `json:"currency"`
	Merchant         string               `json:"merchant"`
	MerchantCategory string               `json:"merchant_category"`
	MerchantCity     string               `json:"merchant_city"`
	MerchantCountry  string               `json:"merchant_country"`
	Latitude         float64              `json:"latitude"`
	Longitude        float64              `json:"longitude"`
	TerminalID       string               `json:"terminal_id"`
	Decision         AuthorizationDecision `json:"decision"`
	DeclineReason    string               `json:"decline_reason,omitempty"`
	RiskScore        float64              `json:"risk_score"`
	RiskAction       string               `json:"risk_action"`
	RiskLevel        string               `json:"risk_level"`
	RiskReasons      string               `json:"risk_reasons,omitempty"`
	ReservationID    uuid.UUID            `json:"reservation_id,omitempty"`
	Status           AuthorizationStatus  `json:"status"`
	LatencyMs        int64                `json:"latency_ms"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
	CapturedAt       *time.Time           `json:"captured_at,omitempty"`
	VoidedAt         *time.Time           `json:"voided_at,omitempty"`
}

// AuthorizeCardRequest is the merchant presentment payload. It mirrors the
// fields of an ISO 8583 / EMV authorisation message relevant to this demo.
type AuthorizeCardRequest struct {
	Amount           int64   `json:"amount"`
	Currency         string  `json:"currency"`
	Merchant         string  `json:"merchant"`
	MerchantCategory string  `json:"merchant_category"`
	MerchantCity     string  `json:"merchant_city"`
	MerchantCountry  string  `json:"merchant_country"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	TerminalID       string  `json:"terminal_id"`
	DeviceID         string  `json:"device_id"`
}

func (r *AuthorizeCardRequest) Validate() error {
	if r.Amount <= 0 {
		return errors.New("amount must be positive")
	}
	if r.Currency == "" {
		return errors.New("currency is required")
	}
	if r.Merchant == "" {
		return errors.New("merchant is required")
	}
	return nil
}
