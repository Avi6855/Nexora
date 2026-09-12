// Package complianceops implements Nexora's compliance operations platform:
//
//  19. Evidence bundles: an investigation's evidence graph (customers,
//     transactions, devices, payments, decisions, documents) is sealed
//     into an immutable bundle carrying a manifest, source refs,
//     timestamps and sha256 hashes, with Verify for tamper detection.
//
//  20. Case snapshots: the customer-state blob is pinned at investigation
//     start; later reads return the pinned snapshot so current-state
//     drift never leaks into the case file.
//
//  21. SLA engine: per-type policies (KYC, FINCRIME, COMPLAINT,
//     PAYMENT_INVESTIGATION) carry deadline, priority and escalation.
//     Tick emits WARNING / ASSIGNED / BREACH / ESCALATED events.
//
//  22. Sampling: risk-based sample of N decisions from a population.
//     Weights: high-risk x3, borderline x2, new-policy/model x2,
//     unusual-segment x2. Deterministic under an explicit seed.
//
//  23. Coverage analyzer: requirements registry (id -> implemented rule,
//     service, test ref, monitor ref) with per-requirement coverage
//     (IMPLEMENTED / TESTED / MONITORED) plus gaps.
//
//  24. Control test runner: control tests evaluated against synthetic
//     scenarios (restricted customer/payment, threshold breach, missing
//     evidence) produce PASS/FAIL run reports.
package complianceops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for unknown bundles, snapshots, cases, requirements, controls and runs.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned for malformed requests.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict is returned for duplicate or illegal state transitions.
	ErrConflict = errors.New("conflict")
	// ErrTampered is returned when bundle verification fails.
	ErrTampered = errors.New("bundle verification failed: tampered")
)

// ── 19. Evidence bundles ────────────────────────────────────────────────

// Evidence kinds collected into a bundle.
const (
	KindCustomer    = "customer"
	KindTransaction = "transaction"
	KindDevice      = "device"
	KindPayment     = "payment"
	KindDecision    = "decision"
	KindDocument    = "document"
)

// Bundle verdicts.
const (
	BundleValid    = "VALID"
	BundleTampered = "TAMPERED"
)

// EvidenceGraph is the investigation's evidence set.
type EvidenceGraph struct {
	Customers    []string `json:"customers"`
	Transactions []string `json:"transactions"`
	Devices      []string `json:"devices"`
	Payments     []string `json:"payments"`
	Decisions    []string `json:"decisions"`
	Documents    []string `json:"documents"`
}

// ManifestEntry is one sealed source ref.
type ManifestEntry struct {
	Kind       string    `json:"kind"`
	RefID      string    `json:"ref_id"`
	Hash       string    `json:"hash"`
	CapturedAt time.Time `json:"captured_at"`
}

// Bundle is the immutable evidence bundle.
type Bundle struct {
	ID              string          `json:"id"`
	InvestigationID string          `json:"investigation_id"`
	Entries         []ManifestEntry `json:"entries"`
	CreatedAt       time.Time       `json:"created_at"`
	BundleHash      string          `json:"bundle_hash"`
}

// hashRef hashes one source ref canonically.
func hashRef(kind, refID string, at time.Time) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + refID + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}

