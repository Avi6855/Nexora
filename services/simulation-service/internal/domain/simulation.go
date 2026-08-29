package domain

import (
	"time"

	"github.com/google/uuid"
)

type SimulationType string

const (
	SimulationTypeLoad    SimulationType = "LOAD"
	SimulationTypeChaos   SimulationType = "CHAOS"
	SimulationTypeFailure SimulationType = "FAILURE"
)

type SimulationStatus string

const (
	SimulationStatusPending   SimulationStatus = "PENDING"
	SimulationStatusRunning   SimulationStatus = "RUNNING"
	SimulationStatusCompleted SimulationStatus = "COMPLETED"
	SimulationStatusFailed    SimulationStatus = "FAILED"
)

type Simulation struct {
	SimulationID uuid.UUID         `json:"simulation_id"`
	Name         string            `json:"name"`
	Type         SimulationType    `json:"type"`
	Status       SimulationStatus  `json:"status"`
	Parameters   map[string]string `json:"parameters"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
}

type PolicyDecision struct {
	PolicyID string `json:"policy_id"`
	RuleID   string `json:"rule_id"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
}

type DigitalTwin struct {
	AccountID         uuid.UUID `json:"account_id"`
	UserID            uuid.UUID `json:"user_id"`
	Balance           int64     `json:"balance"`
	Available         int64     `json:"available"`
	Reserved          int64     `json:"reserved"`
	Currency          string    `json:"currency"`
	Status            string    `json:"status"`
	SnapshotTime      time.Time `json:"snapshot_time"`
	RecentTransactions int     `json:"recent_transactions"`
	AverageTxAmount   int64     `json:"average_tx_amount"`
	MaxTxAmount       int64     `json:"max_tx_amount"`
	TxCountToday      int       `json:"tx_count_today"`
}

type SimulatePaymentRequest struct {
	SourceAccountID string `json:"source_account_id"`
	Destination     string `json:"destination"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
}

type SimulateTransferRequest struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
}

type SimulationResult struct {
	WouldSucceed        bool             `json:"would_succeed"`
	PredictedBalance    int64            `json:"predicted_balance"`
	PredictedAvailable  int64            `json:"predicted_available"`
	RiskScore           float64          `json:"risk_score"`
	PolicyDecisions     []PolicyDecision `json:"policy_decisions"`
	Reasons             []string         `json:"reasons"`
	SimulatedAt         time.Time        `json:"simulated_at"`
}

type CounterfactualPayment struct {
	Simulated       bool   `json:"simulated"`
	WouldSucceed    bool   `json:"would_succeed"`
	SourceBalance   int64  `json:"source_balance"`
	SourceAvailable int64  `json:"source_available"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	PredictedResult string `json:"predicted_result"`
	Reason          string `json:"reason,omitempty"`
}

type CreateSimulationRequest struct {
	Name       string            `json:"name"`
	Type       SimulationType    `json:"type"`
	Parameters map[string]string `json:"parameters"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
