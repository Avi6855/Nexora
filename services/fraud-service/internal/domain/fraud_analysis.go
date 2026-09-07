package domain

import (
	"time"

	"github.com/google/uuid"
)

type FraudAnalysis struct {
	AnalysisID  uuid.UUID  `json:"analysis_id"`
	PaymentID   uuid.UUID  `json:"payment_id"`
	UserID      uuid.UUID  `json:"user_id"`
	AccountID   uuid.UUID  `json:"account_id"`
	RiskScore   float64    `json:"risk_score"`
	RiskAction  RiskAction `json:"risk_action"`
	RiskLevel   RiskLevel  `json:"risk_level"`
	RiskReasons string     `json:"risk_reasons"`
	DeviceID    string     `json:"device_id"`
	IPAddress   string     `json:"ip_address"`
	GeoLocation string     `json:"geo_location"`
	Signals     string     `json:"signals"`
	// Authorisation context captured from the real-time card request so the
	// decision is traceable and future decisions can run velocity/pattern
	// checks over this stored history.
	Amount           int64   `json:"amount"`
	Currency         string  `json:"currency"`
	Merchant         string  `json:"merchant"`
	MerchantCategory string  `json:"merchant_category"`
	MerchantCity     string  `json:"merchant_city"`
	MerchantCountry  string  `json:"merchant_country"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	CreatedAt        time.Time  `json:"created_at"`
}
