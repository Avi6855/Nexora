package domain

import (
	"errors"
	"fmt"
	"time"
)

// ── Orchestrated dispute kinds (extends the CARD lifecycle) ────────────────
// CARD keeps the existing chargeback lifecycle; TRANSFER / DIRECT_DEBIT /
// CASH add per-kind scheme rules on top.

type OrchestratedKind string

const (
	OrchestratedKindCard        OrchestratedKind = "CARD"
	OrchestratedKindTransfer    OrchestratedKind = "TRANSFER"
	OrchestratedKindDirectDebit OrchestratedKind = "DIRECT_DEBIT"
	OrchestratedKindCash        OrchestratedKind = "CASH"
)

// OrchestratedStatus tracks the orchestrated case. RETURNED is the
// direct-debit indemnity terminal state (money returned immediately without
// a review cycle); RESOLVED is the terminal state for all other kinds.
type OrchestratedStatus string

const (
	OrchestratedStatusOpen             OrchestratedStatus = "OPEN"
	OrchestratedStatusAwaitingEvidence OrchestratedStatus = "AWAITING_EVIDENCE"
	OrchestratedStatusUnderReview      OrchestratedStatus = "UNDER_REVIEW"
	OrchestratedStatusResolved         OrchestratedStatus = "RESOLVED"
	OrchestratedStatusReturned         OrchestratedStatus = "RETURNED"
)

// kindSchemeRule encodes per-kind scheme rules.
type kindSchemeRule struct {
	eligibilityWindow time.Duration
	evidenceDeadline  time.Duration
	reviewWindow      time.Duration
	// recallWindow applies to transfers: sender-bank recall requests inside
	// this window follow the fast recall path.
	recallWindow time.Duration
	// requiredEvidence lists evidence types that must appear at least once
	// before the case can move to review (empty = no hard requirement).
	requiredEvidence []string
	// allowedEvidence lists acceptable evidence types for the kind.
	allowedEvidence []string
	// immediateReturn marks indemnity-style kinds (direct debit): an
	// eligible claim returns money immediately, no review cycle.
	immediateReturn bool
	// provisionalCredit mirrors the card lifecycle flag per kind.
	provisionalCredit bool
	// description is the human-readable scheme summary.
	description string
}

var orchestratedKindRules = map[OrchestratedKind]kindSchemeRule{
	OrchestratedKindCard: {
		eligibilityWindow: 120 * 24 * time.Hour,
		evidenceDeadline:  14 * 24 * time.Hour,
		reviewWindow:      45 * 24 * time.Hour,
		requiredEvidence:  []string{},
		allowedEvidence:   []string{"RECEIPT", "CORRESPONDENCE", "PHOTO", "DELIVERY_NOTE", "OTHER"},
		provisionalCredit: true,
		description:       "card scheme chargeback rules",
	},
	OrchestratedKindTransfer: {
		eligibilityWindow: 60 * 24 * time.Hour,
		evidenceDeadline:  10 * 24 * time.Hour,
		reviewWindow:      20 * 24 * time.Hour,
		recallWindow:      10 * 24 * time.Hour,
		requiredEvidence:  []string{},
		allowedEvidence:   []string{"TRANSFER_CONFIRMATION", "CORRESPONDENCE", "BANK_STATEMENT", "OTHER"},
		provisionalCredit: false,
		description:       "sender-bank recall windows: fast recall inside 10 days, late recall up to 60 days",
	},
	OrchestratedKindDirectDebit: {
		eligibilityWindow: 395 * 24 * time.Hour, // ~13 months indemnity window
		evidenceDeadline:  0,
		reviewWindow:      0,
		requiredEvidence:  []string{},
		allowedEvidence:   []string{"INDEMNITY_CLAIM", "BANK_STATEMENT", "CORRESPONDENCE", "OTHER"},
		immediateReturn:   true,
		provisionalCredit: false,
		description:       "direct-debit indemnity: immediate return, no-evidence claim",
	},
	OrchestratedKindCash: {
		eligibilityWindow: 30 * 24 * time.Hour,
		evidenceDeadline:  7 * 24 * time.Hour,
		reviewWindow:      30 * 24 * time.Hour,
		requiredEvidence:  []string{"ATM_RECEIPT"},
		allowedEvidence:   []string{"ATM_RECEIPT", "PHOTO", "POLICE_REPORT"},
		provisionalCredit: false,
		description:       "ATM disputes require ATM receipt plus supporting evidence",
	},
}