// hashBundle hashes the sorted entry hashes plus investigation and timestamp.
func hashBundle(investigationID string, entries []ManifestEntry, createdAt time.Time) string {
	var b strings.Builder
	b.WriteString(investigationID)
	b.WriteString("\x00")
	b.WriteString(createdAt.UTC().Format(time.RFC3339Nano))
	b.WriteString("\x00")
	for _, e := range entries {
		b.WriteString(e.Kind)
		b.WriteString("\x00")
		b.WriteString(e.RefID)
		b.WriteString("\x00")
		b.WriteString(e.Hash)
		b.WriteString("\x00")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// VerifyBundle recomputes every entry hash and the bundle hash.
// Nil or hash mismatch returns ErrTampered.
func VerifyBundle(b *Bundle) error {
	if b == nil {
		return fmt.Errorf("%w: nil bundle", ErrTampered)
	}
	for i := range b.Entries {
		e := &b.Entries[i]
		if e.Kind == "" || e.RefID == "" || e.Hash == "" || e.CapturedAt.IsZero() {
			return fmt.Errorf("%w: malformed manifest entry %d", ErrTampered, i)
		}
		if want := hashRef(e.Kind, e.RefID, e.CapturedAt); want != e.Hash {
			return fmt.Errorf("%w: entry %s/%s hash mismatch", ErrTampered, e.Kind, e.RefID)
		}
	}
	sorted := append([]ManifestEntry(nil), b.Entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].RefID < sorted[j].RefID
	})
	// Stored entries must already be in canonical order; recompute over
	// canonical order and compare with the sealed hash.
	if want := hashBundle(b.InvestigationID, sorted, b.CreatedAt); want != b.BundleHash {
		return fmt.Errorf("%w: bundle hash mismatch", ErrTampered)
	}
	return nil
}

// buildEntries flattens a graph into canonical manifest entries.
func buildEntries(g EvidenceGraph, now time.Time) []ManifestEntry {
	var out []ManifestEntry
	add := func(kind string, ids []string) {
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			out = append(out, ManifestEntry{Kind: kind, RefID: id, Hash: hashRef(kind, id, now), CapturedAt: now})
		}
	}
	add(KindCustomer, g.Customers)
	add(KindTransaction, g.Transactions)
	add(KindDevice, g.Devices)
	add(KindPayment, g.Payments)
	add(KindDecision, g.Decisions)
	add(KindDocument, g.Documents)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].RefID < out[j].RefID
	})
	return out
}

// ── 20. Case snapshots ──────────────────────────────────────────────────

// Snapshot pins the customer-state blob at investigation start.
type Snapshot struct {
	InvestigationID string    `json:"investigation_id"`
	CustomerID      string    `json:"customer_id"`
	State           string    `json:"state"`
	Hash            string    `json:"hash"`
	CapturedAt      time.Time `json:"captured_at"`
}

func hashState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// ── 21. SLA engine ──────────────────────────────────────────────────────

// Supported investigation types.
const (
	CaseTypeKYC                  = "KYC"
	CaseTypeFincrime             = "FINCRIME"
	CaseTypeComplaint            = "COMPLAINT"
	CaseTypePaymentInvestigation = "PAYMENT_INVESTIGATION"
)

// AllCaseTypes lists every supported SLA type.
var AllCaseTypes = []string{CaseTypeKYC, CaseTypeFincrime, CaseTypeComplaint, CaseTypePaymentInvestigation}

// SLA event kinds emitted by Tick.
const (
	EventWarning   = "WARNING"
	EventAssigned  = "ASSIGNED"
	EventBreach    = "BREACH"
	EventEscalated = "ESCALATED"
)

func validCaseType(t string) bool {
	switch t {
	case CaseTypeKYC, CaseTypeFincrime, CaseTypeComplaint, CaseTypePaymentInvestigation:
		return true
	}
	return false
}

// SLAPolicy carries deadline, priority and escalation per case type.
type SLAPolicy struct {
	CaseType      string        `json:"case_type"`
	Deadline      time.Duration `json:"deadline"`
	Priority      string        `json:"priority"`
	Escalation    string        `json:"escalation"`
	WarningBefore time.Duration `json:"warning_before"`
}

// SLAItem is one tracked investigation.
type SLAItem struct {
	ID         string    `json:"id"`
	CaseType   string    `json:"case_type"`
	CreatedAt  time.Time `json:"created_at"`
	DeadlineAt time.Time `json:"deadline_at"`
	Priority   string    `json:"priority"`
	Assignee   string    `json:"assignee"`
	Escalation string    `json:"escalation"`
	Warned     bool      `json:"-"`
	Breached   bool      `json:"-"`
	Escalated  bool      `json:"-"`
	Assigned   bool      `json:"-"`
}

