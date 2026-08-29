package domain

import (
	"time"

	"github.com/google/uuid"
)

type RiskAction string

const (
	RiskActionAllow  RiskAction = "ALLOW"
	RiskActionStepUp RiskAction = "STEP_UP"
	RiskActionBlock  RiskAction = "BLOCK"
	RiskActionReview RiskAction = "REVIEW"
)

type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "LOW"
	RiskLevelMedium   RiskLevel = "MEDIUM"
	RiskLevelHigh     RiskLevel = "HIGH"
	RiskLevelCritical RiskLevel = "CRITICAL"
)

type FraudSignalType string

const (
	FraudSignalAmountOutOfPattern FraudSignalType = "AMOUNT_OUT_OF_PATTERN"
	FraudSignalNewRecipient       FraudSignalType = "NEW_RECIPIENT"
	FraudSignalUnusualTime        FraudSignalType = "UNUSUAL_TIME"
	FraudSignalHighFrequency      FraudSignalType = "HIGH_FREQUENCY"
	FraudSignalDeviceMismatch     FraudSignalType = "DEVICE_MISMATCH"
	FraudSignalGeoAnomaly         FraudSignalType = "GEO_ANOMALY"
	FraudSignalVelocityBreach     FraudSignalType = "VELOCITY_BREACH"
)

type FraudSignal struct {
	SignalType FraudSignalType `json:"signal_type"`
	Score      float64         `json:"score"`
	Weight     float64         `json:"weight"`
	Message    string          `json:"message"`
}

type FraudEvent struct {
	EventID     uuid.UUID `json:"event_id"`
	PaymentID   uuid.UUID `json:"payment_id"`
	UserID      uuid.UUID `json:"user_id"`
	RiskScore   float64   `json:"risk_score"`
	RiskAction  RiskAction `json:"risk_action"`
	RiskLevel   RiskLevel  `json:"risk_level"`
	RiskReasons []string   `json:"risk_reasons"`
	DeviceID    string     `json:"device_id"`
	IPAddress   string     `json:"ip_address"`
	GeoLocation string     `json:"geo_location"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AccountState struct {
	AccountID       uuid.UUID `json:"account_id"`
	Balance         int64     `json:"balance"`
	Available       int64     `json:"available"`
	Status          string    `json:"status"`
	TxCountToday    int       `json:"tx_count_today"`
	TxAmountToday   int64     `json:"tx_amount_today"`
	AverageTxAmount int64     `json:"average_tx_amount"`
	MaxTxAmount     int64     `json:"max_tx_amount"`
	LastTxTime      time.Time `json:"last_tx_time"`
}

type DeviceInfo struct {
	DeviceID      string `json:"device_id"`
	DeviceType    string `json:"device_type"`
	OS            string `json:"os"`
	AppVersion    string `json:"app_version"`
	IsRooted      bool   `json:"is_rooted"`
	IsEmulator    bool   `json:"is_emulator"`
	TrustedDevice bool   `json:"trusted_device"`
}

type PaymentHistory struct {
	TotalPayments    int     `json:"total_payments"`
	AverageAmount    int64   `json:"average_amount"`
	MaxAmount        int64   `json:"max_amount"`
	UniqueRecipients int     `json:"unique_recipients"`
	RecentPayments   int     `json:"recent_payments_24h"`
	FailedPayments   int     `json:"failed_payments_30d"`
	AvgDailySpend    int64   `json:"avg_daily_spend"`
}

type AnalyzePaymentRequest struct {
	PaymentID  string `json:"payment_id"`
	UserID     string `json:"user_id"`
	AccountID  string `json:"account_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	DeviceID   string `json:"device_id"`
	IPAddress  string `json:"ip_address"`
	RecipientID string `json:"recipient_id"`
}

type AnalyzePaymentFullRequest struct {
	PaymentID   string         `json:"payment_id"`
	UserID      string         `json:"user_id"`
	AccountID   string         `json:"account_id"`
	Amount      int64          `json:"amount"`
	Currency    string         `json:"currency"`
	DeviceID    string         `json:"device_id"`
	IPAddress   string         `json:"ip_address"`
	RecipientID string         `json:"recipient_id"`
	AccountState  *AccountState  `json:"account_state,omitempty"`
	DeviceInfo    *DeviceInfo     `json:"device_info,omitempty"`
	PaymentHistory *PaymentHistory `json:"payment_history,omitempty"`
}

type AnalyzePaymentFullResponse struct {
	Action     RiskAction  `json:"action"`
	RiskScore  float64     `json:"risk_score"`
	RiskLevel  RiskLevel   `json:"risk_level"`
	Signals    []FraudSignal `json:"signals"`
	Reasons    []string    `json:"reasons"`
	PaymentID  string      `json:"payment_id"`
	AnalyzedAt time.Time   `json:"analyzed_at"`
}

type AnalyzeRequest struct {
	PaymentID  string  `json:"payment_id"`
	UserID     string  `json:"user_id"`
	Amount     int64   `json:"amount"`
	Currency   string  `json:"currency"`
	DeviceID   string  `json:"device_id"`
	IPAddress  string  `json:"ip_address"`
}

type AnalyzeResponse struct {
	RiskScore  float64    `json:"risk_score"`
	RiskAction RiskAction `json:"risk_action"`
	Reasons    []string   `json:"reasons"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