// OrchestratedEligibility is the per-kind eligibility outcome.
type OrchestratedEligibility struct {
	Eligible          bool   `json:"eligible"`
	Reason            string `json:"reason,omitempty"`
	ImmediateReturn   bool   `json:"immediate_return"`
	ProvisionalCredit bool   `json:"provisional_credit"`
}

// AssessOrchestratedEligibility applies per-kind scheme rules.
func AssessOrchestratedEligibility(kind OrchestratedKind, amountMinor int64, txnAt, now time.Time) OrchestratedEligibility {
	rule, ok := orchestratedKindRules[kind]
	if !ok {
		return OrchestratedEligibility{Eligible: false, Reason: fmt.Sprintf("unknown dispute kind %q (use CARD, TRANSFER, DIRECT_DEBIT, CASH)", string(kind))}
	}
	if amountMinor <= 0 {
		return OrchestratedEligibility{Eligible: false, Reason: "transaction amount must be positive"}
	}
	age := now.Sub(txnAt)
	if age < 0 {
		return OrchestratedEligibility{Eligible: false, Reason: "transaction is in the future"}
	}
	if age > rule.eligibilityWindow {
		return OrchestratedEligibility{Eligible: false, Reason: fmt.Sprintf("%s disputes must be raised within %s of the transaction (%s)", string(kind), rule.eligibilityWindow, rule.description)}
	}
	switch kind {
	case OrchestratedKindTransfer:
		if age <= rule.recallWindow {
			return OrchestratedEligibility{Eligible: true, Reason: "eligible: fast sender-bank recall path", ProvisionalCredit: rule.provisionalCredit}
		}
		return OrchestratedEligibility{Eligible: true, Reason: "eligible: late sender-bank recall path", ProvisionalCredit: rule.provisionalCredit}
	case OrchestratedKindDirectDebit:
		return OrchestratedEligibility{Eligible: true, Reason: "eligible: indemnity-style immediate return", ImmediateReturn: true}
	case OrchestratedKindCash:
		return OrchestratedEligibility{Eligible: true, Reason: "eligible: ATM evidence required (ATM_RECEIPT)", ProvisionalCredit: false}
	default:
		return OrchestratedEligibility{Eligible: true, Reason: "eligible", ProvisionalCredit: rule.provisionalCredit}
	}
}

// ValidateOrchestratedEvidence reports whether evidenceType is acceptable
// for the kind. Cash is strict (ATM evidence only); other kinds accept
// their allow-list.
func ValidateOrchestratedEvidence(kind OrchestratedKind, evidenceType string) bool {
	rule, ok := orchestratedKindRules[kind]
	if !ok || evidenceType == "" {
		return false
	}
	for _, a := range rule.allowedEvidence {
		if a == evidenceType {
			return true
		}
	}
	return false
}

// HasRequiredEvidence reports whether the collected evidence types satisfy
// the kind's hard requirements (cash needs at least one ATM_RECEIPT).
func HasRequiredEvidence(kind OrchestratedKind, got []string) bool {
	rule, ok := orchestratedKindRules[kind]
	if !ok {
		return false
	}
	if len(rule.requiredEvidence) == 0 {
		return true
	}
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, r := range rule.requiredEvidence {
		if !have[r] {
			return false
		}
	}
	return true
}

// ── Ledger-adjustment proposal (never auto-posts) ──────────────────────────
// The proposal is returned for ledger-service to post; the dispute service
// itself never books money for orchestrated cases.

const (
	LegDebit  = "DEBIT"
	LegCredit = "CREDIT"
)