// SLAEvent is one Tick emission.
type SLAEvent struct {
	ItemID   string    `json:"item_id"`
	Kind     string    `json:"kind"`
	CaseType string    `json:"case_type"`
	Detail   string    `json:"detail"`
	At       time.Time `json:"at"`
}

// ── 22. Sampling ────────────────────────────────────────────────────────

// SampleDecision is one population member with risk flags.
type SampleDecision struct {
	ID               string `json:"id"`
	HighRisk         bool   `json:"high_risk"`
	Borderline       bool   `json:"borderline"`
	NewPolicyOrModel bool   `json:"new_policy_or_model"`
	UnusualSegment   bool   `json:"unusual_segment"`
}

// WeightOf returns the risk weight: high-risk x3, borderline x2,
// new-policy/model x2, unusual-segment x2 (multiplicative, min 1).
func WeightOf(d SampleDecision) int {
	w := 1
	if d.HighRisk {
		w *= 3
	}
	if d.Borderline {
		w *= 2
	}
	if d.NewPolicyOrModel {
		w *= 2
	}
	if d.UnusualSegment {
		w *= 2
	}
	return w
}

// Sample draws N decisions without replacement using weights and a
// deterministic seed. The population order is normalised by ID so the
// same logical population always yields the same sample for a seed.
func Sample(pop []SampleDecision, n int, seed int64) ([]SampleDecision, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: sample size must be positive", ErrInvalid)
	}
	if len(pop) == 0 {
		return nil, fmt.Errorf("%w: population is empty", ErrInvalid)
	}
	if n > len(pop) {
		n = len(pop)
	}
	ordered := append([]SampleDecision(nil), pop...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	rng := rand.New(rand.NewSource(seed))
	remaining := append([]SampleDecision(nil), ordered...)
	var out []SampleDecision
	for len(out) < n && len(remaining) > 0 {
		total := 0
		for _, d := range remaining {
			total += WeightOf(d)
		}
		if total <= 0 {
			break
		}
		pick := rng.Intn(total)
		idx := 0
		for i, d := range remaining {
			pick -= WeightOf(d)
			if pick < 0 {
				idx = i
				break
			}
		}
		out = append(out, remaining[idx])
		remaining = append(remaining[:idx], remaining[idx+1:]...)
	}
	return out, nil
}

// ── 23. Coverage analyzer ───────────────────────────────────────────────

// Requirement maps one compliance requirement to its implementation.
type Requirement struct {
	ID         string `json:"id"`
	Rule       string `json:"rule"`
	Service    string `json:"service"`
	TestRef    string `json:"test_ref"`
	MonitorRef string `json:"monitor_ref"`
}

// CoverageEntry is the per-requirement coverage report.
type CoverageEntry struct {
	RequirementID string   `json:"requirement_id"`
	Implemented   bool     `json:"implemented"`
	Tested        bool     `json:"tested"`
	Monitored     bool     `json:"monitored"`
	Gaps          []string `json:"gaps"`
}

func coverageFor(r *Requirement) CoverageEntry {
	e := CoverageEntry{RequirementID: r.ID}
	e.Implemented = strings.TrimSpace(r.Rule) != ""
	e.Tested = strings.TrimSpace(r.TestRef) != ""
	e.Monitored = strings.TrimSpace(r.MonitorRef) != ""
	if !e.Implemented {
		e.Gaps = append(e.Gaps, "missing implementation")
	}
	if !e.Tested {
		e.Gaps = append(e.Gaps, "missing test")
	}
	if !e.Monitored {
		e.Gaps = append(e.Gaps, "missing monitor")
	}
	if e.Gaps == nil {
		e.Gaps = []string{}
	}
	return e
}

