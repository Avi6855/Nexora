package paycycle

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Decision is the approval-chain verdict for a payment intent.
type Decision string

const (
	DecisionAllow           Decision = "ALLOW"
	DecisionRequireApproval Decision = "REQUIRE_APPROVAL"
	DecisionDelay           Decision = "DELAY"
	DecisionBlock           Decision = "BLOCK"
)

var (
	// ErrApprovalNotFound maps to HTTP 404.
	ErrApprovalNotFound = errors.New("approval request not found")
	// ErrApprovalExists maps to HTTP 409 (duplicate rule id).
	ErrApprovalExists = errors.New("approval rule already exists")
	// ErrApprovalState maps to HTTP 409 (already decided).
	ErrApprovalState = errors.New("approval request already decided")
	// ErrApprovalExpired maps to HTTP 409 (decision window elapsed).
	ErrApprovalExpired = errors.New("approval request expired")
)

// DefaultApprovalTTL is the decision window when callers pass ttl <= 0.
const DefaultApprovalTTL = 24 * time.Hour

// PaymentIntent is the payment under review.
type PaymentIntent struct {
	AmountMinor      int64  `json:"amount_minor"`
	Merchant         string `json:"merchant,omitempty"`
	Country          string `json:"country,omitempty"`
	IsNewBeneficiary bool   `json:"is_new_beneficiary"`
}

// ApprovalRule gates intents: every non-zero field must match. Empty
// Merchant/Country match anything; MinAmountMinor == 0 disables the amount
// floor; NewBeneficiaryOnly restricts the rule to first-time payees.
type ApprovalRule struct {
	ID                 string   `json:"id"`
	MinAmountMinor     int64    `json:"min_amount_minor,omitempty"`
	Merchant           string   `json:"merchant,omitempty"`
	Country            string   `json:"country,omitempty"`
	NewBeneficiaryOnly bool     `json:"new_beneficiary_only,omitempty"`
	Decision           Decision `json:"decision"`
	DelaySeconds       int64    `json:"delay_seconds,omitempty"`
}

// matches reports whether rule covers intent. Merchant is a
// case-insensitive substring so "amazon" catches "AMAZON UK"; country is an
// exact case-insensitive code.
func (r ApprovalRule) matches(intent PaymentIntent) bool {
	if r.MinAmountMinor > 0 && intent.AmountMinor < r.MinAmountMinor {
		return false
	}
	if r.Merchant != "" && !strings.Contains(strings.ToLower(intent.Merchant), strings.ToLower(r.Merchant)) {
		return false
	}
	if r.Country != "" && !strings.EqualFold(intent.Country, r.Country) {
		return false
	}
	if r.NewBeneficiaryOnly && !intent.IsNewBeneficiary {
		return false
	}
	return true
}

// severity ranks decisions so overlapping rules resolve deterministically to
// the most restrictive verdict.
func severity(d Decision) int {
	switch d {
	case DecisionBlock:
		return 4
	case DecisionRequireApproval:
		return 3
	case DecisionDelay:
		return 2
	default:
		return 1
	}
}

// ApprovalStatus is the lifecycle of one approval request.
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "PENDING"
	ApprovalApproved ApprovalStatus = "APPROVED"
	ApprovalRejected ApprovalStatus = "REJECTED"
	ApprovalExpired  ApprovalStatus = "EXPIRED"
)

// ApprovalRequest is one human decision with an expiry.
type ApprovalRequest struct {
	ID        string         `json:"id"`
	RuleID    string         `json:"rule_id,omitempty"`
	Intent    PaymentIntent  `json:"intent"`
	Decision  Decision       `json:"decision"`
	Status    ApprovalStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
}

// Engine holds approval rules and requests. Mutex-free like Store; services
// own concurrency.
type Engine struct {
	rules     []ApprovalRule
	approvals map[string]*ApprovalRequest
	seq       int
}

// NewEngine creates an empty approval engine.
func NewEngine() *Engine {
	return &Engine{approvals: map[string]*ApprovalRequest{}}
}

// AddRule stores a rule; duplicate ids are rejected. Empty ids are assigned
// deterministically (rule-1, rule-2, ...).
func (e *Engine) AddRule(r ApprovalRule) (ApprovalRule, error) {
	switch r.Decision {
	case DecisionAllow, DecisionRequireApproval, DecisionDelay, DecisionBlock:
	default:
		return ApprovalRule{}, fmt.Errorf("unknown decision %q", string(r.Decision))
	}
	if r.ID == "" {
		e.seq++
		r.ID = fmt.Sprintf("rule-%d", e.seq)
	}
	for _, existing := range e.rules {
		if existing.ID == r.ID {
			return ApprovalRule{}, fmt.Errorf("approval rule %s: %w", r.ID, ErrApprovalExists)
		}
	}
	e.rules = append(e.rules, r)
	return r, nil
}

// Rules returns a copy of the rule set in insertion order.
func (e *Engine) Rules() []ApprovalRule {
	return append([]ApprovalRule(nil), e.rules...)
}

// Evaluate returns the most restrictive decision among matching rules (first
// added wins ties), or ALLOW with an empty rule id when nothing matches.
func (e *Engine) Evaluate(intent PaymentIntent) (Decision, string) {
	best := DecisionAllow
	bestID := ""
	bestRank := 0
	for _, r := range e.rules {
		if !r.matches(intent) {
			continue
		}
		if rank := severity(r.Decision); rank > bestRank {
			best, bestID, bestRank = r.Decision, r.ID, rank
		}
	}
	return best, bestID
}

// RequestApproval opens a PENDING request recording the evaluated decision.
// ttl <= 0 selects DefaultApprovalTTL; zero now means time.Now().UTC().
func (e *Engine) RequestApproval(intent PaymentIntent, ttl time.Duration, now time.Time) (*ApprovalRequest, error) {
	if ttl <= 0 {
		ttl = DefaultApprovalTTL
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision, ruleID := e.Evaluate(intent)
	e.seq++
	req := &ApprovalRequest{
		ID:        fmt.Sprintf("apr-%06d", e.seq),
		RuleID:    ruleID,
		Intent:    intent,
		Decision:  decision,
		Status:    ApprovalPending,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	e.approvals[req.ID] = req
	cp := *req
	return &cp, nil
}

// DecideApproval approves or rejects a PENDING request. Deciding after expiry
// flips the request to EXPIRED and reports ErrApprovalExpired; deciding twice
// reports ErrApprovalState.
func (e *Engine) DecideApproval(id string, approve bool, now time.Time) (*ApprovalRequest, error) {
	req, ok := e.approvals[id]
	if !ok {
		return nil, ErrApprovalNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if req.Status != ApprovalPending {
		return nil, fmt.Errorf("approval %s is %s: %w", id, req.Status, ErrApprovalState)
	}
	if !now.Before(req.ExpiresAt) {
		req.Status = ApprovalExpired
		return nil, fmt.Errorf("approval %s: %w", id, ErrApprovalExpired)
	}
	if approve {
		req.Status = ApprovalApproved
	} else {
		req.Status = ApprovalRejected
	}
	cp := *req
	return &cp, nil
}

// ApprovalStatus returns one request, lazily expiring PENDING requests whose
// window has elapsed.
func (e *Engine) ApprovalStatus(id string, now time.Time) (*ApprovalRequest, error) {
	req, ok := e.approvals[id]
	if !ok {
		return nil, ErrApprovalNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if req.Status == ApprovalPending && !now.Before(req.ExpiresAt) {
		req.Status = ApprovalExpired
	}
	cp := *req
	return &cp, nil
}
