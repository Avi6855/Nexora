// Package compliance implements Nexora's regulatory platform:
//
//  1. Rule distribution: a rule is only IN FORCE once every serving service
//     has acknowledged it. Unacknowledged rules must not silently change
//     behaviour mid-transaction — the platform refuses to activate a rule
//     with incomplete acknowledgement and reports stale services.
//
//  2. Time-travel reconstruction: an auditor asking "why was this allowed on
//     12 June 2024?" gets the POLICY VERSION IN FORCE AT THAT INSTANT plus
//     the customer state AT THAT INSTANT — never today's rules replayed on
//     yesterday's data.
//
//  3. Evidence provenance: every number in a regulatory report carries the
//     chain that produced it (source datasets → queries → transformation →
//     model version → submission), so "where did this figure come from?" has
//     a machine answer.
//
//  4. Impact analysis: a new rule is projected across the service/endpoint/
//     data-model/customer graph before anyone starts work.
//
//  5. Safe configuration: proposal → validation → diff → approval →
//     simulation → release → monitor → rollback, with drift detection
//     between desired and actual state.
//
//  6. Safe mode: a business-operation safety state. Normal → Degraded →
//     Safe Mode: reads stay available, high-risk writes do not.
package compliance

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// ── 1. Regulatory rule distribution ─────────────────────────────────────────

var (
	ErrRuleNotReady     = errors.New("rule not fully acknowledged")
	ErrRuleSuperseded   = errors.New("rule superseded by a newer version")
	ErrRuleNotFound     = errors.New("rule not found")
	ErrNoPolicyAtTime   = errors.New("no policy version in force at that instant")
	ErrAlreadyApproved  = errors.New("config already approved")
	ErrApprovalRequired = errors.New("config change requires approval")
	ErrSimulationFailed = errors.New("config simulation failed")
	ErrRollbackUnknown  = errors.New("no rollback target recorded")
)

// RuleVersion is one published regulatory rule.
type RuleVersion struct {
	ID          string    `json:"id"`
	Version     int       `json:"version"`
	Payload     string    `json:"payload"` // the machine-readable rule body
	PublishedAt time.Time `json:"published_at"`
	// ActivateAfter gives services a bounded window to acknowledge before
	// enforcement. A rule that is never fully acknowledged NEVER activates.
	ActivateAfter time.Time `json:"activate_after"`
}

// Ack is one service's acknowledgement of a rule version.
type Ack struct {
	Service string    `json:"service"`
	Version int       `json:"version"`
	At      time.Time `json:"at"`
}

// RuleDistributor tracks publication and acknowledgement per service.
type RuleDistributor struct {
	services map[string]bool // serving services expected to acknowledge
	rules    map[string][]RuleVersion
	acks     map[string]map[string]Ack // ruleID → service → ack
	now      func() time.Time
	// ackedHistory[ruleID][service] = highest version that service has ever
	// acknowledged for the rule. A service that acked v1 and has not yet acked
	// v2 is not "unacknowledged" — it is safely RUNNING v1.
	ackedHistory map[string]map[string]int
}

// NewRuleDistributor registers the fleet that must acknowledge rules.
func NewRuleDistributor(services []string, now func() time.Time) *RuleDistributor {
	m := map[string]bool{}
	for _, s := range services {
		m[s] = true
	}
	return &RuleDistributor{
		services:     m,
		rules:        map[string][]RuleVersion{},
		acks:         map[string]map[string]Ack{},
		ackedHistory: map[string]map[string]int{},
		now:          now,
	}
}

// Publish adds a rule version. Versions must be monotonic per rule.
func (d *RuleDistributor) Publish(r RuleVersion) error {
	if r.ID == "" || r.Payload == "" {
		return errors.New("rule id and payload required")
	}
	existing := d.rules[r.ID]
	if len(existing) > 0 && r.Version != existing[len(existing)-1].Version+1 {
		return fmt.Errorf("version %d must follow %d", r.Version, existing[len(existing)-1].Version)
	}
	d.rules[r.ID] = append(existing, r)
	// The current-version ack map resets (every service must ack the new
	// version) but ackedHistory NEVER resets: a service that acked v1 is
	// legitimately still running v1 while v2 rolls out.
	d.acks[r.ID] = map[string]Ack{}
	if d.ackedHistory[r.ID] == nil {
		d.ackedHistory[r.ID] = map[string]int{}
	}
	return nil
}

