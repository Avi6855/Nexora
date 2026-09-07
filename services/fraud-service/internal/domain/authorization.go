package domain

import "time"

// CardAuthorizationDecision is the outcome the card platform can act on.
type CardAuthorizationDecision string

const (
	// CardAuthApprove mirrors RiskActionAllow but uses payment-industry
	// vocabulary in the response to the card platform.
	CardAuthApprove CardAuthorizationDecision = "APPROVE"
	CardAuthReview  CardAuthorizationDecision = "REVIEW"
	CardAuthDecline CardAuthorizationDecision = "DECLINE"
)

// EvaluateAuthorizationRequest carries the *live* authorisation context. It is
// the raw merchant presentment plus real account state pulled from the ledger
// (spent today) and card row (limits) by the card service — nothing here is
// synthesised; the decision itself is computed over stored fraud history.
type EvaluateAuthorizationRequest struct {
	AuthorizationID string  `json:"authorization_id"`
	UserID          string  `json:"user_id"`
	AccountID       string  `json:"account_id"`
	CardID          string  `json:"card_id"`
	Amount          int64   `json:"amount"`
	Currency        string  `json:"currency"`
	Merchant        string  `json:"merchant"`
	MerchantCategory string `json:"merchant_category"`
	MerchantCity    string  `json:"merchant_city"`
	MerchantCountry string  `json:"merchant_country"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	TerminalID      string  `json:"terminal_id"`
	DeviceID        string  `json:"device_id"`
	DailyLimit      int64   `json:"daily_limit"`
	MonthlyLimit    int64   `json:"monthly_limit"`
	SpentToday      int64   `json:"spent_today"`
}

type EvaluateAuthorizationResponse struct {
	AuthorizationID string                  `json:"authorization_id"`
	Decision        CardAuthorizationDecision `json:"decision"`
	RiskScore       float64                 `json:"risk_score"`
	RiskLevel       RiskLevel               `json:"risk_level"`
	Signals         []FraudSignal           `json:"signals"`
	Reasons         []string                `json:"reasons"`
	EvaluatedAt     time.Time               `json:"evaluated_at"`
}