// AdjustmentLeg is one side of a balanced adjustment.
type AdjustmentLeg struct {
	AccountID   string `json:"account_id"`
	Direction   string `json:"direction"` // DEBIT | CREDIT
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// AdjustmentProposal carries balanced debit/credit legs for ledger-service.
type AdjustmentProposal struct {
	ProposalID string          `json:"proposal_id"`
	DisputeID  string          `json:"dispute_id"`
	Reason     string          `json:"reason"`
	Legs       []AdjustmentLeg `json:"legs"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Balanced reports whether total debits equal total credits (and non-zero).
func (p *AdjustmentProposal) Balanced() bool {
	if p == nil || len(p.Legs) == 0 {
		return false
	}
	var debits, credits int64
	for _, l := range p.Legs {
		if l.AmountMinor <= 0 {
			return false
		}
		switch l.Direction {
		case LegDebit:
			debits += l.AmountMinor
		case LegCredit:
			credits += l.AmountMinor
		default:
			return false
		}
	}
	return debits > 0 && debits == credits
}

// ProposeAdjustment builds a balanced refund proposal: debit the scheme
// settlement suspense account, credit the customer account. It never posts.
func ProposeAdjustment(proposalID, disputeID, accountID string, amountMinor int64, currency, reason string, now time.Time) *AdjustmentProposal {
	return &AdjustmentProposal{
		ProposalID: proposalID,
		DisputeID:  disputeID,
		Reason:     reason,
		Legs: []AdjustmentLeg{
			{AccountID: "scheme-settlement-suspense", Direction: LegDebit, AmountMinor: amountMinor, Currency: currency},
			{AccountID: accountID, Direction: LegCredit, AmountMinor: amountMinor, Currency: currency},
		},
		CreatedAt: now,
	}
}

// ── Orchestrated dispute aggregate ─────────────────────────────────────────

type OrchestratedEvidence struct {
	EvidenceID string    `json:"evidence_id"`
	Type       string    `json:"evidence_type"`
	Filename   string    `json:"filename"`
	AddedAt    time.Time `json:"added_at"`
}

// OrchestratedDispute is the per-kind dispute aggregate with status
// tracking and an optional ledger-adjustment proposal.
type OrchestratedDispute struct {
	DisputeID  string                 `json:"dispute_id"`
	Kind       OrchestratedKind       `json:"kind"`
	UserID     string                 `json:"user_id"`
	AccountID  string                 `json:"account_id"`
	Amount     int64                  `json:"amount_minor"`
	Currency   string                 `json:"currency"`
	TxnAt      time.Time              `json:"txn_at"`
	Status     OrchestratedStatus     `json:"status"`
	Evidence   []OrchestratedEvidence `json:"evidence"`
	Resolution string                 `json:"resolution,omitempty"`
	Adjustment *AdjustmentProposal    `json:"adjustment,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

var (
	ErrOrchestratedNotFound        = errors.New("orchestrated dispute not found")
	ErrOrchestratedInvalidKind     = errors.New("invalid dispute kind")
	ErrOrchestratedIneligible      = errors.New("dispute is not eligible")
	ErrOrchestratedInvalidStatus   = errors.New("invalid status transition")
	ErrOrchestratedEvidence        = errors.New("evidence type is not acceptable for this dispute kind")
	ErrOrchestratedMissingEvidence = errors.New("required evidence missing for this dispute kind")
	ErrOrchestratedNoAdjustment    = errors.New("no ledger-adjustment proposal for this dispute yet")
)

// ValidOrchestratedTransitions encodes status tracking. Direct-debit
// indemnity jumps OPEN -> RETURNED; every other kind flows through evidence
// and review before resolving.
var ValidOrchestratedTransitions = map[OrchestratedStatus][]OrchestratedStatus{
	OrchestratedStatusOpen:             {OrchestratedStatusAwaitingEvidence, OrchestratedStatusReturned},
	OrchestratedStatusAwaitingEvidence: {OrchestratedStatusUnderReview, OrchestratedStatusResolved, OrchestratedStatusReturned},
	OrchestratedStatusUnderReview:      {OrchestratedStatusResolved, OrchestratedStatusAwaitingEvidence},
	OrchestratedStatusResolved:         {},
	OrchestratedStatusReturned:         {},
}

// CanAdvanceOrchestrated reports whether a status move is legal.
func CanAdvanceOrchestrated(from, to OrchestratedStatus) bool {
	for _, t := range ValidOrchestratedTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// EvidenceTypes returns the collected evidence type list.
func (d *OrchestratedDispute) EvidenceTypes() []string {
	out := make([]string, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		out = append(out, e.Type)
	}
	return out
}
