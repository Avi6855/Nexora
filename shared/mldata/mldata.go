// Package mldata implements ML/data-platform safety for the control plane:
//
//  43. Impact simulator: model dependency graph {models, dashboards,
//     features, reports, regulatory} → blast radius count + list.
//  44. Freshness gateway: feature SLA table → ALLOW/WARN/BLOCK per age.
//  45. Drift monitor: baseline vs current distributions (median/p95) →
//     drift alerts over threshold.
//  46. Dependency registry: model → {features, datasets, policies} graph +
//     pre-deploy health verify.
//  47. Rollback compat: rollback target's expected feature schema vs
//     current pipeline schema → ROLLBACK_SAFE/UNSAFE.
package mldata

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnknownModel is returned for unregistered models.
	ErrUnknownModel = errors.New("unknown model")
	// ErrModelExists is returned on duplicate registration.
	ErrModelExists = errors.New("model already registered")
	// ErrUnknownFeature is returned when no SLA covers a feature.
	ErrUnknownFeature = errors.New("unknown feature")
	// ErrNoBaseline is returned when drift is checked without a baseline.
	ErrNoBaseline = errors.New("no baseline recorded")
)

// ─── 43. Impact simulator ────────────────────────────────────────────────

// GraphEdge is one dependency edge From → To.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// ImpactGraph is the model/dashboard/feature/report/regulatory graph.
type ImpactGraph struct {
	mu      sync.Mutex
	forward map[string]map[string]bool
	known   map[string]bool
}

// NewImpactGraph builds an empty graph.
func NewImpactGraph() *ImpactGraph {
	return &ImpactGraph{forward: map[string]map[string]bool{}, known: map[string]bool{}}
}

// AddEdge records From → To (From feeds To).
func (g *ImpactGraph) AddEdge(from, to, kind string) error {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return fmt.Errorf("from and to are required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.forward[from] == nil {
		g.forward[from] = map[string]bool{}
	}
	g.forward[from][to] = true
	g.known[from] = true
	g.known[to] = true
	return nil
}

