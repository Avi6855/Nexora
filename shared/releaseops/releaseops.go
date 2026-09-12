// Package releaseops implements Nexora's release-operations platform:
//
//  23. Capacity forecast: per-service traffic samples plus event uplifts
//     (payday / black-friday multipliers) produce a forecast and a replica
//     recommendation — finance never pages because payday arrived.
//
//  24. Release manager: a service dependency graph gates deploys (BLOCKED
//     while any dependency is degraded) and canary releases walk
//     1% → 25% → 100% with health + dependency checks between stages.
//
//  25. Contract observatory: per-endpoint field-usage samples yield usage
//     percentages; fields under a threshold are suggested safe-to-remove
//     and move through an explicit deprecation workflow.
package releaseops

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// ── 23. Capacity forecast ───────────────────────────────────────────────────

// EventUplift is a known traffic multiplier (payday, black-friday…).
type EventUplift struct {
	Name       string
	Multiplier float64
}

// Forecast is the capacity recommendation for a service.
type Forecast struct {
	Service        string
	BaselineRPS    float64
	ForecastRPS    float64
	Replicas       int
	AppliedUplifts []string
}

// Forecaster holds per-service traffic samples.
type Forecaster struct {
	mu      sync.Mutex
	samples map[string][]float64
}

// NewForecaster builds an empty forecaster.
func NewForecaster() *Forecaster {
	return &Forecaster{samples: map[string][]float64{}}
}

