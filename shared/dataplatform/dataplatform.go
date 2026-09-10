// Package dataplatform implements Nexora's data-platform governance suite —
// the problems that appear at 100+ teams and 12,000+ models:
//
//  26. Data quality contracts: a dataset's consumer declares measurable
//     expectations (freshness, null rate, duplicate rate, row volume drift).
//     Violations BLOCK the pipeline: a broken contract quarantines the
//     dataset version instead of silently feeding downstream models.
//
//  27. Data product SLA dashboard: every dataset is a product with an owner,
//     freshness, quality verdict, schema hash and open incidents.
//
//  28. Privacy-aware query gateway: every query is evaluated against who is
//     asking, for what purpose, in which environment, over which dataset.
//     Verdicts: ALLOW / REDACT (PII columns masked) / AGGREGATE (k-anonymity
//     minimum group size) / DENY.
//
//  29. Production data access session recording: sensitive access produces a
//     structured, tamper-evident record (who, what customer, why, which
//     fields, ticket reference) — the audit trail an FCA visit asks for.
//
//  30. Migration safety platform: schema/data migrations run as a gated
//     pipeline (validate → estimate → canary → apply → verify) with pause,
//     rollback and repair states, and an expand-migrate-contract discipline
//     check that refuses to drop a column still read by any consumer.
package dataplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrContractBroken = errors.New("data contract broken")
	ErrDenied         = errors.New("query denied by policy")
	ErrMigrationGate  = errors.New("migration gate failed")
)

// ── 26. Data quality contracts ──────────────────────────────────────────────

// QualityContract is the measurable expectation a consumer declares.
type QualityContract struct {
	Dataset        string
	Consumer       string
	MaxFreshness   time.Duration // age of newest row
	MaxNullRatePct float64       // % of NULLs tolerated in key columns
	MaxDupRatePct  float64       // % duplicate primary keys tolerated
	// MinRows/MaxRows catch volume collapses and explosions (a partition
	// landing at 3% of its usual size is a broken upstream, not a quiet day).
	MinRows int64
	MaxRows int64
}

// QualityMeasurement is what the pipeline observed.
type QualityMeasurement struct {
	Dataset      string
	CheckedAt    time.Time
	NewestRowAge time.Duration
	NullRatePct  float64
	DupRatePct   float64
	Rows         int64
}

// Violation is one failed expectation.
type Violation struct {
	Field    string  `json:"field"`
	Observed float64 `json:"observed"`
	Limit    float64 `json:"limit"`
	Detail   string  `json:"detail"`
}

// QualityVerdict is the gate decision for one measurement.
type QualityVerdict struct {
	Passed     bool
	Violations []Violation
	// Quarantine is true when the dataset version must NOT be published to
	// downstream models.
	Quarantine bool
}

// EvaluateContract checks a measurement against a contract.
func EvaluateContract(c QualityContract, m QualityMeasurement) QualityVerdict {
	var vs []Violation
	if m.NewestRowAge > c.MaxFreshness {
		vs = append(vs, Violation{"freshness", m.NewestRowAge.Seconds(), c.MaxFreshness.Seconds(),
			fmt.Sprintf("newest row %s old, contract allows %s", m.NewestRowAge, c.MaxFreshness)})
	}
	if m.NullRatePct > c.MaxNullRatePct {
		vs = append(vs, Violation{"null_rate", m.NullRatePct, c.MaxNullRatePct,
			fmt.Sprintf("null rate %.3f%% > %.3f%%", m.NullRatePct, c.MaxNullRatePct)})
	}
	if m.DupRatePct > c.MaxDupRatePct {
		vs = append(vs, Violation{"duplicate_rate", m.DupRatePct, c.MaxDupRatePct,
			fmt.Sprintf("duplicate rate %.3f%% > %.3f%%", m.DupRatePct, c.MaxDupRatePct)})
	}
	if c.MinRows > 0 && m.Rows < c.MinRows {
		vs = append(vs, Violation{"row_volume_min", float64(m.Rows), float64(c.MinRows),
			fmt.Sprintf("rows %d < floor %d — possible upstream collapse", m.Rows, c.MinRows)})
	}
	if c.MaxRows > 0 && m.Rows > c.MaxRows {
		vs = append(vs, Violation{"row_volume_max", float64(m.Rows), float64(c.MaxRows),
			fmt.Sprintf("rows %d > ceiling %d — possible duplication upstream", m.Rows, c.MaxRows)})
	}
	return QualityVerdict{Passed: len(vs) == 0, Violations: vs, Quarantine: len(vs) > 0}
}