// ── 24. Control test runner ─────────────────────────────────────────────

// Synthetic control scenarios.
const (
	ScenarioRestrictedCustomer = "restricted_customer"
	ScenarioRestrictedPayment  = "restricted_payment"
	ScenarioThresholdBreach    = "threshold_breach"
	ScenarioMissingEvidence    = "missing_evidence"
	ScenarioClean              = "clean"
)

// AllScenarios lists every synthetic scenario.
var AllScenarios = []string{
	ScenarioRestrictedCustomer,
	ScenarioRestrictedPayment,
	ScenarioThresholdBreach,
	ScenarioMissingEvidence,
	ScenarioClean,
}

// Control verdicts.
const (
	VerdictAllow     = "ALLOW"
	VerdictBlock     = "BLOCK"
	VerdictChallenge = "CHALLENGE"
)

// Run verdicts.
const (
	RunPass = "PASS"
	RunFail = "FAIL"
)

func validScenario(s string) bool {
	for _, a := range AllScenarios {
		if s == a {
			return true
		}
	}
	return false
}

// EvaluateScenario is the synthetic control evaluator (fail-closed):
// restricted customer/payment, threshold breach and missing evidence
// BLOCK; clean traffic ALLOWs.
func EvaluateScenario(scenario string) string {
	switch scenario {
	case ScenarioRestrictedCustomer, ScenarioRestrictedPayment, ScenarioThresholdBreach, ScenarioMissingEvidence:
		return VerdictBlock
	default:
		return VerdictAllow
	}
}

// ControlTest is one registered control expectation.
type ControlTest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Scenario string `json:"scenario"`
	Expected string `json:"expected"`
}

