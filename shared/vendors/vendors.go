// Package vendors implements Nexora's third-party platform: SLA monitoring
// with automatic escalation packages, the vendor capability registry,
// zero-downtime credential rotation, and maintenance-window coordination.
//
// A bank's uptime is the MINIMUM of its own uptime and every vendor it
// depends on. These four engines make vendor reliability measurable,
// degradable-around, and rot-resistant (credentials expire on a schedule,
// not when production breaks).
package vendors

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------- 22. SLA monitor ----------

// SLATarget is the contracted service level for one vendor metric.
type SLATarget struct {
	Vendor        string        `json:"vendor"`
	Metric        string        `json:"metric"`          // AVAILABILITY, LATENCY_P99, ERROR_RATE, SETTLEMENT_DELAY
	Target        float64       `json:"target"`          // meaning depends on metric
	Window        time.Duration `json:"window"`          // measurement window (e.g. 30d)
	CreditTierBps int64         `json:"credit_tier_bps"` // service-credit basis points per breach
}

// Measurement is one observed value.
type Measurement struct {
	At   time.Time `json:"at"`
	Name string    `json:"metric"`
	// Value semantics: AVAILABILITY as 0..1, LATENCY_P99 in ms,
	// ERROR_RATE as 0..1, SETTLEMENT_DELAY in minutes.
	Value float64 `json:"value"`
}

// SLAState tracks one target over its window.
type SLAState struct {
	Target       SLATarget     `json:"target"`
	Measurements []Measurement `json:"measurements"`
	breached     bool
}

// Observe records a measurement and evaluates the SLA. Each measurement is
// one contract period and is judged ALONE: a 0.98 availability hour breaches
// a 99.9% target even if last month was perfect, and a clean hour recovers
// the breach state (the running mean never recovers — one bad hour would
// poison it forever, hiding re-breaches). The aggregate is evidence for the
// escalation package, not the breach signal. Direction of the comparison is
// metric-specific: availability is a floor, latency/error/delay are ceilings.
func (s *SLAState) Observe(m Measurement) (breached bool, detail string) {
	s.Measurements = append(s.Measurements, m)
	switch s.Target.Metric {
	case "AVAILABILITY":
		breached = m.Value < s.Target.Target
	case "LATENCY_P99", "ERROR_RATE", "SETTLEMENT_DELAY":
		breached = m.Value > s.Target.Target
	default:
		return false, fmt.Sprintf("unknown metric %s", s.Target.Metric)
	}
	if breached && !s.breached {
		s.breached = true
		return true, fmt.Sprintf("%s at %.4f breaches target %.4f", s.Target.Metric, m.Value, s.Target.Target)
	}
	if !breached {
		s.breached = false // recovered — next breach re-fires
	}
	return false, ""
}

func (s *SLAState) aggregate() float64 {
	if len(s.Measurements) == 0 {
		return 0
	}
	// Mean over the window; real deployments would use p99 for latency,
	// but the aggregation policy is a per-metric concern of the caller.
	sum := 0.0
	for _, m := range s.Measurements {
		sum += m.Value
	}
	return sum / float64(len(s.Measurements))
}

// Breached reports current breach status.
func (s *SLAState) Breached() bool { return s.breached }

// EscalationPackage is what gets sent to the vendor and to our incident
// channel when an SLA breaches: evidence, impact estimate, contract basis.
type EscalationPackage struct {
	Vendor           string        `json:"vendor"`
	Metric           string        `json:"metric"`
	Observed         float64       `json:"observed"`
	Target           float64       `json:"target"`
	CustomerImpact   string        `json:"customer_impact"`
	ServiceCreditBps int64         `json:"service_credit_bps"`
	Evidence         []Measurement `json:"evidence"`
	GeneratedAt      time.Time     `json:"generated_at"`
}

// BuildEscalation estimates customer impact from the metric and assembles
// the contractual evidence trail.
func BuildEscalation(s *SLAState, detail string, now time.Time) *EscalationPackage {
	agg := s.aggregate()
	impact := "degraded service for some customers"
	switch s.Target.Metric {
	case "AVAILABILITY":
		if agg < 0.99 {
			impact = "material outage: payments through this vendor failing"
		}
	case "SETTLEMENT_DELAY":
		impact = "settlements delayed; customer balances update late"
	case "LATENCY_P99":
		impact = "slow responses on vendor-backed journeys"
	}
	return &EscalationPackage{
		Vendor:           s.Target.Vendor,
		Metric:           s.Target.Metric,
		Observed:         agg,
		Target:           s.Target.Target,
		CustomerImpact:   fmt.Sprintf("%s — %s", impact, detail),
		ServiceCreditBps: s.Target.CreditTierBps,
		Evidence:         append([]Measurement(nil), s.Measurements...),
		GeneratedAt:      now,
	}
}

// ---------- 23. Capability registry ----------

