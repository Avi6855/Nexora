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

// TimeTravelRequest asks "what was this account's state at instant T?". The
// reconstruction runs over the real append-only ledger (balance_after per
// entry), so the answer is authoritative, not simulated.
type TimeTravelRequest struct {
	AccountID string `json:"account_id"`
	// AtTime is RFC3339; empty or zero means "now" (equivalent to the live
	// balance, useful as a self-check).
	AtTime string `json:"at_time"`
}

type TimeTravelStep struct {
	EntryID      string    `json:"entry_id"`
	EntryType    string    `json:"entry_type"`
	Description  string    `json:"description"`
	Amount       int64     `json:"amount"`
	Direction    string    `json:"direction"`
	BalanceAfter int64     `json:"balance_after"`
	BookedAt     time.Time `json:"booked_at"`
}

type TimeTravelResult struct {
	AccountID   uuid.UUID `json:"account_id"`
	SnapshotAt  time.Time `json:"snapshot_at"`
	Balance     int64     `json:"balance"`
	EntriesSeen int       `json:"entries_seen"`
	// LedgerIsLive is true when no entries were booked after the snapshot, so
	// the reconstructed balance equals the current live balance.
	LedgerIsLive bool `json:"ledger_is_live"`
	// Divergence is set when the backwards read (stored balance_after) and the
	// forward replay (signed sum) disagree — the time-travel debugging signal.
	Divergence *ReplayDifference `json:"divergence,omitempty"`
	Steps      []TimeTravelStep  `json:"steps"`
	ReplayedAt time.Time         `json:"replayed_at"`
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