// ControlResult is one evaluated control.
type ControlResult struct {
	TestID   string `json:"test_id"`
	Scenario string `json:"scenario"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Verdict  string `json:"verdict"`
}

// ControlRun is one full runner report.
type ControlRun struct {
	ID      string          `json:"id"`
	At      time.Time       `json:"at"`
	Results []ControlResult `json:"results"`
	Passed  int             `json:"passed"`
	Failed  int             `json:"failed"`
	Overall string          `json:"overall"`
	Total   int             `json:"total"`
}

// ── Store ───────────────────────────────────────────────────────────────

// Store is the mutex-guarded in-memory compliance-ops engine.
type Store struct {
	mu           sync.RWMutex
	seq          int
	bundles      map[string]*Bundle
	snapshots    map[string]*Snapshot
	currents     map[string]string
	slaPolicies  map[string]SLAPolicy
	slaItems     map[string]*SLAItem
	slaSeq       int
	requirements map[string]*Requirement
	controls     map[string]*ControlTest
	runs         map[string]*ControlRun
}

// NewStore builds an empty store.
func NewStore() *Store {
	return &Store{
		bundles:      map[string]*Bundle{},
		snapshots:    map[string]*Snapshot{},
		currents:     map[string]string{},
		slaPolicies:  map[string]SLAPolicy{},
		slaItems:     map[string]*SLAItem{},
		requirements: map[string]*Requirement{},
		controls:     map[string]*ControlTest{},
		runs:         map[string]*ControlRun{},
	}
}

// BuildBundle seals an investigation's evidence graph into an immutable bundle.
func (s *Store) BuildBundle(investigationID string, g EvidenceGraph, now time.Time) (*Bundle, error) {
	if strings.TrimSpace(investigationID) == "" {
		return nil, fmt.Errorf("%w: investigation id is required", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	entries := buildEntries(g, now)
	if entries == nil {
		entries = []ManifestEntry{}
	}
	b := &Bundle{
		ID:              fmt.Sprintf("bundle-%d", s.seq),
		InvestigationID: investigationID,
		Entries:         entries,
		CreatedAt:       now,
	}
	b.BundleHash = hashBundle(investigationID, entries, now)
	s.bundles[b.ID] = b
	out := *b
	out.Entries = append([]ManifestEntry(nil), b.Entries...)
	return &out, nil
}

// GetBundle returns a copy of a bundle.
func (s *Store) GetBundle(id string) (*Bundle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bundles[id]
	if !ok {
		return nil, fmt.Errorf("bundle %s: %w", id, ErrNotFound)
	}
	out := *b
	out.Entries = append([]ManifestEntry(nil), b.Entries...)
	return &out, nil
}

// Verify recomputes the sealed hashes: match -> VALID, else TAMPERED.
func (s *Store) Verify(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bundles[id]
	if !ok {
		return "", fmt.Errorf("bundle %s: %w", id, ErrNotFound)
	}
	if err := VerifyBundle(b); err != nil {
		return BundleTampered, nil
	}
	return BundleValid, nil
}

// SnapshotCase pins the customer-state blob at investigation start.
// A second snapshot for the same investigation conflicts (409): the
// pinned state is immutable.
func (s *Store) SnapshotCase(investigationID, customerID, state string, now time.Time) (*Snapshot, error) {
	if strings.TrimSpace(investigationID) == "" {
		return nil, fmt.Errorf("%w: investigation id is required", ErrInvalid)
	}
	if strings.TrimSpace(customerID) == "" {
		return nil, fmt.Errorf("%w: customer id is required", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snapshots[investigationID]; ok {
		return nil, fmt.Errorf("snapshot for %s: %w", investigationID, ErrConflict)
	}
	snap := &Snapshot{
		InvestigationID: investigationID,
		CustomerID:      customerID,
		State:           state,
		Hash:            hashState(state),
		CapturedAt:      now,
	}
	s.snapshots[investigationID] = snap
	if _, ok := s.currents[customerID]; !ok {
		s.currents[customerID] = state
	}
	out := *snap
	return &out, nil
}

// GetSnapshot returns the pinned snapshot (never current-state drift).
func (s *Store) GetSnapshot(investigationID string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[investigationID]
	if !ok {
		return nil, fmt.Errorf("snapshot for %s: %w", investigationID, ErrNotFound)
	}
	out := *snap
	return &out, nil
}

// SetCurrentState simulates upstream drift of the live customer record.
func (s *Store) SetCurrentState(customerID, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currents[customerID] = state
}

// GetCurrentState returns the live (drifted) customer record.
func (s *Store) GetCurrentState(customerID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.currents[customerID]
	return v, ok
}

// SetSLAPolicy registers or replaces the policy for a case type.
func (s *Store) SetSLAPolicy(p SLAPolicy) error {
	p.CaseType = strings.ToUpper(strings.TrimSpace(p.CaseType))
	if !validCaseType(p.CaseType) {
		return fmt.Errorf("%w: case type must be one of KYC, FINCRIME, COMPLAINT, PAYMENT_INVESTIGATION", ErrInvalid)
	}
	if p.Deadline <= 0 {
		return fmt.Errorf("%w: deadline must be positive", ErrInvalid)
	}
	if strings.TrimSpace(p.Priority) == "" {
		return fmt.Errorf("%w: priority is required", ErrInvalid)
	}
	if strings.TrimSpace(p.Escalation) == "" {
		return fmt.Errorf("%w: escalation is required", ErrInvalid)
	}
	if p.WarningBefore <= 0 {
		p.WarningBefore = p.Deadline / 4
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slaPolicies[p.CaseType] = p
	return nil
}

// GetSLAPolicy returns one policy.
func (s *Store) GetSLAPolicy(caseType string) (SLAPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.slaPolicies[strings.ToUpper(strings.TrimSpace(caseType))]
	if !ok {
		return SLAPolicy{}, fmt.Errorf("sla policy %s: %w", caseType, ErrNotFound)
	}
	return p, nil
}

// OpenCase tracks one investigation under its type policy.
func (s *Store) OpenCase(caseType string, now time.Time) (*SLAItem, error) {
	return s.OpenCaseWithID("", caseType, now, now)
}

// OpenCaseWithID tracks one investigation with an explicit id/created-at
// (used by the HTTP layer to seed deterministic fixtures).
func (s *Store) OpenCaseWithID(id, caseType string, createdAt, now time.Time) (*SLAItem, error) {
	caseType = strings.ToUpper(strings.TrimSpace(caseType))
	if !validCaseType(caseType) {
		return nil, fmt.Errorf("%w: case type must be one of KYC, FINCRIME, COMPLAINT, PAYMENT_INVESTIGATION", ErrInvalid)
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	if now.IsZero() {
		now = createdAt
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.slaPolicies[caseType]
	if !ok {
		return nil, fmt.Errorf("no sla policy for %s: %w", caseType, ErrNotFound)
	}
	if strings.TrimSpace(id) == "" {
		s.slaSeq++
		id = fmt.Sprintf("sla-%d", s.slaSeq)
	} else {
		if _, dup := s.slaItems[id]; dup {
			return nil, fmt.Errorf("sla case %s: %w", id, ErrConflict)
		}
	}
	item := &SLAItem{
		ID:         id,
		CaseType:   caseType,
		CreatedAt:  createdAt.UTC(),
		DeadlineAt: createdAt.UTC().Add(p.Deadline),
		Priority:   p.Priority,
		Escalation: p.Escalation,
	}
	s.slaItems[id] = item
	out := *item
	return &out, nil
}

// GetSLAItem returns a copy of one tracked case.
func (s *Store) GetSLAItem(id string) (*SLAItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.slaItems[id]
	if !ok {
		return nil, fmt.Errorf("sla case %s: %w", id, ErrNotFound)
	}
	out := *it
	return &out, nil
}

// Tick emits WARNING / ASSIGNED / BREACH / ESCALATED events at now.
// Each transition fires once: warnings arm before the deadline, breaches
// and escalations fire after it, and fresh items are assigned to their
// priority queue on first Tick.
func (s *Store) Tick(now time.Time) []SLAEvent {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var events []SLAEvent
	ids := make([]string, 0, len(s.slaItems))
	for id := range s.slaItems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		it := s.slaItems[id]
		p, ok := s.slaPolicies[it.CaseType]
		if !ok {
			continue
		}
		warnBefore := p.WarningBefore
		if warnBefore <= 0 {
			warnBefore = p.Deadline / 4
		}
		if !it.Assigned {
			it.Assigned = true
			it.Assignee = "queue-" + strings.ToLower(it.Priority)
			events = append(events, SLAEvent{
				ItemID: id, Kind: EventAssigned, CaseType: it.CaseType,
				Detail: "assigned to " + it.Assignee, At: now,
			})
		}
		if !it.Warned && !it.Breached && !now.Before(it.DeadlineAt.Add(-warnBefore)) && now.Before(it.DeadlineAt) {
			it.Warned = true
			events = append(events, SLAEvent{
				ItemID: id, Kind: EventWarning, CaseType: it.CaseType,
				Detail: fmt.Sprintf("deadline %s approaches", it.DeadlineAt.Format(time.RFC3339)), At: now,
			})
		}
		if !it.Breached && now.After(it.DeadlineAt) {
			it.Breached = true
			events = append(events, SLAEvent{
				ItemID: id, Kind: EventBreach, CaseType: it.CaseType,
				Detail: fmt.Sprintf("deadline %s breached", it.DeadlineAt.Format(time.RFC3339)), At: now,
			})
			if !it.Escalated {
				it.Escalated = true
				it.Assignee = it.Escalation
				events = append(events, SLAEvent{
					ItemID: id, Kind: EventEscalated, CaseType: it.CaseType,
					Detail: "escalated to " + it.Escalation, At: now,
				})
			}
		}
	}
	if events == nil {
		events = []SLAEvent{}
	}
	return events
}

// RegisterRequirement adds one requirement to the registry.
func (s *Store) RegisterRequirement(r Requirement) error {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return fmt.Errorf("%w: requirement id is required", ErrInvalid)
	}
	if strings.TrimSpace(r.Rule) == "" {
		return fmt.Errorf("%w: implemented rule is required", ErrInvalid)
	}
	if strings.TrimSpace(r.Service) == "" {
		return fmt.Errorf("%w: service is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.requirements[r.ID]; dup {
		return fmt.Errorf("requirement %s: %w", r.ID, ErrConflict)
	}
	cp := r
	s.requirements[r.ID] = &cp
	return nil
}

// GetRequirement returns one requirement.
func (s *Store) GetRequirement(id string) (*Requirement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.requirements[id]
	if !ok {
		return nil, fmt.Errorf("requirement %s: %w", id, ErrNotFound)
	}
	out := *r
	return &out, nil
}

// Coverage reports every requirement with IMPLEMENTED/TESTED/MONITORED flags plus gaps.
func (s *Store) Coverage() []CoverageEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CoverageEntry, 0, len(s.requirements))
	for _, r := range s.requirements {
		out = append(out, coverageFor(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequirementID < out[j].RequirementID })
	return out
}

// RegisterControlTest adds one control expectation.
func (s *Store) RegisterControlTest(t ControlTest) error {
	t.ID = strings.TrimSpace(t.ID)
	if t.ID == "" {
		return fmt.Errorf("%w: control test id is required", ErrInvalid)
	}
	if !validScenario(t.Scenario) {
		return fmt.Errorf("%w: scenario must be one of restricted_customer, restricted_payment, threshold_breach, missing_evidence, clean", ErrInvalid)
	}
	switch strings.ToUpper(strings.TrimSpace(t.Expected)) {
	case VerdictAllow, VerdictBlock, VerdictChallenge:
		t.Expected = strings.ToUpper(strings.TrimSpace(t.Expected))
	default:
		return fmt.Errorf("%w: expected must be ALLOW, BLOCK or CHALLENGE", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.controls[t.ID]; dup {
		return fmt.Errorf("control test %s: %w", t.ID, ErrConflict)
	}
	cp := t
	s.controls[t.ID] = &cp
	return nil
}

// RunControls evaluates every registered control against its synthetic
// scenario and records a PASS/FAIL run report.
func (s *Store) RunControls(now time.Time) (*ControlRun, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.controls) == 0 {
		return nil, fmt.Errorf("%w: no control tests registered", ErrInvalid)
	}
	ids := make([]string, 0, len(s.controls))
	for id := range s.controls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	s.seq++
	run := &ControlRun{ID: fmt.Sprintf("run-%d", s.seq), At: now}
	for _, id := range ids {
		ct := s.controls[id]
		actual := EvaluateScenario(ct.Scenario)
		verdict := RunFail
		if actual == ct.Expected {
			verdict = RunPass
		}
		run.Results = append(run.Results, ControlResult{
			TestID: ct.ID, Scenario: ct.Scenario, Expected: ct.Expected, Actual: actual, Verdict: verdict,
		})
		if verdict == RunPass {
			run.Passed++
		} else {
			run.Failed++
		}
	}
	run.Total = len(run.Results)
	if run.Failed == 0 {
		run.Overall = RunPass
	} else {
		run.Overall = RunFail
	}
	s.runs[run.ID] = run
	out := *run
	out.Results = append([]ControlResult(nil), run.Results...)
	return &out, nil
}

// GetRun returns one control run report.
func (s *Store) GetRun(id string) (*ControlRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, fmt.Errorf("control run %s: %w", id, ErrNotFound)
	}
	out := *r
	out.Results = append([]ControlResult(nil), r.Results...)
	return &out, nil
}
