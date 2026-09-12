// Package decisionops implements Nexora's fraud-decision operations platform:
//
//  16. Right-to-explain: every automated decision carries its signals plus
//     the policy id/version that produced it, and Explain renders a
//     human-readable account of the top contributing signals in plain
//     language — a customer asking "why was my payment blocked?" gets an
//     answer, not a score.
//
//  17. Human-in-the-loop queue: review cases carry a risk score and derived
//     priority, an analyst lease (lock with timeout so a case can never
//     wedge behind a closed laptop), SLA deadlines with timeout sweeps, and
//     escalate/reassign flows. Every action lands in a full audit trail.
//
//  18. Model audit trail: an immutable record of {model_version,
//     feature_snapshot_hash, policy_version, decision, confidence,
//     human_override} with replay verification — recompute the snapshot
//     hash and the record replays as REPRODUCED or TAMPERED.
package decisionops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// ── 16. Right-to-explain ────────────────────────────────────────────────────

// Signal is one weighted input to an automated decision.
type Signal struct {
	Name   string
	Value  float64
	Weight float64
}

// Decision is the automated outcome to be explained.
type Decision struct {
	Signals       []Signal
	PolicyID      string
	PolicyVersion string
	Outcome       string
}

// Explanation is the human-readable account of a decision.
type Explanation struct {
	Summary       string
	Outcome       string
	PolicyID      string
	PolicyVersion string
	TopSignals    []string
	Lines         []string
}

// plainLanguage renders a signal name as a customer-facing phrase.
func plainLanguage(name string) string {
	switch name {
	case "unusual_location":
		return "the transaction came from an unusual location"
	case "velocity_spike":
		return "many transactions happened in a short time"
	case "new_device":
		return "a new or unrecognised device was used"
	case "amount_anomaly":
		return "the amount was unusual for this account"
	case "merchant_risk":
		return "the merchant has a risky profile"
	case "account_age":
		return "the account is very new"
	case "failed_attempts":
		return "there were recent failed attempts"
	case "cross_border":
		return "the payment crossed a border"
	default:
		return fmt.Sprintf("signal %q was observed", name)
	}
}

// contribution is the signed influence of a signal on the outcome.
func contribution(s Signal) float64 { return s.Value * s.Weight }

// strength words the magnitude for a human reader.
func strength(abs float64) string {
	switch {
	case abs >= 1.0:
		return "strongly"
	case abs >= 0.4:
		return "moderately"
	default:
		return "slightly"
	}
}

// direction words whether the signal pushed towards or away from action.
func direction(c float64) string {
	if c >= 0 {
		return "increased risk"
	}
	return "reduced risk"
}

// Explain renders a decision as a human-readable explanation, listing the
// top contributing signals (by |value × weight|) in plain language.
func Explain(d Decision) Explanation {
	ordered := append([]Signal(nil), d.Signals...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return math.Abs(contribution(ordered[i])) > math.Abs(contribution(ordered[j]))
	})
	top := 3
	if len(ordered) < top {
		top = len(ordered)
	}
	exp := Explanation{
		Outcome:       d.Outcome,
		PolicyID:      d.PolicyID,
		PolicyVersion: d.PolicyVersion,
	}
	for _, s := range ordered[:top] {
		c := contribution(s)
		exp.TopSignals = append(exp.TopSignals, s.Name)
		exp.Lines = append(exp.Lines, fmt.Sprintf(
			"%s %s: observed value %.2f with weight %.2f (%s).",
			s.Name, direction(c), s.Value, s.Weight,
			fmt.Sprintf("%s and %s %s", plainLanguage(s.Name), strength(math.Abs(c)), direction(c)),
		))
	}
	policy := d.PolicyID
	if d.PolicyVersion != "" {
		policy += " (version " + d.PolicyVersion + ")"
	}
	if len(exp.TopSignals) == 0 {
		exp.Summary = fmt.Sprintf("Decision %s under policy %s was made with no recorded signals.", d.Outcome, policy)
	} else {
		exp.Summary = fmt.Sprintf("Decision %s under policy %s was driven mainly by %s.",
			d.Outcome, policy, joinNames(exp.TopSignals))
	}
	return exp
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			if i == len(names)-1 {
				out += " and "
			} else {
				out += ", "
			}
		}
		out += n
	}
	return out
}

// ── 17. Human-in-the-loop queue ─────────────────────────────────────────────

var (
	ErrCaseNotFound  = errors.New("unknown case")
	ErrLeaseConflict = errors.New("case is leased to another analyst")
	ErrCaseDecided   = errors.New("case is already decided")
	ErrBadDecision   = errors.New("unknown decision")
)

// CaseStatus is the lifecycle state of a review case.
type CaseStatus string

const (
	StatusOpen      CaseStatus = "OPEN"
	StatusLeased    CaseStatus = "LEASED"
	StatusDecided   CaseStatus = "DECIDED"
	StatusEscalated CaseStatus = "ESCALATED"
)

