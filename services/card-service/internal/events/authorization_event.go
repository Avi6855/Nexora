package events

import "time"

// AuthorizationEvent is the flat, consumer-friendly payload published to
// nexora.card.authorization.*. It carries everything the notification / feed
// pipeline needs (user, account, merchant, amount, live balances) and is the
// contract the notification-service consumer decodes.
type AuthorizationEvent struct {
	AuthorizationID  string    `json:"authorization_id"`
	CardID           string    `json:"card_id"`
	UserID           string    `json:"user_id"`
	AccountID        string    `json:"account_id"`
	Amount           int64     `json:"amount"`
	Currency         string    `json:"currency"`
	Merchant         string    `json:"merchant"`
	MerchantCategory string    `json:"merchant_category"`
	MerchantCity     string    `json:"merchant_city"`
	MerchantCountry  string    `json:"merchant_country"`
	TerminalID       string    `json:"terminal_id"`
	Status           string    `json:"status"` // APPROVED | DECLINED | CAPTURED | VOIDED
	Decision         string    `json:"decision"`
	DeclineReason    string    `json:"decline_reason,omitempty"`
	RiskScore        float64   `json:"risk_score"`
	RiskReasons      string    `json:"risk_reasons,omitempty"`
	ReservationID    string    `json:"reservation_id,omitempty"`
	BalanceAfter     *int64    `json:"balance_after,omitempty"`
	LatencyMs        int64     `json:"latency_ms"`
	CreatedAt        time.Time `json:"created_at"`
}
