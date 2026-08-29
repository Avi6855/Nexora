package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ReplayStatus string

const (
	ReplayStatusPending   ReplayStatus = "PENDING"
	ReplayStatusRunning   ReplayStatus = "RUNNING"
	ReplayStatusCompleted ReplayStatus = "COMPLETED"
	ReplayStatusFailed    ReplayStatus = "FAILED"
)

type ReplayRequest struct {
	ReplayID    uuid.UUID    `json:"replay_id"`
	StartTime   time.Time    `json:"start_time"`
	EndTime     time.Time    `json:"end_time"`
	EventTypes  []string     `json:"event_types"`
	Status      ReplayStatus `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
	EventsCount int          `json:"events_count"`
}

type ReplayStep struct {
	StepID       int             `json:"step_id"`
	EventType    string          `json:"event_type"`
	Timestamp    time.Time       `json:"timestamp"`
	Payload      json.RawMessage `json:"payload"`
	AppliedState json.RawMessage `json:"applied_state,omitempty"`
}

type HistoricalState struct {
	SnapshotTime time.Time       `json:"snapshot_time"`
	State        json.RawMessage `json:"state"`
	Source       string          `json:"source"`
}

type ReplayResult struct {
	ReplayID        uuid.UUID     `json:"replay_id"`
	OriginalResult  json.RawMessage `json:"original_result"`
	ReplayedResult  json.RawMessage `json:"replayed_result"`
	Differences     []ReplayDifference `json:"differences"`
	Steps           []ReplayStep  `json:"steps"`
	Deterministic   bool          `json:"deterministic"`
	TotalEvents     int           `json:"total_events"`
	ReplayedAt      time.Time     `json:"replayed_at"`
}

type ReplayDifference struct {
	Field      string `json:"field"`
	Original   string `json:"original"`
	Replayed   string `json:"replayed"`
	StepID     int    `json:"step_id"`
}

type ReplayTransactionRequest struct {
	TransactionID string `json:"transaction_id"`
}

type ReplayAccountRequest struct {
	AccountID string `json:"account_id"`
	FromTime  string `json:"from_time"`
	ToTime    string `json:"to_time"`
}

type ReplayPaymentRequest struct {
	PaymentID string `json:"payment_id"`
}

type CreateReplayRequest struct {
	StartTime  string   `json:"start_time"`
	EndTime    string   `json:"end_time"`
	EventTypes []string `json:"event_types"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
