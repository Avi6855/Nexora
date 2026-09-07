package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type PolicyType string

const (
	PolicyTypeSpendingLimit  PolicyType = "SPENDING_LIMIT"
	PolicyTypeVelocity       PolicyType = "VELOCITY"
	PolicyTypeGeographic     PolicyType = "GEOGRAPHIC"
	PolicyTypeDevice         PolicyType = "DEVICE"
	PolicyTypeTimeBased      PolicyType = "TIME_BASED"
	PolicyTypeAmountPattern  PolicyType = "AMOUNT_PATTERN"
	PolicyTypeRecipientRisk  PolicyType = "RECIPIENT_RISK"
)

type PolicyScope string

const (
	PolicyScopeGlobal  PolicyScope = "GLOBAL"
	PolicyScopeUser    PolicyScope = "USER"
	PolicyScopeAccount PolicyScope = "ACCOUNT"
	PolicyScopePayment PolicyScope = "PAYMENT"
)

type PolicyStatus string

const (
	PolicyStatusDraft    PolicyStatus = "DRAFT"
	PolicyStatusTesting  PolicyStatus = "TESTING"
	PolicyStatusShadow   PolicyStatus = "SHADOW"
	PolicyStatusActive   PolicyStatus = "ACTIVE"
	PolicyStatusDisabled PolicyStatus = "DISABLED"
)

type PolicyDecisionAction string

const (
	PolicyDecisionAllow  PolicyDecisionAction = "ALLOW"
	PolicyDecisionStepUp PolicyDecisionAction = "STEP_UP"
	PolicyDecisionBlock  PolicyDecisionAction = "BLOCK"
)

type Policy struct {
	PolicyID    uuid.UUID    `json:"policy_id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	PolicyType  PolicyType   `json:"policy_type"`
	Scope       PolicyScope  `json:"scope"`
	Status      PolicyStatus `json:"status"`
	Rules       []PolicyRule `json:"rules"`
	Enabled     bool         `json:"enabled"`
	Version     int          `json:"version"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type PolicyRule struct {
	RuleID      string            `json:"rule_id"`
	Name        string            `json:"name"`
	Condition   string            `json:"condition"`
	Action      PolicyDecisionAction `json:"action"`
	Priority    int               `json:"priority"`
	Parameters  map[string]string `json:"parameters"`
	Enabled     bool              `json:"enabled"`
}

type PaymentContext struct {
	PaymentID   uuid.UUID `json:"payment_id"`
	AccountID   uuid.UUID `json:"account_id"`
	UserID      uuid.UUID `json:"user_id"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	DeviceID    string    `json:"device_id"`
	IPAddress   string    `json:"ip_address"`
	RecipientID string    `json:"recipient_id"`
	Timestamp   time.Time `json:"timestamp"`
}

type PolicyDecision struct {
	PolicyID     uuid.UUID            `json:"policy_id"`
	PolicyName   string               `json:"policy_name"`
	Action       PolicyDecisionAction `json:"action"`
	MatchedRules []MatchedRule        `json:"matched_rules"`
	Reasons      []string             `json:"reasons"`
	EvaluatedAt  time.Time            `json:"evaluated_at"`
}

type MatchedRule struct {
	RuleID   string `json:"rule_id"`
	RuleName string `json:"rule_name"`
	Action   PolicyDecisionAction `json:"action"`
	Reason   string `json:"reason"`
}

type EvaluatePaymentRequest struct {
	PaymentID   string `json:"payment_id"`
	AccountID   string `json:"account_id"`
	UserID      string `json:"user_id"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	DeviceID    string `json:"device_id"`
	IPAddress   string `json:"ip_address"`
	RecipientID string `json:"recipient_id"`
}

type EvaluatePaymentResponse struct {
	FinalDecision PolicyDecisionAction `json:"final_decision"`
	PolicyDecisions []PolicyDecision    `json:"policy_decisions"`
	MatchedRules  []MatchedRule        `json:"matched_rules"`
	Reasons       []string             `json:"reasons"`
}

type ShadowCompareRequest struct {
	PaymentID   string `json:"payment_id"`
	AccountID   string `json:"account_id"`
	UserID      string `json:"user_id"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	DeviceID    string `json:"device_id"`
	IPAddress   string `json:"ip_address"`
	RecipientID string `json:"recipient_id"`
	PolicyID    string `json:"policy_id"`
}

type ShadowCompareResponse struct {
	CurrentDecision  PolicyDecision `json:"current_decision"`
	ShadowDecision   PolicyDecision `json:"shadow_decision"`
	Match            bool           `json:"match"`
	Differences      []string       `json:"differences"`
}

type CreatePolicyRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	PolicyType  PolicyType   `json:"policy_type"`
	Scope       PolicyScope  `json:"scope"`
	Rules       []PolicyRule `json:"rules"`
}

// ── Feature flags: progressive rollout + automatic rollback ─────────────────

// FeatureFlag is a progressively-rolled-out capability switch. Assignment is
// deterministic: hash(flag_id, user_id) < rollout_pct, so the same user is
// always in the same cohort across replicas and restarts.
type FeatureFlag struct {
	FlagID      uuid.UUID `json:"flag_id"`
	Key         string    `json:"key"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	// RolloutPct: 0 = off for everyone, 100 = fully on.
	RolloutPct int       `json:"rollout_pct"`
	// CohortConstraint (optional) like "app_version>=2.1" or "country=UK".
	CohortConstraint string `json:"cohort_constraint,omitempty"`
	// Auto-rollback guards: measured by the flag's own metrics window.
	BaselineErrorPct  float64   `json:"baseline_error_pct"`
	MaxErrorPct       float64   `json:"max_error_pct"`
	MaxLatencyMs      float64   `json:"max_latency_ms"`
	ErrorPct          float64   `json:"error_pct"`
	LatencyMs         float64   `json:"latency_ms"`
	RolledBack        bool      `json:"rolled_back"`
	RollbackReason    string    `json:"rollback_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

var (
	ErrFlagNotFound = errorsNew("feature flag not found")
	ErrInvalidFlag  = errorsNew("flag key and valid rollout_pct (0-100) are required")
)

// errorsNew avoids importing errors twice in this file's const block style.
func errorsNew(msg string) error { return fmt.Errorf("%s", msg) }

// CreateFlagRequest is the create/update payload.
type CreateFlagRequest struct {
	Key              string  `json:"key"`
	Description      string  `json:"description"`
	RolloutPct       int     `json:"rollout_pct"`
	CohortConstraint string  `json:"cohort_constraint,omitempty"`
	MaxErrorPct      float64 `json:"max_error_pct,omitempty"`
	MaxLatencyMs     float64 `json:"max_latency_ms,omitempty"`
}

// RecordFlagMetricRequest reports the flag's live error rate / latency.
type RecordFlagMetricRequest struct {
	ErrorPct  float64 `json:"error_pct"`
	LatencyMs float64 `json:"latency_ms"`
}

// EvaluateFlagResponse answers "is this user in the flag's cohort?".
type EvaluateFlagResponse struct {
	Key      string `json:"key"`
	Enabled  bool   `json:"enabled"`
	RolloutPct int  `json:"rollout_pct"`
	Reason   string `json:"reason"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