// Edges returns a sorted snapshot.
func (g *ImpactGraph) Edges() []GraphEdge {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []GraphEdge{}
	for from, tos := range g.forward {
		for to := range tos {
			out = append(out, GraphEdge{From: from, To: to})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// Impact is the blast-radius answer for a proposed change.
type Impact struct {
	Node     string   `json:"node"`
	Count    int      `json:"count"`
	Impacted []string `json:"impacted"`
}

// Simulate returns the downstream blast radius of node, cycle-safe.
func (g *ImpactGraph) Simulate(node string) (Impact, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.known[node] {
		return Impact{}, fmt.Errorf("unknown node %q", node)
	}
	visited := map[string]bool{node: true}
	queue := []string{node}
	out := []string{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		next := []string{}
		for n := range g.forward[cur] {
			next = append(next, n)
		}
		sort.Strings(next)
		for _, n := range next {
			if visited[n] {
				continue
			}
			visited[n] = true
			out = append(out, n)
			queue = append(queue, n)
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return Impact{Node: node, Count: len(out), Impacted: out}, nil
}

// ─── 44. Freshness gateway ──────────────────────────────────────────────

// FreshnessVerdict gates feature use by age.
type FreshnessVerdict string

const (
	// FreshAllow means the feature is fresh enough to serve.
	FreshAllow FreshnessVerdict = "ALLOW"
	// FreshWarn means the feature is stale but usable with caution.
	FreshWarn FreshnessVerdict = "WARN"
	// FreshBlock means the feature is too stale to serve.
	FreshBlock FreshnessVerdict = "BLOCK"
)

// FreshnessGateway holds per-feature SLA ceilings.
type FreshnessGateway struct {
	mu  sync.Mutex
	sla map[string]time.Duration
}

// NewFreshnessGateway builds an empty gateway.
func NewFreshnessGateway() *FreshnessGateway {
	return &FreshnessGateway{sla: map[string]time.Duration{}}
}

// SetSLA records the maximum acceptable age for a feature.
func (g *FreshnessGateway) SetSLA(feature string, maxAge time.Duration) error {
	if strings.TrimSpace(feature) == "" {
		return fmt.Errorf("feature is required")
	}
	if maxAge <= 0 {
		return fmt.Errorf("max age must be positive")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sla[feature] = maxAge
	return nil
}

// FreshnessResult is the gateway answer.
type FreshnessResult struct {
	Feature string           `json:"feature"`
	Verdict FreshnessVerdict `json:"verdict"`
	AgeMs   int64            `json:"age_ms"`
	SLAMs   int64            `json:"sla_ms"`
	Reason  string           `json:"reason"`
}

// Check grades one feature's age: <80% SLA → ALLOW, ≤SLA → WARN,
// >SLA → BLOCK.
func (g *FreshnessGateway) Check(feature string, age time.Duration) (FreshnessResult, error) {
	if age < 0 {
		return FreshnessResult{}, fmt.Errorf("age must not be negative")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	sla, ok := g.sla[feature]
	if !ok {
		return FreshnessResult{}, fmt.Errorf("%w: %s", ErrUnknownFeature, feature)
	}
	res := FreshnessResult{Feature: feature, AgeMs: age.Milliseconds(), SLAMs: sla.Milliseconds()}
	switch {
	case age > sla:
		res.Verdict = FreshBlock
		res.Reason = fmt.Sprintf("age %s exceeds SLA %s", age, sla)
	case float64(age) > 0.8*float64(sla):
		res.Verdict = FreshWarn
		res.Reason = fmt.Sprintf("age %s past 80%% of SLA %s", age, sla)
	default:
		res.Verdict = FreshAllow
		res.Reason = fmt.Sprintf("age %s within SLA %s", age, sla)
	}
	return res, nil
}

// ─── 45. Drift monitor ─────────────────────────────────────────────────

// Distribution summarises a feature/score stream.
type Distribution struct {
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
}

// DriftMonitor compares live distributions against baselines.
type DriftMonitor struct {
	mu        sync.Mutex
	baselines map[string]Distribution
	threshold float64 // fractional drift that alerts, e.g. 0.2 = 20%
}

// NewDriftMonitor builds a monitor; non-positive thresholds get 20%.
func NewDriftMonitor(thresholdPct float64) *DriftMonitor {
	if thresholdPct <= 0 {
		thresholdPct = 20
	}
	return &DriftMonitor{baselines: map[string]Distribution{}, threshold: thresholdPct / 100}
}

// SetBaseline records the reference distribution for a key.
func (m *DriftMonitor) SetBaseline(key string, d Distribution) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("key is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baselines[key] = d
	return nil
}

// DriftResult is the drift answer.
type DriftResult struct {
	Key      string  `json:"key"`
	Alert    bool    `json:"alert"`
	DriftPct float64 `json:"drift_pct"`
	Reason   string  `json:"reason"`
}

func relDrift(base, cur float64) float64 {
	if base == 0 {
		if cur == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return math.Abs(cur-base) / math.Abs(base)
}

// Check compares current against the baseline; either median or p95 drift
// beyond the threshold alerts.
func (m *DriftMonitor) Check(key string, cur Distribution) (DriftResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	base, ok := m.baselines[key]
	if !ok {
		return DriftResult{}, fmt.Errorf("%w: %s", ErrNoBaseline, key)
	}
	drift := math.Max(relDrift(base.Median, cur.Median), relDrift(base.P95, cur.P95))
	pct := math.Round(drift*10000) / 100
	res := DriftResult{Key: key, DriftPct: pct}
	thresholdPct := math.Round(m.threshold*10000) / 100
	if math.IsInf(drift, 1) || drift > m.threshold {
		res.Alert = true
		res.Reason = fmt.Sprintf("drift %.2f%% exceeds threshold %.2f%%", pct, thresholdPct)
	} else {
		res.Reason = fmt.Sprintf("drift %.2f%% within threshold %.2f%%", pct, thresholdPct)
	}
	return res, nil
}

// ─── 46. Dependency registry ────────────────────────────────────────────

// ModelDeps is one model's build-time dependencies.
type ModelDeps struct {
	ID       string   `json:"id"`
	Features []string `json:"features"`
	Datasets []string `json:"datasets"`
	Policies []string `json:"policies"`
}

// DependencyRegistry maps models to their features/datasets/policies.
type DependencyRegistry struct {
	mu     sync.Mutex
	models map[string]ModelDeps
}

// NewDependencyRegistry builds an empty registry.
func NewDependencyRegistry() *DependencyRegistry {
	return &DependencyRegistry{models: map[string]ModelDeps{}}
}

// Register adds a model; duplicates conflict.
func (r *DependencyRegistry) Register(m ModelDeps) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("model id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.models[m.ID]; ok {
		return fmt.Errorf("%w: %s", ErrModelExists, m.ID)
	}
	m.Features = append([]string{}, m.Features...)
	m.Datasets = append([]string{}, m.Datasets...)
	m.Policies = append([]string{}, m.Policies...)
	r.models[m.ID] = m
	return nil
}

// VerifyResult is the pre-deploy health answer.
type VerifyResult struct {
	Model   string   `json:"model"`
	Healthy bool     `json:"healthy"`
	Missing []string `json:"missing"`
}

// VerifyDeps checks that every dataset is healthy and every feature and
// policy is declared. datasetHealth maps dataset → healthy; a dataset
// absent from the map counts as missing/unhealthy.
func (r *DependencyRegistry) VerifyDeps(id string, datasetHealth map[string]bool) (VerifyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.models[id]
	if !ok {
		return VerifyResult{}, fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	missing := []string{}
	if len(m.Features) == 0 {
		missing = append(missing, "no features declared")
	}
	if len(m.Policies) == 0 {
		missing = append(missing, "no policies declared")
	}
	for _, ds := range m.Datasets {
		if !datasetHealth[ds] {
			missing = append(missing, fmt.Sprintf("dataset %q unhealthy or unknown", ds))
		}
	}
	sort.Strings(missing)
	if missing == nil {
		missing = []string{}
	}
	return VerifyResult{Model: id, Healthy: len(missing) == 0, Missing: missing}, nil
}

// ─── 47. Rollback compat ────────────────────────────────────────────────

// RollbackVerdict grades a rollback target's schema.
type RollbackVerdict string

const (
	// RollbackSafe means the old model still fits the current pipeline.
	RollbackSafe RollbackVerdict = "ROLLBACK_SAFE"
	// RollbackUnsafe means rolling back would break the pipeline.
	RollbackUnsafe RollbackVerdict = "UNSAFE"
)

// RollbackCheck is the compatibility answer.
type RollbackCheck struct {
	Verdict    RollbackVerdict `json:"verdict"`
	Mismatches []string        `json:"mismatches"`
}

// VerifyRollbackCompat compares the rollback target's expected feature
// schema against the current pipeline schema: every expected field must
// exist with the same type. Extra current fields are fine.
func VerifyRollbackCompat(expected, current map[string]string) RollbackCheck {
	mismatches := []string{}
	for field, want := range expected {
		got, ok := current[field]
		if !ok {
			mismatches = append(mismatches, fmt.Sprintf("field %q missing from current pipeline schema", field))
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(want)) {
			mismatches = append(mismatches, fmt.Sprintf("field %q type mismatch: rollback expects %q, pipeline has %q", field, want, got))
		}
	}
	sort.Strings(mismatches)
	if mismatches == nil {
		mismatches = []string{}
	}
	if len(mismatches) > 0 {
		return RollbackCheck{Verdict: RollbackUnsafe, Mismatches: mismatches}
	}
	return RollbackCheck{Verdict: RollbackSafe, Mismatches: mismatches}
}
