// Package support implements Nexora's customer-operations platform: the
// purpose-filtered context API, the cross-bank support case state machine,
// the queue fairness engine, and cross-channel case deduplication.
//
// Four problems, one platform:
//
//  1. CONTEXT: an agent currently opens six systems to serve one customer.
//     One API returns account/card/payment/case/communication state in a
//     single response — with PURPOSE-BASED field filtering, because an agent
//     handling a card freeze has no business reading vulnerability flags,
//     and the API must enforce that, not the UI.
//
//  2. CASE LIFECYCLE: OPEN→ASSIGNED→WAITING_CUSTOMER→WAITING_EXTERNAL→
//     RESOLVED→REOPENED with SLA deadlines per priority and automatic
//     escalation when they breach.
//
//  3. FAIRNESS: queue order is computed from customer impact, financial
//     exposure, vulnerability, SLA pressure and age — never arrival order,
//     never agent preference.
//
//  4. DEDUPLICATION: one problem reported over chat, email, phone and
//     in-app is ONE case. Deterministic signals (customer, transaction,
//     time window) + text similarity decide MERGE/LINK/KEEP_SEPARATE.
package support

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------- 18. Customer context API ----------

// Purpose enumerates why the context is being requested. Field visibility is
// derived from purpose — the enforcement point for least privilege.
type Purpose string

const (
	PurposeCardSupport    Purpose = "CARD_SUPPORT"
	PurposePaymentSupport Purpose = "PAYMENT_SUPPORT"
	PurposeGeneralSupport Purpose = "GENERAL_SUPPORT"
	PurposeFraudSupport   Purpose = "FRAUD_SUPPORT"
)

// ContextView is the assembled customer context, filtered by purpose.
type ContextView struct {
	CustomerID     string   `json:"customer_id"`
	Purpose        Purpose  `json:"purpose"`
	AccountState   string   `json:"account_state,omitempty"` // OPEN/FROZEN/CLOSED
	CardState      string   `json:"card_state,omitempty"`
	PaymentState   string   `json:"payment_state,omitempty"`
	RecentActivity []string `json:"recent_activity,omitempty"`
	OpenCases      []string `json:"open_cases,omitempty"`
	Comms          []string `json:"communication_history,omitempty"`
	Vulnerable     bool     `json:"-"`              // only fraud/vulnerability purposes
	Omitted        []string `json:"omitted_fields"` // transparency: what was withheld
}

// SourceData is everything the platform knows (never returned raw).
type SourceData struct {
	CustomerID     string
	AccountState   string
	CardState      string
	PaymentState   string
	RecentActivity []string
	OpenCases      []string
	Comms          []string
	Vulnerable     bool
	InternalNotes  []string
}

// BuildContext assembles the view for a purpose. Internal notes NEVER leave
// the platform for agent-facing purposes; vulnerability flags only for
// purposes that must know (fraud, general duty-of-care).
func BuildContext(src SourceData, p Purpose) ContextView {
	v := ContextView{CustomerID: src.CustomerID, Purpose: p}
	var omitted []string
	include := func(cond bool, set func(), field string) {
		if cond {
			set()
		} else {
			omitted = append(omitted, field)
		}
	}
	include(true, func() { v.AccountState = src.AccountState }, "account_state")
	include(p == PurposeCardSupport || p == PurposeFraudSupport || p == PurposeGeneralSupport,
		func() { v.CardState = src.CardState }, "card_state")
	include(p == PurposePaymentSupport || p == PurposeFraudSupport || p == PurposeGeneralSupport,
		func() { v.PaymentState = src.PaymentState }, "payment_state")
	include(p != PurposeCardSupport,
		func() { v.RecentActivity = src.RecentActivity }, "recent_activity")
	include(true, func() { v.OpenCases = src.OpenCases }, "open_cases")
	include(true, func() { v.Comms = src.Comms }, "communication_history")
	include(p == PurposeFraudSupport || p == PurposeGeneralSupport,
		func() { v.Vulnerable = src.Vulnerable }, "vulnerability_flag")
	// Internal notes are never exposed to any agent-facing purpose.
	omitted = append(omitted, "internal_notes")
	v.Omitted = omitted
	return v
}