// ── 27. Data product SLA dashboard ──────────────────────────────────────────

// HealthState is the product-level health of a dataset.
type HealthState string

const (
	HealthHealthy  HealthState = "HEALTHY"
	HealthDegraded HealthState = "DEGRADED"
	HealthBroken   HealthState = "BROKEN"
	HealthUnowned  HealthState = "UNOWNED" // an unowned dataset is an incident waiting to happen
)

// Incident is an open quality incident.
type Incident struct {
	ID       string
	OpenedAt time.Time
	Severity string
	Summary  string
}

// DataProduct is a dataset treated as a product.
type DataProduct struct {
	Name        string
	Owner       string
	Tier        string // "CRITICAL", "STANDARD", "INTERNAL"
	SchemaHash  string
	LastPublish time.Time
	OpenIssues  []Incident
	lastVerdict *QualityVerdict
}

// DatasetRegistry tracks data products.
type DatasetRegistry struct {
	products map[string]*DataProduct
}

func NewDatasetRegistry() *DatasetRegistry {
	return &DatasetRegistry{products: map[string]*DataProduct{}}
}

// Register requires an owner: unowned datasets are representable but visibly
// marked UNOWNED on the dashboard.
func (r *DatasetRegistry) Register(name, owner, tier string, schema map[string]string, now time.Time) *DataProduct {
	p := &DataProduct{Name: name, Owner: owner, Tier: tier, SchemaHash: SchemaHash(schema), LastPublish: now}
	r.products[name] = p
	return p
}

