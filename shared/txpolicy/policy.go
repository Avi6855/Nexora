// Package txpolicy is Nexora's dynamic transaction-policy platform: versioned
// screening rules with deterministic evaluation, rollback, a full audit log,
// and a shadow-mode simulator for testing candidate policies against history
// before they touch live traffic.
package txpolicy

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Effect is what a matched rule demands.
type Effect string

const (
	EffectRequireStepUp Effect = "require_step_up"
	EffectApprove       Effect = "approve"
	EffectBlock         Effect = "block"
)

// valid reports whether e is a known effect.
func (e Effect) valid() bool {
	switch e {
	case EffectRequireStepUp, EffectApprove, EffectBlock:
		return true
	default:
		return false
	}
}

// Predicate gates a rule: every set field must match. Nil numeric/bool
// pointers and empty strings match anything, so a zero Predicate is an
// explicit catch-all.
type Predicate struct {
	MinAmountMinor     *int64 `json:"min_amount_minor,omitempty"`
	BeneficiaryNew     *bool  `json:"beneficiary_new,omitempty"`
	MinDestinationRisk *int   `json:"min_destination_risk,omitempty"`
	Category           string `json:"category,omitempty"`
	Country            string `json:"country,omitempty"`
}

// matches reports whether ctx satisfies the predicate.
func (p Predicate) matches(c Context) bool {
	if p.MinAmountMinor != nil && c.AmountMinor < *p.MinAmountMinor {
		return false
	}
	if p.BeneficiaryNew != nil && c.BeneficiaryNew != *p.BeneficiaryNew {
		return false
	}
	if p.MinDestinationRisk != nil && c.DestinationRisk < *p.MinDestinationRisk {
		return false
	}
	if p.Category != "" && !strings.EqualFold(c.Category, p.Category) {
		return false
	}
	if p.Country != "" && !strings.EqualFold(c.Country, p.Country) {
		return false
	}
	return true
}

// Rule is one versioned policy line. Version is assigned by PutRule — callers
// must not set it.
type Rule struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	Priority  int       `json:"priority"`
	Predicate Predicate `json:"predicate"`
	Effect    Effect    `json:"effect"`
}

// Context is the transaction under evaluation.
type Context struct {
	AmountMinor     int64  `json:"amount_minor"`
	BeneficiaryNew  bool   `json:"beneficiary_new"`
	DestinationRisk int    `json:"destination_risk"` // 0-100
	Category        string `json:"category,omitempty"`
	Country         string `json:"country,omitempty"`
}

// Result is the outcome of one evaluation.
type Result struct {
	Matched bool   `json:"matched"`
	RuleID  string `json:"rule_id,omitempty"`
	Version int    `json:"version,omitempty"`
	Effect  Effect `json:"effect"`
}

// AuditEntry records one evaluation for replay and inspection.
type AuditEntry struct {
	At      time.Time `json:"at"`
	Context Context   `json:"context"`
	RuleID  string    `json:"rule_id,omitempty"`
	Version int       `json:"version,omitempty"`
	Effect  Effect    `json:"effect"`
	Matched bool      `json:"matched"`
}

// Engine holds versioned rules and the audit log. Mutex-free; services own
// concurrency.
type Engine struct {
	current map[string]*Rule
	history map[string][]Rule
	audit   []AuditEntry
}

// NewEngine creates an empty policy engine.
func NewEngine() *Engine {
	return &Engine{current: map[string]*Rule{}, history: map[string][]Rule{}}
}

// PutRule stores a rule, bumping its version (1 for a new id, previous+1
// otherwise). The returned copy carries the assigned version.
func (e *Engine) PutRule(r Rule) (Rule, error) {
	if strings.TrimSpace(r.ID) == "" {
		return Rule{}, fmt.Errorf("rule id is required")
	}
	if !r.Effect.valid() {
		return Rule{}, fmt.Errorf("unknown effect %q", string(r.Effect))
	}
	version := 1
	if prev, ok := e.current[r.ID]; ok {
		version = prev.Version + 1
	}
	r.Version = version
	cp := r
	e.current[r.ID] = &cp
	e.history[r.ID] = append(e.history[r.ID], cp)
	return cp, nil
}

// Rollback drops the latest version of id and restores the prior one. It
// fails when the rule is unknown or has only one version (nothing to restore).
func (e *Engine) Rollback(id string) (Rule, error) {
	h := e.history[id]
	if len(h) == 0 {
		return Rule{}, fmt.Errorf("rule %s: %w", id, ErrRuleNotFound)
	}
	if len(h) == 1 {
		return Rule{}, fmt.Errorf("rule %s has no prior version: %w", id, ErrNoPriorVersion)
	}
	h = h[:len(h)-1]
	e.history[id] = h
	restored := h[len(h)-1]
	cp := restored
	e.current[id] = &cp
	return cp, nil
}

// Get returns the current version of one rule.
func (e *Engine) Get(id string) (Rule, error) {
	r, ok := e.current[id]
	if !ok {
		return Rule{}, fmt.Errorf("rule %s: %w", id, ErrRuleNotFound)
	}
	return *r, nil
}

// Rules returns the current rule set in deterministic evaluation order
// (priority ascending, then id ascending).
func (e *Engine) Rules() []Rule {
	out := make([]Rule, 0, len(e.current))
	for _, r := range e.current {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Evaluate matches ctx against the rule set in deterministic order; the
// first matching rule wins. No match means EffectApprove with Matched=false.
// Every evaluation is appended to the audit log.
func (e *Engine) Evaluate(ctx Context) Result {
	var res Result
	if r := matchPure(e, ctx); r != nil {
		res = Result{Matched: true, RuleID: r.ID, Version: r.Version, Effect: r.Effect}
	} else {
		res = Result{Matched: false, Effect: EffectApprove}
	}
	e.audit = append(e.audit, AuditEntry{
		At:      time.Now().UTC(),
		Context: ctx,
		RuleID:  res.RuleID,
		Version: res.Version,
		Effect:  res.Effect,
		Matched: res.Matched,
	})
	return res
}

// Audit returns a copy of the evaluation log, oldest first.
func (e *Engine) Audit() []AuditEntry {
	return append([]AuditEntry(nil), e.audit...)
}

// matchPure finds the winning rule without touching the audit log, so the
// shadow simulator can replay history without polluting production audit.
func matchPure(e *Engine, ctx Context) *Rule {
	if e == nil {
		return nil
	}
	for _, r := range e.Rules() {
		if r.Predicate.matches(ctx) {
			cp := r
			return &cp
		}
	}
	return nil
}