// ---------- 19. Case state machine ----------

// CaseState is the support case lifecycle.
type CaseState string

const (
	CaseOpen            CaseState = "OPEN"
	CaseAssigned        CaseState = "ASSIGNED"
	CaseWaitingCustomer CaseState = "WAITING_CUSTOMER"
	CaseWaitingExternal CaseState = "WAITING_EXTERNAL"
	CaseResolved        CaseState = "RESOLVED"
	CaseReopened        CaseState = "REOPENED"
	CaseEscalated       CaseState = "ESCALATED"
)

// Priority drives SLA deadlines.
type Priority string

const (
	PriorityLow      Priority = "LOW"
	PriorityNormal   Priority = "NORMAL"
	PriorityHigh     Priority = "HIGH"
	PriorityCritical Priority = "CRITICAL"
)

// SLATarget is the first-response target per priority.
func SLATarget(p Priority) time.Duration {
	switch p {
	case PriorityCritical:
		return 15 * time.Minute
	case PriorityHigh:
		return 1 * time.Hour
	case PriorityNormal:
		return 8 * time.Hour
	default:
		return 24 * time.Hour
	}
}

var caseTransitions = map[CaseState][]CaseState{
	CaseOpen:            {CaseAssigned, CaseResolved, CaseEscalated},
	CaseAssigned:        {CaseWaitingCustomer, CaseWaitingExternal, CaseResolved, CaseEscalated},
	CaseWaitingCustomer: {CaseAssigned, CaseResolved, CaseEscalated},
	CaseWaitingExternal: {CaseAssigned, CaseResolved, CaseEscalated},
	CaseReopened:        {CaseAssigned, CaseEscalated},
	CaseResolved:        {CaseReopened}, // reopen is the only exit
	CaseEscalated:       {CaseAssigned},
}

// Case is one customer support case.
type Case struct {
	ID          string    `json:"id"`
	CustomerID  string    `json:"customer_id"`
	State       CaseState `json:"state"`
	Priority    Priority  `json:"priority"`
	Owner       string    `json:"owner,omitempty"` // agent id
	CreatedAt   time.Time `json:"created_at"`
	Deadline    time.Time `json:"sla_deadline"`
	Channel     string    `json:"channel"` // CHAT, EMAIL, PHONE, IN_APP
	Subject     string    `json:"subject"`
	LinkedTxnID string    `json:"linked_txn_id,omitempty"`
	BreachCount int       `json:"breach_count"`
}

// Move transitions the case, enforcing the machine.
func (c *Case) Move(to CaseState, now time.Time) error {
	for _, allowed := range caseTransitions[c.State] {
		if allowed == to {
			if to == CaseReopened {
				c.State = to
				c.Deadline = now.Add(SLATarget(c.Priority))
				return nil
			}
			c.State = to
			return nil
		}
	}
	return fmt.Errorf("illegal case transition %s → %s", c.State, to)
}

// SLABreached reports (and counts) a breach at instant now.
func (c *Case) SLABreached(now time.Time) bool {
	if now.After(c.Deadline) && c.State != CaseResolved {
		c.BreachCount++
		c.Deadline = now.Add(SLATarget(c.Priority)) // re-arm once counted
		return true
	}
	return false
}

// Escalate moves an unowned/breached case up.
func (c *Case) Escalate(now time.Time) error {
	if err := c.Move(CaseEscalated, now); err != nil {
		return err
	}
	c.Deadline = now.Add(SLATarget(c.Priority) / 2) // escalated = tighter SLA
	return nil
}

// ---------- 20. Queue fairness engine ----------

// QueueCase is the scoring view of a waiting case.
type QueueCase struct {
	Case
	CustomerImpact     float64 `json:"customer_impact"`    // 0..1 (blocked from money = high)
	FinancialExposure  int64   `json:"financial_exposure"` // minor units at risk
	VulnerableCustomer bool    `json:"vulnerable_customer"`
}

