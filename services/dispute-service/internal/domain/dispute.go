package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── Case status (open vs terminal) ──────────────────────────────────────────

type CaseStatus string

const (
	CaseStatusOpen     CaseStatus = "OPEN"
	CaseStatusResolved CaseStatus = "RESOLVED"
)

// ── Lifecycle stage: the long-running workflow state machine ────────────────

type LifecycleStage string

const (
	// StageSubmitted — case created, eligibility not yet assessed.
	StageSubmitted LifecycleStage = "SUBMITTED"
	// StageEligibility — checking scheme rules (time window, category).
	StageEligibility LifecycleStage = "ELIGIBILITY"
	// StageAwaitingEvidence — user documents requested/in progress.
	StageAwaitingEvidence LifecycleStage = "AWAITING_EVIDENCE"
	// StageMerchantResponse — evidence sent to merchant/scheme, waiting.
	StageMerchantResponse LifecycleStage = "MERCHANT_RESPONSE"
	// StageUnderReview — analyst/scheme review.
	StageUnderReview LifecycleStage = "UNDER_REVIEW"
	// StageResolved — terminal (with a Resolution).
	StageResolved LifecycleStage = "RESOLVED"
)

// ValidLifecycleTransitions encodes the workflow. Timeouts advance cases
// forward; the only backward edge is re-requesting evidence from review.
var ValidLifecycleTransitions = map[LifecycleStage][]LifecycleStage{
	StageSubmitted:        {StageEligibility},
	StageEligibility:      {StageAwaitingEvidence, StageResolved},
	StageAwaitingEvidence: {StageMerchantResponse, StageResolved},
	StageMerchantResponse: {StageUnderReview, StageResolved},
	StageUnderReview:      {StageAwaitingEvidence, StageResolved},
}