// SchemaHash is a deterministic fingerprint of a schema (canonical
// column:type ordering) so consumers can detect schema drift.
func SchemaHash(schema map[string]string) string {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s:%s;", k, schema[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// RecordVerdict stores the latest quality gate result.
func (r *DatasetRegistry) RecordVerdict(name string, v QualityVerdict) error {
	p, ok := r.products[name]
	if !ok {
		return fmt.Errorf("unknown dataset %s", name)
	}
	p.lastVerdict = &v
	return nil
}

// OpenIncident records an incident against a dataset.
func (r *DatasetRegistry) OpenIncident(name string, inc Incident) error {
	p, ok := r.products[name]
	if !ok {
		return fmt.Errorf("unknown dataset %s", name)
	}
	p.OpenIssues = append(p.OpenIssues, inc)
	return nil
}

// Health computes the dashboard verdict for a product. Precedence:
// unowned → BROKEN contract/incident → open incidents → DEGRADED → HEALTHY.
func (r *DatasetRegistry) Health(name string) (HealthState, error) {
	p, ok := r.products[name]
	if !ok {
		return "", fmt.Errorf("unknown dataset %s", name)
	}
	if p.Owner == "" {
		return HealthUnowned, nil
	}
	if (p.lastVerdict != nil && p.lastVerdict.Quarantine) || p.Tier == "" {
		return HealthBroken, nil
	}
	if len(p.OpenIssues) > 0 {
		return HealthDegraded, nil
	}
	return HealthHealthy, nil
}

// ── 28. Privacy-aware query gateway ─────────────────────────────────────────

// QueryVerdict is the gateway decision.
type QueryVerdict string

const (
	QueryAllow     QueryVerdict = "ALLOW"
	QueryRedact    QueryVerdict = "REDACT"
	QueryAggregate QueryVerdict = "AGGREGATE"
	QueryDeny      QueryVerdict = "DENY"
)

// QueryRequest describes a query's context — the gateway decides on context,
// not SQL text (SQL parsing is the wrong layer for access control).
type QueryRequest struct {
	Engineer    string
	Purpose     string // "SUPPORT_TICKET", "FRAUD_INVESTIGATION", "ANALYTICS", ...
	Environment string // "PRODUCTION", "STAGING", "SANDBOX"
	Dataset     string
	PIIColumns  []string // PII columns the query touches
	NeedsRawPII bool
	TicketRef   string
}

// GovernancePolicy is the gateway's rule set.
type GovernancePolicy struct {
	// ApprovedPurposes that may touch PII at all.
	ApprovedPurposes map[string]bool
	// MinGroupSize for AGGREGATE verdicts (k-anonymity floor).
	MinGroupSize int
	// Production raw-PII always requires a ticket reference.
	RequireTicketInProduction bool
}

func DefaultGovernancePolicy() GovernancePolicy {
	return GovernancePolicy{
		ApprovedPurposes:          map[string]bool{"SUPPORT_TICKET": true, "FRAUD_INVESTIGATION": true},
		MinGroupSize:              25,
		RequireTicketInProduction: true,
	}
}

// EvaluateQuery decides the verdict.
func EvaluateQuery(req QueryRequest, p GovernancePolicy) (QueryVerdict, string) {
	// Analytics in production over PII without an approved purpose: DENY.
	if !p.ApprovedPurposes[req.Purpose] && len(req.PIIColumns) > 0 {
		return QueryDeny, fmt.Sprintf("purpose %s is not approved for PII access", req.Purpose)
	}
	if p.RequireTicketInProduction && req.Environment == "PRODUCTION" && len(req.PIIColumns) > 0 && req.TicketRef == "" {
		return QueryDeny, "production PII access requires a ticket reference"
	}
	if len(req.PIIColumns) == 0 {
		return QueryAllow, "no PII touched"
	}
	if req.NeedsRawPII && p.ApprovedPurposes[req.Purpose] {
		return QueryRedact, "raw PII redacted; keys/hashes returned instead"
	}
	return QueryAggregate, fmt.Sprintf("results aggregated at k≥%d", p.MinGroupSize)
}

// ── 29. Production data access session recording ────────────────────────────

// AccessEvent is one recorded sensitive-access action.
type AccessEvent struct {
	At        time.Time
	Engineer  string
	Customer  string
	Fields    []string
	Reason    string
	TicketRef string
}

// AccessSession groups events with a chain hash — tampering with any record
// breaks every subsequent hash, so "edit the log" is detectable.
type AccessSession struct {
	ID        string
	Engineer  string
	TicketRef string
	Events    []AccessEvent
	ChainHash string
}

// AccessRecorder records sessions.
type AccessRecorder struct {
	sessions map[string]*AccessSession
}

func NewAccessRecorder() *AccessRecorder {
	return &AccessRecorder{sessions: map[string]*AccessSession{}}
}

// StartSession opens a recorded session; a ticket reference is mandatory —
// unexplained production access is the thing this platform exists to stop.
func (a *AccessRecorder) StartSession(id, engineer, ticketRef string, now time.Time) (*AccessSession, error) {
	if ticketRef == "" {
		return nil, errors.New("session requires a ticket reference")
	}
	s := &AccessSession{ID: id, Engineer: engineer, TicketRef: ticketRef, ChainHash: ""}
	a.sessions[id] = s
	return s, nil
}

// Record appends an event and advances the chain hash.
func (a *AccessSession) Record(ev AccessEvent) {
	ev.At = time.Now().UTC()
	a.Events = append(a.Events, ev)
	h := sha256.Sum256([]byte(a.ChainHash + "|" + ev.Engineer + "|" + ev.Customer + "|" + strings.Join(ev.Fields, ",") + "|" + ev.Reason + "|" + ev.At.Format(time.RFC3339Nano)))
	a.ChainHash = hex.EncodeToString(h[:])
}

// VerifyChain recomputes the chain to detect tampering.
func (a *AccessSession) VerifyChain() bool {
	hash := ""
	for _, ev := range a.Events {
		h := sha256.Sum256([]byte(hash + "|" + ev.Engineer + "|" + ev.Customer + "|" + strings.Join(ev.Fields, ",") + "|" + ev.Reason + "|" + ev.At.Format(time.RFC3339Nano)))
		hash = hex.EncodeToString(h[:])
	}
	return hash == a.ChainHash
}

// ── 30. Migration safety platform ───────────────────────────────────────────

// MigrationStage is the gated pipeline.
type MigrationStage string

const (
	MigSubmitted  MigrationStage = "SUBMITTED"
	MigValidating MigrationStage = "VALIDATING"
	MigEstimated  MigrationStage = "CAPACITY_ESTIMATED"
	MigCanary     MigrationStage = "CANARY"
	MigApplying   MigrationStage = "APPLYING"
	MigVerifying  MigrationStage = "VERIFYING"
	MigComplete   MigrationStage = "COMPLETE"
	MigPaused     MigrationStage = "PAUSED"
	MigRolling    MigrationStage = "ROLLING_BACK"
	MigRolledBack MigrationStage = "ROLLED_BACK"
	MigRepairing  MigrationStage = "REPAIRING"
)

// MigrationRequest is a submitted migration.
type MigrationRequest struct {
	ID        string
	Target    string // table/dataset
	Statement string
	// DropColumn / Contractive operations need consumer evidence.
	DropColumn string
	// ReadByConsumers lists consumers still reading the column to drop.
	ReadByConsumers []string
	EstRows         int64
}

// MigrationState is the engine's tracked state.
type MigrationState struct {
	Req       MigrationRequest
	Stage     MigrationStage
	History   []string
	CanaryPct float64
	CanaryBad bool
	VerifyBad bool
}

// MigrationEngine runs the gated pipeline.
type MigrationEngine struct {
	states map[string]*MigrationState
}

func NewMigrationEngine() *MigrationEngine {
	return &MigrationEngine{states: map[string]*MigrationState{}}
}

// Submit validates at the door: contractive changes without consumer
// evidence never enter the pipeline — the classic "dropped a column the
// reporting job still read" outage is unrepresentable here.
func (e *MigrationEngine) Submit(req MigrationRequest, now time.Time) (*MigrationState, error) {
	if req.DropColumn != "" && len(req.ReadByConsumers) > 0 {
		return nil, fmt.Errorf("%w: column %s still read by %d consumers — migrate consumers first (expand-migrate-contract)",
			ErrMigrationGate, req.DropColumn, len(req.ReadByConsumers))
	}
	if req.EstRows < 0 {
		return nil, fmt.Errorf("%w: negative row estimate", ErrMigrationGate)
	}
	st := &MigrationState{Req: req, Stage: MigSubmitted, History: []string{fmt.Sprintf("%s SUBMITTED", now.Format(time.RFC3339))}}
	e.states[req.ID] = st
	return st, nil
}

func (e *MigrationEngine) state(id string) (*MigrationState, error) {
	st, ok := e.states[id]
	if !ok {
		return nil, fmt.Errorf("unknown migration %s", id)
	}
	return st, nil
}

func (st *MigrationState) advance(to MigrationStage, note string) {
	st.Stage = to
	st.History = append(st.History, note)
}

// Validate moves SUBMITTED → VALIDATING → CAPACITY_ESTIMATED.
func (e *MigrationEngine) Validate(id string) error {
	st, err := e.state(id)
	if err != nil {
		return err
	}
	if st.Stage != MigSubmitted {
		return fmt.Errorf("%w: cannot validate from %s", ErrMigrationGate, st.Stage)
	}
	st.advance(MigValidating, "schema validation started")
	st.advance(MigEstimated, fmt.Sprintf("capacity estimated for %d rows", st.Req.EstRows))
	return nil
}

// Canary runs the migration against a slice of traffic. Failure marks the
// canary bad; Apply refuses to proceed.
func (e *MigrationEngine) Canary(id string, pct float64, bad bool) error {
	st, err := e.state(id)
	if err != nil {
		return err
	}
	if st.Stage != MigEstimated {
		return fmt.Errorf("%w: canary requires capacity estimate, at %s", ErrMigrationGate, st.Stage)
	}
	if pct <= 0 || pct > 100 {
		return fmt.Errorf("%w: canary pct %v out of range", ErrMigrationGate, pct)
	}
	st.CanaryPct = pct
	st.CanaryBad = bad
	note := fmt.Sprintf("canary %.1f%% passed", pct)
	if bad {
		note = fmt.Sprintf("canary %.1f%% FAILED", pct)
	}
	st.advance(MigCanary, note)
	return nil
}

// Apply promotes canary → full application; a failed canary forces a
// rollback decision first.
func (e *MigrationEngine) Apply(id string) error {
	st, err := e.state(id)
	if err != nil {
		return err
	}
	if st.Stage != MigCanary {
		return fmt.Errorf("%w: apply requires canary stage, at %s", ErrMigrationGate, st.Stage)
	}
	if st.CanaryBad {
		return fmt.Errorf("%w: canary failed — rollback or repair before applying", ErrMigrationGate)
	}
	st.advance(MigApplying, "applying to full dataset")
	st.advance(MigVerifying, "verification queries running")
	if st.VerifyBad {
		st.advance(MigRepairing, "verification mismatch — repair scheduled")
		return nil
	}
	st.advance(MigComplete, "migration complete")
	return nil
}

// MarkVerifyFailed injects a verification failure before completion.
func (e *MigrationEngine) MarkVerifyFailed(id string) error {
	st, err := e.state(id)
	if err != nil {
		return err
	}
	if st.Stage != MigCanary && st.Stage != MigApplying && st.Stage != MigVerifying {
		return fmt.Errorf("%w: verification failure only meaningful while running, at %s", ErrMigrationGate, st.Stage)
	}
	st.VerifyBad = true
	return nil
}

// Pause halts a running migration (resumable from the same stage).
func (e *MigrationEngine) Pause(id string) error {
	st, err := e.state(id)
	if err != nil {
		return err
	}
	switch st.Stage {
	case MigApplying, MigVerifying, MigCanary:
		st.History = append(st.History, "PAUSED at "+string(st.Stage))
		st.Stage = MigPaused
		return nil
	default:
		return fmt.Errorf("%w: cannot pause from %s", ErrMigrationGate, st.Stage)
	}
}

// Resume continues a paused migration from its recorded stage.
func (e *MigrationEngine) Resume(id string) (MigrationStage, error) {
	st, err := e.state(id)
	if err != nil {
		return "", err
	}
	if st.Stage != MigPaused {
		return st.Stage, fmt.Errorf("%w: not paused", ErrMigrationGate)
	}
	// Resume re-enters the paused-from stage: the last history entry before
	// PAUSED records it.
	st.Stage = MigCanary
	if len(st.History) >= 2 {
		last := st.History[len(st.History)-2]
		for _, s := range []MigrationStage{MigCanary, MigApplying, MigVerifying} {
			if strings.Contains(last, string(s)) {
				st.Stage = s
			}
		}
	}
	st.History = append(st.History, "resumed at "+string(st.Stage))
	return st.Stage, nil
}

// Rollback unwinds a migration; allowed any time before COMPLETE.
func (e *MigrationEngine) Rollback(id string) error {
	st, err := e.state(id)
	if err != nil {
		return err
	}
	if st.Stage == MigComplete {
		return fmt.Errorf("%w: completed migration cannot be rolled back — write a reverse migration", ErrMigrationGate)
	}
	st.advance(MigRolling, "rollback started")
	st.advance(MigRolledBack, "rollback complete")
	return nil
}

// Stage reports the current stage.
func (e *MigrationEngine) Stage(id string) (MigrationStage, error) {
	st, err := e.state(id)
	if err != nil {
		return "", err
	}
	return st.Stage, nil
}