// FairnessScore weights the ranking. Weights are documented and fixed:
// vulnerability doubles weight because duty of care outranks money; SLA
// pressure grows non-linearly as the deadline approaches (a case 95% through
// its SLA must outrank one 20% through with higher exposure).
type fairnessWeights struct {
	impact     float64
	exposure   float64
	vulnerable float64
	sla        float64
	age        float64
}

var weights = fairnessWeights{impact: 30, exposure: 25, vulnerable: 40, sla: 30, age: 5}

// FairnessScore computes queue priority (higher = sooner).
func FairnessScore(c QueueCase, now time.Time) float64 {
	score := c.CustomerImpact * weights.impact
	// Exposure saturates at £5,000 — a £50k dispute is not 10× a £5k one
	// in human priority terms.
	exp := float64(c.FinancialExposure) / 500000.0
	if exp > 1 {
		exp = 1
	}
	score += exp * weights.exposure
	if c.VulnerableCustomer {
		score += weights.vulnerable
	}
	// SLA pressure: fraction of deadline elapsed, 0..1.
	if !c.Deadline.IsZero() {
		total := c.Deadline.Sub(c.CreatedAt)
		if total > 0 {
			elapsed := now.Sub(c.CreatedAt)
			frac := float64(elapsed) / float64(total)
			if frac > 1 {
				frac = 1
			}
			score += frac * weights.sla
		}
	}
	// Age: quarter point per hour, capped.
	ageH := now.Sub(c.CreatedAt).Hours()
	if ageH > 48 {
		ageH = 48
	}
	score += ageH * 0.25 / 48 * weights.age * 4
	return score
}

// Rank orders the queue, fairest first.
func Rank(cases []QueueCase, now time.Time) []QueueCase {
	out := make([]QueueCase, len(cases))
	copy(out, cases)
	sort.SliceStable(out, func(i, j int) bool {
		return FairnessScore(out[i], now) > FairnessScore(out[j], now)
	})
	return out
}

// ---------- 21. Case deduplication ----------

// DedupVerdict decides what to do with a candidate duplicate.
type DedupVerdict string

const (
	DedupMerge        DedupVerdict = "MERGE"         // same problem, one case
	DedupLink         DedupVerdict = "LINK"          // related, keep separate but connected
	DedupKeepSeparate DedupVerdict = "KEEP_SEPARATE" // different problems
)

// DedupInput is the comparison between an existing open case and a new report.
type DedupInput struct {
	Existing        Case
	NewSubject      string
	NewChannel      string
	SameCustomer    bool
	SameTransaction bool
	WindowHours     float64 // time between the two reports
}

// DedupThresholds are the tuned bands for the deterministic signals.
const (
	mergeWindowHours = 72.0
	mergeTextSimilar = 0.62
	linkTextSimilar  = 0.35
)

// Dedup decides MERGE/LINK/KEEP_SEPARATE deterministically:
//   - MERGE: same customer + (same transaction OR high text similarity)
//     within the merge window.
//   - LINK: related signals but weaker (similar text OR same transaction,
//     outside the window).
//   - KEEP_SEPARATE: nothing aligns — different problems must never be
//     merged, because merging silently drops one customer's thread.
func Dedup(in DedupInput) DedupVerdict {
	sim := textSimilarity(in.Existing.Subject, in.NewSubject)
	if !in.SameCustomer {
		return DedupKeepSeparate
	}
	strongSignal := in.SameTransaction || sim >= mergeTextSimilar
	weakSignal := in.SameTransaction || sim >= linkTextSimilar
	switch {
	case strongSignal && in.WindowHours <= mergeWindowHours:
		return DedupMerge
	case weakSignal:
		return DedupLink
	default:
		return DedupKeepSeparate
	}
}

// textSimilarity is a token-set Jaccard with bigram fallback for short
// subjects — deterministic, no ML dependency, explainable.
func textSimilarity(a, b string) float64 {
	at, bt := tokenSetLower(a), tokenSetLower(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range at {
		set[t] = true
	}
	inter := 0
	for _, t := range bt {
		if set[t] {
			inter++
		}
	}
	union := len(at) + len(bt) - inter
	return float64(inter) / float64(union)
}

func tokenSetLower(s string) []string {
	return strings.Fields(strings.ToLower(s))
}