// AddSamples records traffic samples (requests/sec) for a service.
func (f *Forecaster) AddSamples(service string, samples []float64) error {
	if service == "" {
		return errors.New("service is required")
	}
	if len(samples) == 0 {
		return errors.New("at least one sample is required")
	}
	for _, s := range samples {
		if s < 0 {
			return errors.New("samples must not be negative")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples[service] = append(f.samples[service], samples...)
	return nil
}

// Forecast computes baseline × uplifts and recommends replicas. The
// baseline is the mean of recorded samples (or of the provided samples
// when given); perReplicaRPS is the capacity of one replica.
func (f *Forecaster) Forecast(service string, samples []float64, uplifts []EventUplift, perReplicaRPS float64) (Forecast, error) {
	if service == "" {
		return Forecast{}, errors.New("service is required")
	}
	if perReplicaRPS <= 0 {
		return Forecast{}, errors.New("per-replica capacity must be positive")
	}
	data := samples
	if len(data) == 0 {
		f.mu.Lock()
		data = append([]float64(nil), f.samples[service]...)
		f.mu.Unlock()
	}
	if len(data) == 0 {
		return Forecast{}, fmt.Errorf("no traffic samples for service %s", service)
	}
	sum := 0.0
	for _, s := range data {
		if s < 0 {
			return Forecast{}, errors.New("samples must not be negative")
		}
		sum += s
	}
	base := sum / float64(len(data))
	mult := 1.0
	var applied []string
	for _, u := range uplifts {
		if u.Multiplier <= 0 {
			return Forecast{}, fmt.Errorf("uplift %q multiplier must be positive", u.Name)
		}
		mult *= u.Multiplier
		applied = append(applied, u.Name)
	}
	forecast := base * mult
	replicas := int(math.Ceil(forecast / perReplicaRPS))
	if replicas < 1 {
		replicas = 1
	}
	return Forecast{
		Service: service, BaselineRPS: base, ForecastRPS: forecast,
		Replicas: replicas, AppliedUplifts: applied,
	}, nil
}

// ── 24. Release manager ─────────────────────────────────────────────────────

var (
	ErrUnknownCanary  = errors.New("unknown canary")
	ErrCanaryComplete = errors.New("canary is already complete")
	ErrDeployBlocked  = errors.New("deploy blocked: a dependency is degraded")
	ErrUnknownService = errors.New("unknown service")
)

// Health states.
const (
	HealthHealthy  = "HEALTHY"
	HealthDegraded = "DEGRADED"
)

// Canary stages.
var canaryStages = []int{1, 25, 100}

// Canary is one staged rollout.
type Canary struct {
	ID      string
	Service string
	Version string
	Stage   int    // percent currently serving
	Status  string // RUNNING / COMPLETE
}

// ReleaseManager holds the dependency graph, health and canaries.
type ReleaseManager struct {
	mu       sync.Mutex
	seq      int
	deps     map[string][]string // service → dependencies
	health   map[string]string   // service → HEALTHY/DEGRADED
	canaries map[string]*Canary
}

// NewReleaseManager builds an empty manager.
func NewReleaseManager() *ReleaseManager {
	return &ReleaseManager{
		deps: map[string][]string{}, health: map[string]string{},
		canaries: map[string]*Canary{},
	}
}

// RegisterDep declares that service depends on dependsOn.
func (m *ReleaseManager) RegisterDep(service, dependsOn string) error {
	if service == "" || dependsOn == "" {
		return errors.New("service and dependency are required")
	}
	if service == dependsOn {
		return errors.New("a service cannot depend on itself")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deps[service] {
		if d == dependsOn {
			return nil // idempotent
		}
	}
	m.deps[service] = append(m.deps[service], dependsOn)
	if _, ok := m.health[service]; !ok {
		m.health[service] = HealthHealthy
	}
	if _, ok := m.health[dependsOn]; !ok {
		m.health[dependsOn] = HealthHealthy
	}
	return nil
}

// ReportHealth records the current health of a service.
func (m *ReleaseManager) ReportHealth(service, status string) error {
	if service == "" {
		return errors.New("service is required")
	}
	if status != HealthHealthy && status != HealthDegraded {
		return fmt.Errorf("unknown health status %q", status)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.health[service] = status
	return nil
}

func (m *ReleaseManager) degradedLocked(service string) []string {
	var bad []string
	if m.health[service] == HealthDegraded {
		bad = append(bad, service)
	}
	seen := map[string]bool{}
	var walk func(s string)
	walk = func(s string) {
		for _, d := range m.deps[s] {
			if seen[d] {
				continue
			}
			seen[d] = true
			if m.health[d] == HealthDegraded {
				bad = append(bad, d)
			}
			walk(d)
		}
	}
	walk(service)
	sort.Strings(bad)
	return bad
}

// GateDeploy reports whether a deploy may proceed. The gate is BLOCKED
// when the service itself or any (transitive) dependency is degraded.
func (m *ReleaseManager) GateDeploy(service string) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if bad := m.degradedLocked(service); len(bad) > 0 {
		return false, fmt.Sprintf("BLOCKED: degraded dependencies: %v", bad)
	}
	return true, "ALLOWED"
}

// StartCanary opens a canary at 1% after passing the deploy gate.
func (m *ReleaseManager) StartCanary(service, version string) (*Canary, error) {
	if service == "" || version == "" {
		return nil, errors.New("service and version are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if bad := m.degradedLocked(service); len(bad) > 0 {
		return nil, ErrDeployBlocked
	}
	m.seq++
	c := &Canary{ID: fmt.Sprintf("canary-%d", m.seq), Service: service, Version: version, Stage: canaryStages[0], Status: "RUNNING"}
	m.canaries[c.ID] = c
	cp := *c
	return &cp, nil
}

// AdvanceCanary moves a canary to the next stage after re-checking health
// and dependencies; the 100% stage completes the rollout.
func (m *ReleaseManager) AdvanceCanary(id string) (*Canary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.canaries[id]
	if !ok {
		return nil, ErrUnknownCanary
	}
	if c.Status == "COMPLETE" {
		return nil, ErrCanaryComplete
	}
	if bad := m.degradedLocked(c.Service); len(bad) > 0 {
		return nil, ErrDeployBlocked
	}
	idx := 0
	for i, s := range canaryStages {
		if s == c.Stage {
			idx = i
			break
		}
	}
	if idx == len(canaryStages)-1 {
		c.Status = "COMPLETE"
	} else {
		c.Stage = canaryStages[idx+1]
	}
	cp := *c
	return &cp, nil
}

// GetCanary returns a copy of a canary.
func (m *ReleaseManager) GetCanary(id string) (*Canary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.canaries[id]
	if !ok {
		return nil, ErrUnknownCanary
	}
	cp := *c
	return &cp, nil
}

// ── 25. Contract observatory ────────────────────────────────────────────────

var ErrUnknownField = errors.New("no usage recorded for this endpoint field")

type fieldUsage struct {
	total int
	used  int
}

// FieldStats is the usage assessment for one endpoint field.
type FieldStats struct {
	Endpoint   string
	Field      string
	Total      int
	Used       int
	UsagePct   float64
	Deprecated bool
}

// Observatory tracks per-endpoint field usage.
type Observatory struct {
	mu         sync.Mutex
	usage      map[string]map[string]*fieldUsage // endpoint → field
	deprecated map[string]bool                   // endpoint + "\x00" + field
}

// NewObservatory builds an empty observatory.
func NewObservatory() *Observatory {
	return &Observatory{usage: map[string]map[string]*fieldUsage{}, deprecated: map[string]bool{}}
}

func obsKey(endpoint, field string) string { return endpoint + "\x00" + field }

// RecordUsage records one sample: whether the field was present/used.
func (o *Observatory) RecordUsage(endpoint, field string, used bool) error {
	if endpoint == "" || field == "" {
		return errors.New("endpoint and field are required")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.usage[endpoint] == nil {
		o.usage[endpoint] = map[string]*fieldUsage{}
	}
	u := o.usage[endpoint][field]
	if u == nil {
		u = &fieldUsage{}
		o.usage[endpoint][field] = u
	}
	u.total++
	if used {
		u.used++
	}
	return nil
}

// Stats returns usage percentages for one field.
func (o *Observatory) Stats(endpoint, field string) (FieldStats, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	u := o.usage[endpoint][field]
	if u == nil || u.total == 0 {
		return FieldStats{}, ErrUnknownField
	}
	return FieldStats{
		Endpoint: endpoint, Field: field, Total: u.total, Used: u.used,
		UsagePct:   100 * float64(u.used) / float64(u.total),
		Deprecated: o.deprecated[obsKey(endpoint, field)],
	}, nil
}

// SafeToRemove reports whether usage sits under the threshold percentage
// (a deprecation candidate, not a deletion order).
func (o *Observatory) SafeToRemove(endpoint, field string, thresholdPct float64) (bool, FieldStats, error) {
	st, err := o.Stats(endpoint, field)
	if err != nil {
		return false, FieldStats{}, err
	}
	return st.UsagePct < thresholdPct, st, nil
}

// Deprecate moves a field into the deprecation workflow.
func (o *Observatory) Deprecate(endpoint, field string) error {
	if endpoint == "" || field == "" {
		return errors.New("endpoint and field are required")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.usage[endpoint] == nil || o.usage[endpoint][field] == nil {
		return ErrUnknownField
	}
	o.deprecated[obsKey(endpoint, field)] = true
	return nil
}