// Capability is one thing a vendor can do for us.
type Capability struct {
	Name    string           `json:"name"` // PAYMENTS, REFUNDS, STATUS_WEBHOOK...
	Version string           `json:"version"`
	Limits  map[string]int64 `json:"limits"` // e.g. max_amount_minor
	Regions []string         `json:"regions"`
}

// VendorRecord is the registry entry.
type VendorRecord struct {
	Name         string              `json:"name"`
	Capabilities []Capability        `json:"capabilities"`
	Maintenance  []MaintenanceWindow `json:"maintenance_windows"`
}

// Registry is the central vendor capability store.
type Registry struct {
	vendors map[string]*VendorRecord
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{vendors: map[string]*VendorRecord{}} }

// Register adds or replaces a vendor record.
func (r *Registry) Register(v VendorRecord) { r.vendors[v.Name] = &v }

// Supports answers "can vendor X do Y, in region Z, right now?" — including
// maintenance-window awareness so routing can pre-empt planned downtime.
func (r *Registry) Supports(vendor, capability, region string, at time.Time) (bool, string) {
	v, ok := r.vendors[vendor]
	if !ok {
		return false, "unknown vendor"
	}
	var cap *Capability
	for i := range v.Capabilities {
		if strings.EqualFold(v.Capabilities[i].Name, capability) {
			cap = &v.Capabilities[i]
			break
		}
	}
	if cap == nil {
		return false, fmt.Sprintf("vendor %s does not support %s", vendor, capability)
	}
	if region != "" {
		found := false
		for _, rg := range cap.Regions {
			if rg == region || rg == "GLOBAL" {
				found = true
				break
			}
		}
		if !found {
			return false, fmt.Sprintf("capability %s not available in region %s", capability, region)
		}
	}
	for _, mw := range v.Maintenance {
		if !at.Before(mw.Start) && at.Before(mw.End) {
			return false, fmt.Sprintf("vendor in maintenance window %s–%s", mw.Start.Format(time.RFC3339), mw.End.Format(time.RFC3339))
		}
	}
	return true, "supported"
}

// ---------- 24. Credential rotation ----------

// Credential is one rotating secret for one vendor.
type Credential struct {
	ID         string    `json:"id"`
	Vendor     string    `json:"vendor"`
	Kind       string    `json:"kind"` // API_KEY, MTLS_CERT, OAUTH_CLIENT
	NotBefore  time.Time `json:"not_before"`
	NotAfter   time.Time `json:"not_after"`
	Active     bool      `json:"active"`
	DeployedTo []string  `json:"deployed_to"` // services running with it
}

// RotationState is the lifecycle stage of a credential nearing expiry.
type RotationState string

const (
	RotationHealthy  RotationState = "HEALTHY"
	RotationWarn30   RotationState = "WARN_30D"
	RotationWarn7    RotationState = "WARN_7D"
	RotationCritical RotationState = "CRITICAL_1D"
	RotationExpired  RotationState = "EXPIRED"
)

// RotationStage classifies expiry urgency.
func RotationStage(c Credential, now time.Time) RotationState {
	remaining := c.NotAfter.Sub(now)
	switch {
	case remaining <= 0:
		return RotationExpired
	case remaining <= 24*time.Hour:
		return RotationCritical
	case remaining <= 7*24*time.Hour:
		return RotationWarn7
	case remaining <= 30*24*time.Hour:
		return RotationWarn30
	default:
		return RotationHealthy
	}
}

// RotationError is returned for invalid rotation moves.
var ErrRotationInvalid = errors.New("invalid credential rotation")

// Rotator manages the issue→deploy→verify→revoke ladder. Dual-active is the
// core idea: the NEW credential is issued and deployed alongside the old one,
// verified, and only then is the old one revoked — zero downtime.
type Rotator struct {
	creds map[string]*Credential
	now   func() time.Time
}

// NewRotator creates a rotator.
func NewRotator(now func() time.Time) *Rotator {
	if now == nil {
		now = time.Now
	}
	return &Rotator{creds: map[string]*Credential{}, now: now}
}

// Issue creates a new credential overlapping the old one's validity.
func (r *Rotator) Issue(id, vendor, kind string, lifetime time.Duration, deployedTo []string) (*Credential, error) {
	if lifetime <= 0 {
		return nil, fmt.Errorf("%w: lifetime must be positive", ErrRotationInvalid)
	}
	now := r.now()
	c := &Credential{ID: id, Vendor: vendor, Kind: kind, NotBefore: now,
		NotAfter: now.Add(lifetime), Active: true, DeployedTo: deployedTo}
	r.creds[id] = c
	return c, nil
}

// Get fetches a credential.
func (r *Rotator) Get(id string) (*Credential, error) {
	c, ok := r.creds[id]
	if !ok {
		return nil, fmt.Errorf("credential %s not found", id)
	}
	return c, nil
}

// Deploy records that a service now runs with this credential.
func (r *Rotator) Deploy(id, service string) error {
	c, err := r.Get(id)
	if err != nil {
		return err
	}
	for _, s := range c.DeployedTo {
		if s == service {
			return nil
		}
	}
	c.DeployedTo = append(c.DeployedTo, service)
	return nil
}