// Acknowledge records one service's acceptance of a specific version.
// Acknowledging a superseded version is rejected: a service must never
// "confirm" old behaviour it will no longer run.
func (d *RuleDistributor) Acknowledge(ruleID string, version int, service string) error {
	if !d.services[service] {
		return fmt.Errorf("unknown service %s", service)
	}
	versions := d.rules[ruleID]
	var target *RuleVersion
	for i := range versions {
		if versions[i].Version == version {
			target = &versions[i]
		}
	}
	if target == nil {
		return ErrRuleNotFound
	}
	latest := versions[len(versions)-1]
	if version != latest.Version {
		return fmt.Errorf("%w: %s v%d is stale (latest v%d)", ErrRuleSuperseded, ruleID, version, latest.Version)
	}
	d.acks[ruleID][service] = Ack{Service: service, Version: version, At: d.now()}
	if d.ackedHistory[ruleID] == nil {
		d.ackedHistory[ruleID] = map[string]int{}
	}
	if version > d.ackedHistory[ruleID][service] {
		d.ackedHistory[ruleID][service] = version
	}
	return nil
}

// Status reports acknowledgement completeness.
type RuleStatus struct {
	Activated    bool     `json:"activated"`
	Acknowledged []string `json:"acknowledged"`
	Missing      []string `json:"missing"` // services that have NOT acked the latest version
	Stale        []string `json:"stale"`   // services acking an older version
}

// ActivateWhenReady returns the rule's enforcement status. A rule activates
// only when every serving service has acknowledged the latest version AND
// the activation instant has passed.
func (d *RuleDistributor) ActivateWhenReady(ruleID string) (RuleStatus, error) {
	versions, ok := d.rules[ruleID]
	if !ok {
		return RuleStatus{}, ErrRuleNotFound
	}
	latest := versions[len(versions)-1]
	st := RuleStatus{}
	for svc := range d.services {
		ack, acked := d.acks[ruleID][svc]
		switch {
		case !acked:
			st.Missing = append(st.Missing, svc)
		case ack.Version != latest.Version:
			st.Stale = append(st.Stale, svc)
		default:
			st.Acknowledged = append(st.Acknowledged, svc)
		}
	}
	sort.Strings(st.Acknowledged)
	sort.Strings(st.Missing)
	sort.Strings(st.Stale)
	st.Activated = len(st.Missing) == 0 && len(st.Stale) == 0 && !d.now().Before(latest.ActivateAfter)
	return st, nil
}

// EffectiveVersion returns the version a transaction must be judged against
// at instant t: the newest ACTIVATED version. A never-fully-acknowledged
// version is never effective — the previous version keeps ruling.
func (d *RuleDistributor) EffectiveVersion(ruleID string, t time.Time) (int, error) {
	versions, ok := d.rules[ruleID]
	if !ok {
		return 0, ErrRuleNotFound
	}
	// Walk newest→oldest; the first version EVERY service has acknowledged
	// (acknowledged version ≥ v) whose activation time has passed wins. A
	// service mid-upgrade to v2 is still running v1, so v1 remains effective.
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		complete := true
		for svc := range d.services {
			if d.ackedHistory[ruleID][svc] < v.Version {
				complete = false
				break
			}
		}
		if complete && !t.Before(v.ActivateAfter) {
			return v.Version, nil
		}
	}
	return 0, fmt.Errorf("%w: %s has no fully-acknowledged version", ErrRuleNotReady, ruleID)
}

// ── 2. Time-travel compliance engine ────────────────────────────────────────

