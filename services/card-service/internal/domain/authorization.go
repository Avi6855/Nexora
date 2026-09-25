package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAuthNotFound             = errors.New("authorization not found")
	ErrAuthNotApprovable        = errors.New("authorization is not in an approvable state")
	ErrAuthNotCapturable        = errors.New("authorization is not in a capturable state")
	ErrAuthNotVoidable          = errors.New("authorization is not in a voidable state")
	ErrAuthCardMismatch         = errors.New("authorization does not belong to this card")
	ErrAuthorizationDeclined    = errors.New("authorization declined")
	ErrInsufficientFundsDecline = errors.New("insufficient funds")
	ErrRiskServiceUnavailable   = errors.New("risk decisioning unavailable; failing closed")

	// Strong customer authentication (PSD2 / 3-D Secure step-up).
	ErrAuthNotChallenged  = errors.New("authorization is not awaiting a step-up challenge")
	ErrChallengeNotFound  = errors.New("sca challenge not found")
	ErrChallengeStale     = errors.New("sca challenge was already answered")
	ErrChallengeOTP       = errors.New("sca one-time code rejected")
	ErrSCANotConfigured   = errors.New("strong customer authentication is not configured")
	ErrAuthNotRefundable  = errors.New("authorization is not in a refundable state")
	ErrRefundAmount       = errors.New("refund amount is invalid")
	ErrRefundOverCaptured = errors.New("refund exceeds the captured amount")
)

type AuthorizationStatus string

const (
	AuthStatusPending AuthorizationStatus = "PENDING"
	// AuthStatusChallenged is the PSD2 step-up state: the presentment needs
	// strong customer authentication before any money is held. No reservation
	// exists in this state, so an unanswered challenge never freezes funds.
	AuthStatusChallenged        AuthorizationStatus = "CHALLENGED"
	AuthStatusApproved          AuthorizationStatus = "APPROVED"
	AuthStatusDeclined          AuthorizationStatus = "DECLINED"
	AuthStatusCaptured          AuthorizationStatus = "CAPTURED"
	AuthStatusVoided            AuthorizationStatus = "VOIDED"
	AuthStatusPartiallyRefunded AuthorizationStatus = "PARTIALLY_REFUNDED"
	AuthStatusRefunded          AuthorizationStatus = "REFUNDED"
)

type AuthorizationDecision string

const (
	AuthDecisionApprove AuthorizationDecision = "APPROVE"
	AuthDecisionReview  AuthorizationDecision = "REVIEW"
	AuthDecisionDecline AuthorizationDecision = "DECLINE"
	// AuthDecisionChallenge is returned when the presentment may proceed only
	// after the cardholder answers a 3-D Secure step-up.
	AuthDecisionChallenge AuthorizationDecision = "CHALLENGE"
)

// CardAuthorization is the persisted record of one merchant presentment
// (tap / online card use). Every field is real: merchant context comes from
// the request, decision/risk from the fraud engine over stored history, and
// the reservation is the live funds hold in the ledger.
type CardAuthorization struct {
	AuthorizationID  uuid.UUID             `json:"authorization_id"`
	CardID           uuid.UUID             `json:"card_id"`
	UserID           uuid.UUID             `json:"user_id"`
	AccountID        uuid.UUID             `json:"account_id"`
	Amount           int64                 `json:"amount"`
	Currency         string                `json:"currency"`
	Merchant         string                `json:"merchant"`
	MerchantCategory string                `json:"merchant_category"`
	MerchantCity     string                `json:"merchant_city"`
	MerchantCountry  string                `json:"merchant_country"`
	Latitude         float64               `json:"latitude"`
	Longitude        float64               `json:"longitude"`
	TerminalID       string                `json:"terminal_id"`
	Decision         AuthorizationDecision `json:"decision"`
	DeclineReason    string                `json:"decline_reason,omitempty"`
	RiskScore        float64               `json:"risk_score"`
	RiskAction       string                `json:"risk_action"`
	RiskLevel        string                `json:"risk_level"`
	RiskReasons      string                `json:"risk_reasons,omitempty"`
	ReservationID    uuid.UUID             `json:"reservation_id,omitempty"`
	// ChallengeID is the outstanding/completed 3-D Secure step-up, if any.
	ChallengeID uuid.UUID `json:"challenge_id,omitempty"`
	// SCAExemption records the PSD2 exemption the approved presentment relied
	// on (empty when the cardholder authenticated through a challenge).
	SCAExemption string              `json:"sca_exemption,omitempty"`
	Status       AuthorizationStatus `json:"status"`
	LatencyMs    int64               `json:"latency_ms"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
	CapturedAt   *time.Time          `json:"captured_at,omitempty"`
	VoidedAt     *time.Time          `json:"voided_at,omitempty"`
	// RefundedAmount is the total credited back to the customer across all
	// (partial) refunds of this captured presentment.
	RefundedAmount int64      `json:"refunded_amount,omitempty"`
	RefundedAt     *time.Time `json:"refunded_at,omitempty"`
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
	// MerchantInitiated marks a follow-on merchant transaction (recurring
	// billing, no-show fee) that claims the PSD2 merchant-initiated exemption.
	// The claim is only honoured for a merchant that already has a captured
	// presentment on this card, so a merchant cannot exempt itself from strong
	// customer authentication by asserting the flag.
	MerchantInitiated bool `json:"merchant_initiated"`
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

// IsEcommerce reports whether the presentment is card-not-present (online or
// MOTO). The acquirer tags the entry mode on the authorisation message; the
// terminal id carries it directly, and known on-line merchants are recognised
// from the descriptor so a mislabelled terminal still cannot dodge the
// card-not-present controls (online payments toggle, SCA step-up).
func (r *AuthorizeCardRequest) IsEcommerce() bool {
	merchant := strings.ToLower(r.Merchant)
	return strings.EqualFold(r.TerminalID, "ECOM") ||
		strings.Contains(merchant, "amazon") || strings.Contains(merchant, ".com") ||
		strings.Contains(merchant, "online") || strings.Contains(merchant, "app store") ||
		strings.Contains(merchant, "google play") || strings.Contains(merchant, "steam")
}

// RefundAuthorizationRequest is a merchant- or operations-initiated credit
// against a captured card presentment. A partial refund is expressed as an
// amount below the captured total; the remaining balance stays refundable.
type RefundAuthorizationRequest struct {
	Amount int64  `json:"amount"`
	Reason string `json:"reason,omitempty"`
}

func (r *RefundAuthorizationRequest) Validate() error {
	if r.Amount <= 0 {
		return ErrRefundAmount
	}
	return nil
}