func CanTransition(from, to LifecycleStage) bool {
	for _, t := range ValidLifecycleTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// ── Reason + eligibility + resolution ───────────────────────────────────────

type DisputeReason string

const (
	ReasonFraud          DisputeReason = "FRAUD"           // didn't authorise it
	ReasonNotReceived    DisputeReason = "NOT_RECEIVED"    // paid, goods never arrived
	ReasonNotAsDescribed DisputeReason = "NOT_AS_DESCRIBED"
	ReasonDuplicate      DisputeReason = "DUPLICATE"
	ReasonIncorrectAmount DisputeReason = "INCORRECT_AMOUNT"
	ReasonCancelled      DisputeReason = "CANCELLED_RECURRING" // subscription kept charging
)

// disputeCategoryRules is the eligibility engine core: which reasons are
// scheme-disputable per transaction category, and the evidence deadline.
type categoryRule struct {
	eligible           bool
	evidenceDeadline   time.Duration // how long the user has to send evidence
	merchantDeadline   time.Duration // how long the merchant has to respond
	provisionalCredit  bool          // card-scheme rules allow temp credit
}

var disputeCategoryRules = map[string]categoryRule{
	"CARD_PAYMENT":    {true, 14 * 24 * time.Hour, 45 * 24 * time.Hour, true},
	"CARD_REFUND":     {false, 0, 0, false}, // refunds aren't disputes
	"BANK_TRANSFER":   {false, 0, 0, false}, // APP scams go via scam intelligence, not chargebacks
	"POT_TRANSFER":    {false, 0, 0, false},
	"DIRECT_DEBIT":    {true, 7 * 24 * time.Hour, 30 * 24 * time.Hour, false}, // DD guarantee
	"CASH_WITHDRAWAL": {false, 0, 0, false}, // ATM disputes need police report, out of scope
}

// Eligibility is the outcome of the eligibility engine.
type Eligibility struct {
	Eligible            bool           `json:"eligible"`
	Reason              string         `json:"reason,omitempty"`
	EvidenceDeadline    time.Time      `json:"evidence_deadline"`
	MerchantDeadline    time.Time      `json:"merchant_deadline"`
	ProvisionalCredit   bool           `json:"provisional_credit"`
}

// AssessEligibility applies scheme-style rules: category must be disputable,
// the transaction must be recent, and the amount must be non-zero.
func AssessEligibility(category string, txnAmount int64, txnAt, now time.Time) Eligibility {
	rule, ok := disputeCategoryRules[category]
	if !ok {
		// Unknown category: treat like a card payment (the common case) but
		// flag it in the reason.
		rule = categoryRule{eligible: true, evidenceDeadline: 14 * 24 * time.Hour, merchantDeadline: 45 * 24 * time.Hour, provisionalCredit: true}
	}
	if !rule.eligible {
		return Eligibility{Eligible: false, Reason: fmt.Sprintf("%s transactions cannot be raised as disputes (scheme rules)", category), EvidenceDeadline: time.Time{}, MerchantDeadline: time.Time{}}
	}
	if txnAmount <= 0 {
		return Eligibility{Eligible: false, Reason: "transaction amount must be positive"}
	}
	if age := now.Sub(txnAt); age > 120*24*time.Hour {
		return Eligibility{Eligible: false, Reason: "transaction is older than the 120-day chargeback window"}
	}
	return Eligibility{
		Eligible:          true,
		Reason:            "eligible",
		EvidenceDeadline:  txnAt.Add(rule.evidenceDeadline),
		MerchantDeadline:  txnAt.Add(rule.merchantDeadline),
		ProvisionalCredit: rule.provisionalCredit,
	}
}

// ── Resolution ──────────────────────────────────────────────────────────────

type Resolution string

const (
	ResolutionRefunded        Resolution = "REFUNDED"          // customer won, money returned
	ResolutionRejected        Resolution = "REJECTED"          // merchant/scheme ruled against
	ResolutionMerchantRefund  Resolution = "MERCHANT_REFUND"   // merchant settled directly
	ResolutionWithdrawn       Resolution = "WITHDRAWN"         // user dropped it
)

// ── Aggregates ──────────────────────────────────────────────────────────────

type Evidence struct {
	EvidenceID uuid.UUID `json:"evidence_id"`
	CaseID     uuid.UUID `json:"case_id"`
	// RECEIPT | CORRESPONDENCE | PHOTO | DELIVERY_NOTE | OTHER
	EvidenceType string    `json:"evidence_type"`
	Filename     string    `json:"filename"`
	Content      string    `json:"content,omitempty"` // base64 (small docs at demo scale)
	UploadedBy   uuid.UUID `json:"uploaded_by"`
	CreatedAt    time.Time `json:"created_at"`
}

type CaseEvent struct {
	EventID   uuid.UUID `json:"event_id"`
	CaseID    uuid.UUID `json:"case_id"`
	EventType string    `json:"event_type"`
	Actor     string    `json:"actor"` // "user:<id>" | "system" | "analyst"
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// DisputeCase is the case aggregate.
type DisputeCase struct {
	CaseID            uuid.UUID      `json:"case_id"`
	UserID            uuid.UUID      `json:"user_id"`
	AccountID         uuid.UUID      `json:"account_id"`
	EntryID           uuid.UUID      `json:"entry_id"`
	TransactionID     uuid.UUID      `json:"transaction_id"`
	Amount            int64          `json:"amount"`
	Currency          string         `json:"currency"`
	Merchant          string         `json:"merchant"`
	Category          string         `json:"category"`
	Reason            DisputeReason  `json:"reason"`
	Description       string         `json:"description,omitempty"`
	Status            CaseStatus     `json:"status"`
	Stage             LifecycleStage `json:"stage"`
	Eligibility       Eligibility    `json:"eligibility"`
	Resolution        Resolution     `json:"resolution,omitempty"`
	ProvisionalCredit bool           `json:"provisional_credit"`
	Deadline          *time.Time     `json:"deadline,omitempty"` // current stage deadline
	ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

var (
	ErrCaseNotFound      = errors.New("dispute case not found")
	ErrCaseNotOpen       = errors.New("dispute case is already resolved")
	ErrInvalidTransition = errors.New("invalid lifecycle transition")
	ErrNotOwner          = errors.New("dispute case does not belong to caller")
	ErrDuplicateDispute  = errors.New("a dispute already exists for this transaction")
	ErrInvalidEvidence   = errors.New("evidence type or content is required")
)

// TransitionTo moves the case forward through the state machine.
func (c *DisputeCase) TransitionTo(next LifecycleStage, now time.Time) error {
	if c.Status != CaseStatusOpen {
		return ErrCaseNotOpen
	}
	if !CanTransition(c.Stage, next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, c.Stage, next)
	}
	c.Stage = next
	c.UpdatedAt = now
	return nil
}

// CreateDisputeRequest is the app's "report a problem" payload.
type CreateDisputeRequest struct {
	EntryID       string `json:"entry_id"`
	Reason        string `json:"reason"`
	Description   string `json:"description,omitempty"`
}

// CreateDisputeResponse returns the created case (post-eligibility).
type CreateDisputeResponse struct {
	Case *DisputeCase `json:"case"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