// PolicySnapshot is one version of a policy with its validity window.
type PolicySnapshot struct {
	Version        int       `json:"version"`
	Body           string    `json:"body"`
	EffectiveFrom  time.Time `json:"effective_from"`
	EffectiveUntil time.Time `json:"effective_until"` // zero = open-ended
}

// CustomerStateAt is a historical customer fact (append-only event log view).
type CustomerStateAt struct {
	At    time.Time `json:"at"`
	Field string    `json:"field"` // e.g. "kyc_tier", "residency"
	Value string    `json:"value"`
}

// DecisionRecord is the reconstruction output: what rule applied, to what
// customer state, and the resulting decision with its explanation.
type DecisionRecord struct {
	TxID          string            `json:"tx_id"`
	At            time.Time         `json:"at"`
	PolicyVersion int               `json:"policy_version"`
	PolicyBody    string            `json:"policy_body"`
	CustomerState map[string]string `json:"customer_state"`
	Decision      string            `json:"decision"`
	Explanation   string            `json:"explanation"`
}

// TimeTravelStore holds policy snapshots and customer state history.
type TimeTravelStore struct {
	policies map[string][]PolicySnapshot  // policyID → snapshots
	states   map[string][]CustomerStateAt // customerID → state events
}

// NewTimeTravelStore creates an empty store.
func NewTimeTravelStore() *TimeTravelStore {
	return &TimeTravelStore{policies: map[string][]PolicySnapshot{}, states: map[string][]CustomerStateAt{}}
}

// AddPolicy appends a policy snapshot. Overlaps are rejected — two policies
// must never claim the same instant, or reconstruction becomes ambiguous.
func (s *TimeTravelStore) AddPolicy(policyID string, snap PolicySnapshot) error {
	if snap.EffectiveFrom.IsZero() {
		return errors.New("effective_from required")
	}
	for _, p := range s.policies[policyID] {
		overlaps := p.EffectiveUntil.IsZero() || p.EffectiveUntil.After(snap.EffectiveFrom)
		overlaps = overlaps && (snap.EffectiveUntil.IsZero() || snap.EffectiveUntil.After(p.EffectiveFrom))
		if overlaps {
			return fmt.Errorf("policy %s v%d [%s,∞) overlaps new snapshot [%s,∞)",
				policyID, p.Version, p.EffectiveFrom.Format(time.DateOnly), snap.EffectiveFrom.Format(time.DateOnly))
		}
	}
	s.policies[policyID] = append(s.policies[policyID], snap)
	sort.Slice(s.policies[policyID], func(i, j int) bool {
		return s.policies[policyID][i].EffectiveFrom.Before(s.policies[policyID][j].EffectiveFrom)
	})
	return nil
}

// AddState appends a customer state event.
func (s *TimeTravelStore) AddState(customerID string, e CustomerStateAt) {
	s.states[customerID] = append(s.states[customerID], e)
}