// Verify is the pre-revocation gate: the new credential must be deployed to
// every service the old one covered, and must be currently valid. Revoking
// the old credential before the new one is fully deployed is THE classic
// self-inflicted outage.
func (r *Rotator) Verify(newID, oldID string) error {
	n, err := r.Get(newID)
	if err != nil {
		return err
	}
	o, err := r.Get(oldID)
	if err != nil {
		return err
	}
	if r.now().After(n.NotAfter) || r.now().Before(n.NotBefore) {
		return fmt.Errorf("%w: new credential %s not currently valid", ErrRotationInvalid, newID)
	}
	covered := map[string]bool{}
	for _, s := range n.DeployedTo {
		covered[s] = true
	}
	for _, s := range o.DeployedTo {
		if !covered[s] {
			return fmt.Errorf("%w: service %s still on old credential — deploy %s first", ErrRotationInvalid, s, newID)
		}
	}
	return nil
}

// Revoke retires the old credential once verification passed.
func (r *Rotator) Revoke(id string) error {
	c, err := r.Get(id)
	if err != nil {
		return err
	}
	c.Active = false
	return nil
}

// Due lists credentials needing action, most urgent first.
func (r *Rotator) Due() []Credential {
	var out []Credential
	now := r.now()
	for _, c := range r.creds {
		if c.Active {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := RotationStage(out[i], now), RotationStage(out[j], now)
		if si != sj {
			return urgency(si) < urgency(sj)
		}
		return out[i].NotAfter.Before(out[j].NotAfter)
	})
	return out
}

func urgency(s RotationState) int {
	switch s {
	case RotationExpired:
		return 0
	case RotationCritical:
		return 1
	case RotationWarn7:
		return 2
	case RotationWarn30:
		return 3
	default:
		return 4
	}
}

// ---------- 25. Maintenance window coordinator ----------

// MaintenanceWindow is a planned vendor downtime.
type MaintenanceWindow struct {
	Vendor  string    `json:"vendor"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Summary string    `json:"summary"`
}

// Impact describes what a maintenance window touches.
type Impact struct {
	Window       MaintenanceWindow `json:"window"`
	Capabilities []string          `json:"capabilities_affected"`
	Services     []string          `json:"services_affected"`
	Journeys     []string          `json:"customer_journeys_affected"`
	Recommended  string            `json:"recommended_action"`
	DegradeMode  string            `json:"degraded_mode"`
}

// Coordinator maps vendor maintenance to affected capabilities, services and
// customer journeys via the capability registry, and recommends pre-emptive
// degraded modes.
type Coordinator struct {
	registry *Registry
	// serviceVendors maps service → vendors it calls.
	serviceVendors map[string][]string
	// journeyServices maps customer journey → services it needs.
	journeyServices map[string][]string
	// degradeMode names the fallback per capability.
	degradeMode map[string]string
}

// NewCoordinator wires the dependency graph.
func NewCoordinator(reg *Registry, serviceVendors, journeyServices map[string][]string, degradeMode map[string]string) *Coordinator {
	return &Coordinator{registry: reg, serviceVendors: serviceVendors, journeyServices: journeyServices, degradeMode: degradeMode}
}

// Assess projects one maintenance window through the dependency graph.
func (c *Coordinator) Assess(w MaintenanceWindow) Impact {
	vendorCaps := map[string]bool{}
	if v, ok := c.registry.vendors[w.Vendor]; ok {
		for _, cap := range v.Capabilities {
			vendorCaps[cap.Name] = true
		}
	}
	var services, journeys []string
	capSet := map[string]bool{}
	for svc, vendors := range c.serviceVendors {
		for _, vd := range vendors {
			if vd == w.Vendor {
				services = append(services, svc)
				for capName := range vendorCaps {
					capSet[capName] = true
				}
			}
		}
	}
	for j, svcs := range c.journeyServices {
		for _, svc := range svcs {
			if contains(services, svc) {
				journeys = append(journeys, j)
				break
			}
		}
	}
	caps := make([]string, 0, len(capSet))
	for cName := range capSet {
		caps = append(caps, cName)
	}
	sort.Strings(caps)
	sort.Strings(services)
	sort.Strings(journeys)

	mode := "queue and replay after window closes"
	for cName := range capSet {
		if dm, ok := c.degradeMode[cName]; ok {
			mode = dm
			break
		}
	}
	rec := fmt.Sprintf("activate degraded mode for %s from %s to %s",
		strings.Join(caps, "/"), w.Start.Format(time.Kitchen), w.End.Format(time.Kitchen))
	if len(journeys) == 0 {
		rec = "no customer-facing impact expected"
	}
	return Impact{
		Window: w, Capabilities: caps, Services: services, Journeys: journeys,
		Recommended: rec, DegradeMode: mode,
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