// Review decisions an analyst may record.
const (
	DecideApprove         = "approve"
	DecideReject          = "reject"
	DecideRequestEvidence = "request-evidence"
	DecideEscalate        = "escalate"
)

// Case is one human-review item.
type Case struct {
	ID          string
	Subject     string
	RiskScore   float64
	Priority    string
	Status      CaseStatus
	Assignee    string
	LeaseHolder string
	LeaseExpiry time.Time
	SLADeadline time.Time
	CreatedAt   time.Time
	Decision    string
	DecidedBy   string
	DecidedAt   time.Time
}

// AuditEntry is one immutable step in a case's history.
type AuditEntry struct {
	At     time.Time
	Actor  string
	Action string
	Detail string
}

// PriorityForRisk derives queue priority from a risk score.
func PriorityForRisk(risk float64) string {
	switch {
	case risk >= 80:
		return "CRITICAL"
	case risk >= 60:
		return "HIGH"
	case risk >= 40:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// Queue is the HITL work queue.
type Queue struct {
	mu    sync.Mutex
	seq   int
	cases map[string]*Case
	audit map[string][]AuditEntry
}

// NewQueue builds an empty queue.
func NewQueue() *Queue {
	return &Queue{cases: map[string]*Case{}, audit: map[string][]AuditEntry{}}
}

func (q *Queue) log(id, actor, action, detail string, now time.Time) {
	q.audit[id] = append(q.audit[id], AuditEntry{At: now, Actor: actor, Action: action, Detail: detail})
}

// Enqueue adds a review case with a risk score and SLA deadline.
func (q *Queue) Enqueue(subject string, risk float64, sla time.Duration, now time.Time) *Case {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	c := &Case{
		ID:          fmt.Sprintf("case-%d", q.seq),
		Subject:     subject,
		RiskScore:   risk,
		Priority:    PriorityForRisk(risk),
		Status:      StatusOpen,
		SLADeadline: now.Add(sla),
		CreatedAt:   now,
	}
	q.cases[c.ID] = c
	q.log(c.ID, "system", "enqueue",
		fmt.Sprintf("subject %s risk %.1f priority %s sla %s", subject, risk, c.Priority, c.SLADeadline.Format(time.RFC3339)), now)
	return copyCase(c)
}

// Get returns a copy of a case.
func (q *Queue) Get(id string) (*Case, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.cases[id]
	if !ok {
		return nil, ErrCaseNotFound
	}
	return copyCase(c), nil
}

func copyCase(c *Case) *Case {
	cp := *c
	return &cp
}

// Lease takes the analyst lock on a case for ttl. A live lease held by
// another analyst conflicts (409); an expired lease may be stolen.
func (q *Queue) Lease(id, analyst string, ttl time.Duration, now time.Time) error {
	if analyst == "" {
		return errors.New("analyst is required")
	}
	if ttl <= 0 {
		return errors.New("lease ttl must be positive")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.cases[id]
	if !ok {
		return ErrCaseNotFound
	}
	if c.Status == StatusDecided {
		return ErrCaseDecided
	}
	if c.LeaseHolder != "" && c.LeaseHolder != analyst && now.Before(c.LeaseExpiry) {
		return ErrLeaseConflict
	}
	c.LeaseHolder = analyst
	c.LeaseExpiry = now.Add(ttl)
	c.Assignee = analyst
	if c.Status != StatusEscalated {
		c.Status = StatusLeased
	}
	q.log(id, analyst, "lease", fmt.Sprintf("lease until %s", c.LeaseExpiry.Format(time.RFC3339)), now)
	return nil
}

func validDecision(d string) bool {
	switch d {
	case DecideApprove, DecideReject, DecideRequestEvidence, DecideEscalate:
		return true
	}
	return false
}

// Decide records an analyst decision. The decider must hold a live lease
// unless the lease has expired (a stolen case is decided by its new holder).
func (q *Queue) Decide(id, analyst, decision, note string, now time.Time) error {
	if !validDecision(decision) {
		return ErrBadDecision
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.cases[id]
	if !ok {
		return ErrCaseNotFound
	}
	if c.Status == StatusDecided {
		return ErrCaseDecided
	}
	if c.LeaseHolder != "" && c.LeaseHolder != analyst && now.Before(c.LeaseExpiry) {
		return ErrLeaseConflict
	}
	c.Status = StatusDecided
	c.Decision = decision
	c.DecidedBy = analyst
	c.DecidedAt = now
	c.LeaseHolder = ""
	c.LeaseExpiry = time.Time{}
	detail := decision
	if note != "" {
		detail += ": " + note
	}
	q.log(id, analyst, "decide", detail, now)
	return nil
}

// Escalate hands a case to a higher tier.
func (q *Queue) Escalate(id, actor, to string, now time.Time) error {
	if to == "" {
		return errors.New("escalation target is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.cases[id]
	if !ok {
		return ErrCaseNotFound
	}
	if c.Status == StatusDecided {
		return ErrCaseDecided
	}
	c.Status = StatusEscalated
	c.Assignee = to
	c.LeaseHolder = ""
	c.LeaseExpiry = time.Time{}
	q.log(id, actor, "escalate", "escalated to "+to, now)
	return nil
}

// Reassign moves a case to another analyst, clearing any live lease.
func (q *Queue) Reassign(id, actor, to string, now time.Time) error {
	if to == "" {
		return errors.New("reassignment target is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.cases[id]
	if !ok {
		return ErrCaseNotFound
	}
	if c.Status == StatusDecided {
		return ErrCaseDecided
	}
	c.Assignee = to
	c.LeaseHolder = ""
	c.LeaseExpiry = time.Time{}
	if c.Status == StatusLeased {
		c.Status = StatusOpen
	}
	q.log(id, actor, "reassign", "reassigned to "+to, now)
	return nil
}

// SweepTimeouts releases expired leases and auto-escalates SLA breaches.
// It returns the ids it touched.
func (q *Queue) SweepTimeouts(now time.Time) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var swept []string
	for id, c := range q.cases {
		if c.Status == StatusDecided {
			continue
		}
		if c.LeaseHolder != "" && !now.Before(c.LeaseExpiry) {
			holder := c.LeaseHolder
			c.LeaseHolder = ""
			c.LeaseExpiry = time.Time{}
			if c.Status == StatusLeased {
				c.Status = StatusOpen
			}
			q.log(id, "system", "lease-timeout", "lease held by "+holder+" expired", now)
			swept = append(swept, id)
		}
		if c.Status != StatusDecided && c.Status != StatusEscalated && !c.SLADeadline.IsZero() && now.After(c.SLADeadline) {
			c.Status = StatusEscalated
			c.LeaseHolder = ""
			c.LeaseExpiry = time.Time{}
			q.log(id, "system", "sla-breach", "SLA deadline passed, auto-escalated", now)
			swept = append(swept, id)
		}
	}
	sort.Strings(swept)
	return swept
}

// Audit returns the full audit trail for a case, oldest first.
func (q *Queue) Audit(id string) ([]AuditEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.cases[id]; !ok {
		return nil, ErrCaseNotFound
	}
	return append([]AuditEntry(nil), q.audit[id]...), nil
}

// ── 18. Model audit trail ───────────────────────────────────────────────────

// Verdicts for replay verification.
const (
	VerdictReproduced = "REPRODUCED"
	VerdictTampered   = "TAMPERED"
)

// ModelRecord is one immutable model-decision record.
type ModelRecord struct {
	ID                  string
	ModelVersion        string
	FeatureSnapshotHash string
	PolicyVersion       string
	Decision            string
	Confidence          float64
	HumanOverride       bool
	OverrideBy          string
	CreatedAt           time.Time
}

// ComputeSnapshotHash hashes a canonical feature snapshot.
func ComputeSnapshotHash(snapshot string) string {
	sum := sha256.Sum256([]byte(snapshot))
	return hex.EncodeToString(sum[:])
}

// ModelStore is the immutable audit-trail store.
type ModelStore struct {
	mu      sync.Mutex
	seq     int
	records map[string]*ModelRecord
}

// NewModelStore builds an empty store.
func NewModelStore() *ModelStore {
	return &ModelStore{records: map[string]*ModelRecord{}}
}

// Record appends an immutable record, hashing the feature snapshot.
func (s *ModelStore) Record(modelVersion, snapshot, policyVersion, decision string, confidence float64, humanOverride bool, overrideBy string, now time.Time) *ModelRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	r := &ModelRecord{
		ID:                  fmt.Sprintf("model-%d", s.seq),
		ModelVersion:        modelVersion,
		FeatureSnapshotHash: ComputeSnapshotHash(snapshot),
		PolicyVersion:       policyVersion,
		Decision:            decision,
		Confidence:          confidence,
		HumanOverride:       humanOverride,
		OverrideBy:          overrideBy,
		CreatedAt:           now,
	}
	s.records[r.ID] = r
	cp := *r
	return &cp
}

// Get returns a copy of a record.
func (s *ModelStore) Get(id string) (*ModelRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return nil, ErrCaseNotFound
	}
	cp := *r
	return &cp, nil
}

// Verify recomputes the snapshot hash: match → REPRODUCED, else TAMPERED.
func (s *ModelStore) Verify(id, snapshot string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return "", ErrCaseNotFound
	}
	if ComputeSnapshotHash(snapshot) == r.FeatureSnapshotHash {
		return VerdictReproduced, nil
	}
	return VerdictTampered, nil
}
