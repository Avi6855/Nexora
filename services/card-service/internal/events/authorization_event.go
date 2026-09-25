package events

import "time"

// AuthorizationEvent is the flat, consumer-friendly payload published to
// nexora.card.authorization.*. It carries everything the notification / feed
// pipeline needs (user, account, merchant, amount, live balances) and is the
// contract the notification-service consumer decodes.
type AuthorizationEvent struct {
	AuthorizationID  string  `json:"authorization_id"`
	CardID           string  `json:"card_id"`
	UserID           string  `json:"user_id"`
	AccountID        string  `json:"account_id"`
	Amount           int64   `json:"amount"`
	Currency         string  `json:"currency"`
	Merchant         string  `json:"merchant"`
	MerchantCategory string  `json:"merchant_category"`
	MerchantCity     string  `json:"merchant_city"`
	MerchantCountry  string  `json:"merchant_country"`
	TerminalID       string  `json:"terminal_id"`
	Status           string  `json:"status"` // CHALLENGED | APPROVED | DECLINED | CAPTURED | VOIDED | PARTIALLY_REFUNDED | REFUNDED
	Decision         string  `json:"decision"`
	DeclineReason    string  `json:"decline_reason,omitempty"`
	RiskScore        float64 `json:"risk_score"`
	RiskReasons      string  `json:"risk_reasons,omitempty"`
	ReservationID    string  `json:"reservation_id,omitempty"`
	// SCAExemption is the PSD2 exemption an approval relied on instead of a
	// step-up (card_present_cvm, low_value, transaction_risk_analysis, ...).
	SCAExemption string `json:"sca_exemption,omitempty"`
	// Challenge fields are set on card.authorization.challenged.
	ChallengeID     string     `json:"challenge_id,omitempty"`
	ChallengeExpiry *time.Time `json:"challenge_expires_at,omitempty"`
	// ChallengeOTP is the one-time code for the cardholder. It travels only on
	// the internal step-up event for the notification pipeline (in-app push /
	// SMS); it is never logged and never returned to the merchant.
	ChallengeOTP string `json:"challenge_otp,omitempty"`
	// RefundedAmount is the running total credited back for this presentment;
	// BalanceAfter on a refund event is the customer's real balance afterwards.
	RefundedAmount int64     `json:"refunded_amount,omitempty"`
	RefundID       string    `json:"refund_id,omitempty"`
	BalanceAfter   *int64    `json:"balance_after,omitempty"`
	LatencyMs      int64     `json:"latency_ms"`
	CreatedAt      time.Time `json:"created_at"`
}
