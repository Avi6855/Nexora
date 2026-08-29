package domain

import (
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

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
