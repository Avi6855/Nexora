package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ── Time-Travel Compliance Engine ───────────────────────────────────────────
//
// Auditors must be able to ask: "why was THIS transaction allowed on 12 June
// 2024?" — and get the decision reconstructed from the policy versions and
// customer state AS THEY WERE at that instant. This is not audit logging; it
// is historical decision reconstruction. Correctness depends on two
// non-negotiable invariants:
//
//  1. Every policy change is an append-only version with [valid_from,
//     valid_to) — old versions are never mutated.
//  2. Customer state is captured at decision time (snapshot), so the replay
//     sees exactly what the engine saw.

var (
	ErrNoPolicyVersionAtTime = errors.New("no policy version was in force at the requested time")
	ErrNoStateSnapshot       = errors.New("no customer state snapshot covers the requested time")
)

// PolicyVersion is an immutable, time-bounded version of a policy.
type PolicyVersion struct {
	PolicyID  uuid.UUID    `json:"policy_id"`
	Version   int          `json:"version"`
	Status    PolicyStatus `json:"status"`
	Rules     []PolicyRule `json:"rules"`
	Name      string       `json:"name"`
	Enabled   bool         `json:"enabled"`
	ValidFrom time.Time    `json:"valid_from"`
	ValidTo   time.Time    `json:"valid_to"` // zero value = still current
}

// Covers reports whether this version governed at instant t.
func (v PolicyVersion) Covers(t time.Time) bool {
	if t.Before(v.ValidFrom) {
		return false
	}
	return v.ValidTo.IsZero() || t.Before(v.ValidTo)
}

// CustomerStateSnapshot captures the risk-relevant customer facts AT decision
// time. Policies that depend on customer attributes (KYC tier, device trust,
// prior flags) evaluate against the snapshot, never against "current" state.
type CustomerStateSnapshot struct {
	SnapshotID     uuid.UUID `json:"snapshot_id"`
	UserID         uuid.UUID `json:"user_id"`
	CapturedAt     time.Time `json:"captured_at"`
	KYCTier        int       `json:"kyc_tier"`
	DeviceKnown    bool      `json:"device_known"`
	AccountAgeDays int       `json:"account_age_days"`
	FlaggedRisk    bool      `json:"flagged_risk"`
}

// Covers reports whether this snapshot is the one that would have been read
// at instant t (latest snapshot captured at or before t).
func (s CustomerStateSnapshot) Covers(t time.Time) bool {
	return !s.CapturedAt.After(t)
}

// HistoricalEvaluation is the reconstructed decision: which policy versions
// governed, which rules matched, what the outcome would be — with a
// deterministic hash binding the whole chain together.
type HistoricalEvaluation struct {
	ReconstructedAt time.Time              `json:"reconstructed_at"`
	AsOf            time.Time              `json:"as_of"`
	PaymentID       string                 `json:"payment_id"`
	PolicyVersions  []PolicyVersion        `json:"policy_versions"`
	Snapshot        *CustomerStateSnapshot `json:"customer_state,omitempty"`
	FinalDecision   PolicyDecisionAction   `json:"final_decision"`
	MatchedRules    []MatchedRule          `json:"matched_rules"`
	Reasons         []string               `json:"reasons"`
	ReplayHash      string                 `json:"replay_hash"`
	// Deterministic marks whether every input needed for an exact replay was
	// available. Partial reconstructions are still useful but must be
	// labelled honestly.
	Deterministic bool `json:"deterministic"`
}

// RecordHistoricalDecisionRequest archives a decision at the moment it is
// made, binding the policy versions used to the outcome.
type RecordHistoricalDecisionRequest struct {
	PaymentID      string               `json:"payment_id"`
	AccountID      string               `json:"account_id"`
	UserID         string               `json:"user_id"`
	Amount         int64                `json:"amount"`
	Currency       string               `json:"currency"`
	DeviceID       string               `json:"device_id"`
	IPAddress      string               `json:"ip_address"`
	RecipientID    string               `json:"recipient_id"`
	DecidedAt      time.Time            `json:"decided_at"`
	FinalDecision  PolicyDecisionAction `json:"final_decision"`
	PolicyVersions []uuid.UUID          `json:"policy_versions"`
	SnapshotID     uuid.UUID            `json:"snapshot_id"`
}

// HistoricalDecisionRecord is the stored record (what EvaluatePayment writes
// as a side effect so future reconstruction is possible).
type HistoricalDecisionRecord struct {
	RecordID   uuid.UUID                       `json:"record_id"`
	Request    RecordHistoricalDecisionRequest `json:"request"`
	ReplayHash string                          `json:"replay_hash"`
	CreatedAt  time.Time                       `json:"created_at"`
}

// TimeTravelRequest asks "reconstruct the decision for this payment as of
// this instant".
type TimeTravelRequest struct {
	PaymentID string    `json:"payment_id"`
	AsOf      time.Time `json:"as_of"`
}

// TimeTravelResponse answers it.
type TimeTravelResponse struct {
	Evaluation HistoricalEvaluation `json:"evaluation"`
}

// CaptureSnapshotRequest records customer state at a point in time.
type CaptureSnapshotRequest struct {
	UserID         string    `json:"user_id"`
	KYCTier        int       `json:"kyc_tier"`
	DeviceKnown    bool      `json:"device_known"`
	AccountAgeDays int       `json:"account_age_days"`
	FlaggedRisk    bool      `json:"flagged_risk"`
	CapturedAt     time.Time `json:"captured_at,omitempty"` // zero = now
}