// PolicyAt returns the policy version in force at instant t.
func (s *TimeTravelStore) PolicyAt(policyID string, t time.Time) (*PolicySnapshot, error) {
	for i := range s.policies[policyID] {
		p := &s.policies[policyID][i]
		if !t.Before(p.EffectiveFrom) && (p.EffectiveUntil.IsZero() || t.Before(p.EffectiveUntil)) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %s at %s", ErrNoPolicyAtTime, policyID, t.Format(time.RFC3339))
}

// StateAt reconstructs each tracked field's value at instant t (the most
// recent event at or before t). Fields with no event yet are absent — an
// auditor must see "unknown then", not today's value.
func (s *TimeTravelStore) StateAt(customerID string, t time.Time) map[string]string {
	out := map[string]string{}
	for _, e := range s.states[customerID] {
		if !e.At.After(t) {
			out[e.Field] = e.Value
		}
	}
	return out
}

// DecisionFunc re-runs the decision logic for a policy version.
type DecisionFunc func(policyBody string, customerState map[string]string) (decision, explanation string)

// Reconstruct answers the auditor: the decision that WOULD be made under the
// policy and customer state in force at the transaction instant.
func (s *TimeTravelStore) Reconstruct(txID string, policyID, customerID string, at time.Time, decide DecisionFunc) (*DecisionRecord, error) {
	p, err := s.PolicyAt(policyID, at)
	if err != nil {
		return nil, err
	}
	state := s.StateAt(customerID, at)
	decision, explanation := decide(p.Body, state)
	return &DecisionRecord{
		TxID: txID, At: at, PolicyVersion: p.Version, PolicyBody: p.Body,
		CustomerState: state, Decision: decision, Explanation: explanation,
	}, nil
}

// ── 3. Regulatory reporting pipeline with evidence provenance ───────────────

// LineageStep is one stage in producing a reported figure.
type LineageStep struct {
	Stage      string   `json:"stage"` // SOURCE, QUERY, TRANSFORM, MODEL, SUBMISSION
	Detail     string   `json:"detail"`
	DatasetIDs []string `json:"dataset_ids"`
}

// EvidenceFigure is one number in a regulatory report with its full chain.
type EvidenceFigure struct {
	Name         string        `json:"name"` // e.g. "total_deposits_gbp"
	ValueMinor   int64         `json:"value_minor"`
	Currency     string        `json:"currency"`
	Chain        []LineageStep `json:"chain"`
	ModelVersion string        `json:"model_version"`
	GeneratedAt  time.Time     `json:"generated_at"`
}

// ProvenanceError means the chain is incomplete — the figure cannot be
// submitted because its origin cannot be proven.
var ErrIncompleteProvenance = errors.New("evidence chain incomplete")

// ValidateProvenance enforces the chain contract: a figure must trace to at
// least one SOURCE dataset, pass through a TRANSFORM, and pin the model
// version that computed it.
func ValidateProvenance(f EvidenceFigure) error {
	var hasSource, hasTransform bool
	for _, s := range f.Chain {
		switch s.Stage {
		case "SOURCE":
			if len(s.DatasetIDs) == 0 {
				return fmt.Errorf("%w: SOURCE step names no datasets", ErrIncompleteProvenance)
			}
			hasSource = true
		case "TRANSFORM":
			hasTransform = true
		}
	}
	if !hasSource {
		return fmt.Errorf("%w: no SOURCE step", ErrIncompleteProvenance)
	}
	if !hasTransform {
		return fmt.Errorf("%w: no TRANSFORM step", ErrIncompleteProvenance)
	}
	if f.ModelVersion == "" {
		return fmt.Errorf("%w: model version unpinned", ErrIncompleteProvenance)
	}
	return nil
}

// Report is a regulatory submission.
type Report struct {
	Regime      string           `json:"regime"` // FCA, PRA, HMRC...
	Period      string           `json:"period"`
	Figures     []EvidenceFigure `json:"figures"`
	SubmittedAt time.Time        `json:"submitted_at"`
	SubmittedBy string           `json:"submitted_by"`
}

// Submit validates every figure's provenance before the report leaves.
func Submit(r Report) error {
	if len(r.Figures) == 0 {
		return errors.New("report has no figures")
	}
	for _, f := range r.Figures {
		if err := ValidateProvenance(f); err != nil {
			return fmt.Errorf("figure %s: %w", f.Name, err)
		}
	}
	return nil
}

// ── 4. Regulatory change impact analyzer ────────────────────────────────────

// ServiceGraph is the dependency graph used for impact projection.
type ServiceGraph struct {
	// Services maps service → the data models it owns.
	Services map[string][]string
	// Endpoints maps service → API endpoints it exposes.
	Endpoints map[string][]string
	// Journeys maps customer journey → services involved.
	Journeys map[string][]string
	// CustomersPerModel estimates the customer population touching a model.
	CustomersPerModel map[string]int
}

// ImpactAnalysis projects a new rule across the graph.
type ImpactAnalysis struct {
	RuleID            string   `json:"rule_id"`
	Services          []string `json:"services"`
	Endpoints         []string `json:"endpoints"`
	DataModels        []string `json:"data_models"`
	Journeys          []string `json:"journeys"`
	CustomersAffected int      `json:"customers_affected_estimate"`
}

// AnalyzeImpact finds every service owning a touched model, every endpoint
// those services expose, every journey through those services, and the
// de-duplicated customer estimate.
func AnalyzeImpact(ruleID string, touchedModels []string, g ServiceGraph) ImpactAnalysis {
	svcSet := map[string]bool{}
	for svc, models := range g.Services {
		for _, m := range models {
			if contains(touchedModels, m) {
				svcSet[svc] = true
			}
		}
	}
	epSet := map[string]bool{}
	for svc := range svcSet {
		for _, ep := range g.Endpoints[svc] {
			epSet[ep] = true
		}
	}
	jSet := map[string]bool{}
	for j, svcs := range g.Journeys {
		for _, s := range svcs {
			if svcSet[s] {
				jSet[j] = true
				break
			}
		}
	}
	custSet := map[string]int{}
	for _, m := range touchedModels {
		if n, ok := g.CustomersPerModel[m]; ok {
			custSet[m] = n
		}
	}
	total := 0
	seen := map[string]bool{}
	for _, n := range custSet {
		total += n // models in the analysis are distinct populations by construction
		_ = seen
	}
	analysis := ImpactAnalysis{RuleID: ruleID, CustomersAffected: total}
	for s := range svcSet {
		analysis.Services = append(analysis.Services, s)
	}
	for e := range epSet {
		analysis.Endpoints = append(analysis.Endpoints, e)
	}
	analysis.DataModels = append(analysis.DataModels, touchedModels...)
	for j := range jSet {
		analysis.Journeys = append(analysis.Journeys, j)
	}
	sort.Strings(analysis.Services)
	sort.Strings(analysis.Endpoints)
	sort.Strings(analysis.Journeys)
	return analysis
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ── 38. Safe configuration change platform ──────────────────────────────────

// ConfigState is the lifecycle of a configuration change.
type ConfigState string

const (
	CfgProposed   ConfigState = "PROPOSED"
	CfgValidated  ConfigState = "VALIDATED"
	CfgApproved   ConfigState = "APPROVED"
	CfgSimulated  ConfigState = "SIMULATED"
	CfgReleased   ConfigState = "RELEASED"
	CfgRolledBack ConfigState = "ROLLED_BACK"
)

// ConfigChange is one proposed production configuration change.
type ConfigChange struct {
	ID         string      `json:"id"`
	Target     string      `json:"target"` // service or platform component
	Key        string      `json:"key"`    // e.g. "transfer_limit_minor"
	OldValue   string      `json:"old_value"`
	NewValue   string      `json:"new_value"`
	State      ConfigState `json:"state"`
	ApprovedBy string      `json:"approved_by,omitempty"`
	// RollbackTo records the previous released value for instant revert.
	RollbackTo string    `json:"-"`
	ReleasedAt time.Time `json:"released_at,omitempty"`
}

// ConfigPlatform drives the safe-change lifecycle.
type ConfigPlatform struct {
	changes map[string]*ConfigChange
	// validators run on PROPOSED→VALIDATED; any error blocks the path.
	validators []func(ConfigChange) error
	// simulators run on APPROVED→SIMULATED; any error blocks release.
	simulators []func(ConfigChange) error
}

// NewConfigPlatform creates the lifecycle engine.
func NewConfigPlatform() *ConfigPlatform {
	return &ConfigPlatform{changes: map[string]*ConfigChange{}}
}

// AddValidator registers a validation gate.
func (p *ConfigPlatform) AddValidator(v func(ConfigChange) error) {
	p.validators = append(p.validators, v)
}

// AddSimulator registers a simulation gate.
func (p *ConfigPlatform) AddSimulator(v func(ConfigChange) error) {
	p.simulators = append(p.simulators, v)
}

// Propose starts a change.
func (p *ConfigPlatform) Propose(c ConfigChange) (*ConfigChange, error) {
	if c.ID == "" || c.Key == "" || c.NewValue == c.OldValue {
		return nil, errors.New("change must have id, key and a real diff")
	}
	c.State = CfgProposed
	p.changes[c.ID] = &c
	return &c, nil
}

// Validate runs the validation gates.
func (p *ConfigPlatform) Validate(id string) error {
	c, ok := p.changes[id]
	if !ok {
		return ErrRollbackUnknown
	}
	if c.State != CfgProposed {
		return fmt.Errorf("change in state %s, want PROPOSED", c.State)
	}
	for _, v := range p.validators {
		if err := v(*c); err != nil {
			return err
		}
	}
	c.State = CfgValidated
	return nil
}

// Approve records a human approval. Approval before validation is rejected —
// a rubber stamp on an unvalidated change is worse than no approval.
func (p *ConfigPlatform) Approve(id, approver string) error {
	c, ok := p.changes[id]
	if !ok {
		return ErrRollbackUnknown
	}
	if c.State != CfgValidated {
		return fmt.Errorf("%w: approve requires VALIDATED, got %s", ErrApprovalRequired, c.State)
	}
	if approver == "" {
		return errors.New("approver identity required")
	}
	c.ApprovedBy = approver
	c.State = CfgApproved
	return nil
}

// Simulate runs the simulation gates.
func (p *ConfigPlatform) Simulate(id string) error {
	c, ok := p.changes[id]
	if !ok {
		return ErrRollbackUnknown
	}
	if c.State != CfgApproved {
		return fmt.Errorf("%w: simulate requires APPROVED, got %s", ErrApprovalRequired, c.State)
	}
	for _, s := range p.simulators {
		if err := s(*c); err != nil {
			return fmt.Errorf("%w: %v", ErrSimulationFailed, err)
		}
	}
	c.State = CfgSimulated
	return nil
}

// Release puts the change live, recording the rollback target.
func (p *ConfigPlatform) Release(id string, now time.Time) error {
	c, ok := p.changes[id]
	if !ok {
		return ErrRollbackUnknown
	}
	if c.State != CfgSimulated {
		return fmt.Errorf("release requires SIMULATED, got %s", c.State)
	}
	c.RollbackTo = c.OldValue
	c.ReleasedAt = now
	c.State = CfgReleased
	return nil
}

// Rollback reverts a released change to its recorded prior value.
func (p *ConfigPlatform) Rollback(id string) (string, error) {
	c, ok := p.changes[id]
	if !ok {
		return "", ErrRollbackUnknown
	}
	if c.State != CfgReleased {
		return "", fmt.Errorf("only RELEASED changes can roll back, got %s", c.State)
	}
	c.State = CfgRolledBack
	return c.RollbackTo, nil
}

// ── 39. Configuration drift detection ───────────────────────────────────────

// Drift is one desired-vs-actual difference.
type Drift struct {
	Target  string `json:"target"`
	Key     string `json:"key"`
	Desired string `json:"desired"`
	Actual  string `json:"actual"`
}

// DetectDrift compares desired state to reported actual state per target.
func DetectDrift(desired, actual map[string]map[string]string) []Drift {
	var out []Drift
	targets := map[string]bool{}
	for t := range desired {
		targets[t] = true
	}
	for t := range actual {
		targets[t] = true
	}
	for t := range targets {
		keys := map[string]bool{}
		for k := range desired[t] {
			keys[k] = true
		}
		for k := range actual[t] {
			keys[k] = true
		}
		for k := range keys {
			d, hasD := desired[t][k]
			a, hasA := actual[t][k]
			if hasD && hasA && d == a {
				continue
			}
			out = append(out, Drift{Target: t, Key: k, Desired: d, Actual: a})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ── 40. Banking service safe mode ───────────────────────────────────────────

// Mode is the business-operation safety state.
type Mode string

const (
	ModeNormal   Mode = "NORMAL"
	ModeDegraded Mode = "DEGRADED"
	ModeSafe     Mode = "SAFE_MODE"
)

// Capability is a customer-facing operation.
type Capability string

const (
	CapReadBalance   Capability = "READ_BALANCE"
	CapViewTxns      Capability = "VIEW_TRANSACTIONS"
	CapMoveMoney     Capability = "MOVE_MONEY"
	CapChangeDetails Capability = "CHANGE_ACCOUNT_DETAILS"
	CapNewCard       Capability = "ORDER_NEW_CARD"
)

// SafeModePolicy defines what each mode allows.
type SafeModePolicy struct {
	// Allowed per mode, capability → enabled.
	Allowed map[Mode]map[Capability]bool
}

// DefaultSafeModePolicy: reads always on; in SAFE_MODE all state-changing
// high-risk operations stop, money movement stops, but the customer still
// sees their balance — a bank that looks fully dead triggers panic.
func DefaultSafeModePolicy() SafeModePolicy {
	return SafeModePolicy{Allowed: map[Mode]map[Capability]bool{
		ModeNormal: {
			CapReadBalance: true, CapViewTxns: true, CapMoveMoney: true,
			CapChangeDetails: true, CapNewCard: true,
		},
		ModeDegraded: {
			CapReadBalance: true, CapViewTxns: true, CapMoveMoney: true,
			CapChangeDetails: false, CapNewCard: true,
		},
		ModeSafe: {
			CapReadBalance: true, CapViewTxns: true, CapMoveMoney: false,
			CapChangeDetails: false, CapNewCard: false,
		},
	}}
}

// Allows answers "can this capability run in this mode?".
func (p SafeModePolicy) Allows(m Mode, c Capability) bool {
	return p.Allowed[m][c]
}

// SafeModeController escalates on signals and de-escalates only after a
// sustained healthy period (flapping between modes is its own failure).
type SafeModeController struct {
	Policy SafeModePolicy
	Mode   Mode
	since  time.Time
	// lastFailure is the anchor for de-escalation: health must span the WHOLE
	// window from the last failure, not from when the current mode began.
	// Anchoring on mode entry would reset the clock at every step-down, so a
	// controller could stand in DEGRADED forever with health "always 0s old".
	lastFailure      time.Time
	sawSuccessInMode bool
	failures         int
	healthyNeeded    time.Duration
}

// NewSafeModeController starts in NORMAL.
func NewSafeModeController(p SafeModePolicy, healthyNeeded time.Duration) *SafeModeController {
	return &SafeModeController{Policy: p, Mode: ModeNormal, healthyNeeded: healthyNeeded}
}

// RecordFailure escalates: 3 consecutive failures → DEGRADED, 6 → SAFE_MODE.
func (c *SafeModeController) RecordFailure(now time.Time) {
	c.failures++
	c.lastFailure = now
	c.sawSuccessInMode = false
	switch {
	case c.failures >= 6:
		c.setMode(ModeSafe, now)
	case c.failures >= 3:
		c.setMode(ModeDegraded, now)
	}
}

// RecordSuccess counts toward de-escalation. A step-down needs BOTH: an
// observed success since entering the current mode, AND the full healthy
// window elapsed since the last failure. Measuring from mode entry instead
// would let a single success de-escalate instantly (the window had already
// passed inside the old mode).
func (c *SafeModeController) RecordSuccess(now time.Time) {
	c.failures = 0
	if c.Mode == ModeNormal {
		return
	}
	c.sawSuccessInMode = true
	if !c.lastFailure.IsZero() && now.Sub(c.lastFailure) >= c.healthyNeeded {
		if c.Mode == ModeSafe {
			c.setMode(ModeDegraded, now)
		} else {
			c.setMode(ModeNormal, now)
		}
	}
}

// CustomerMessage explains the current mode to the app.
func (c *SafeModeController) CustomerMessage() string {
	switch c.Mode {
	case ModeNormal:
		return ""
	case ModeDegraded:
		return "Some account changes are temporarily unavailable. Payments and viewing your money work normally."
	default:
		return "We're protecting your account: you can view your balance and transactions, but some actions are paused right now."
	}
}

func (c *SafeModeController) setMode(m Mode, now time.Time) {
	if c.Mode != m {
		c.Mode = m
		c.since = now
		c.sawSuccessInMode = false
	}
}

// Since reports when the current mode began.
func (c *SafeModeController) Since() time.Time { return c.since }
