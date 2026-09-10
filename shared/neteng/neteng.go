// Package neteng implements Nexora's multi-cloud network engineering:
// cross-cloud path health and physical circuit capacity planning.
//
// Monzo-style architecture runs primary on one cloud with a stand-in on
// another, plus physical connections to payment schemes. Two problems are
// first-class here:
//
//   - Path health: an AWS→GCP path that silently degrades (packet loss,
//     route flaps) shows up as mysterious p99s in every service that
//     crosses it. This package scores paths and flags degradation.
//   - Circuit capacity: unlike cloud autoscaling, a 100Mbps physical circuit
//     to a payment scheme cannot be scaled at deploy time. Forecast growth
//     and alert BEFORE the circuit saturates — 90 days of lead time is the
//     difference between a planned upgrade and an incident.
package neteng

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// ── Cross-cloud path health ─────────────────────────────────────────────────

// PathSample is one probe interval on a cross-cloud path.
type PathSample struct {
	At             time.Time `json:"at"`
	LatencyMs      float64   `json:"latency_ms"`
	PacketLossPct  float64   `json:"packet_loss_pct"`
	ThroughputMbps float64   `json:"throughput_mbps"`
	RouteChanged   bool      `json:"route_changed"`
}

// PathHealth is the scored state of a network path.
type PathHealth struct {
	Path           string  `json:"path"`  // e.g. "aws-eu-west-2 → gcp-europe-west2"
	Score          float64 `json:"score"` // 0-100
	State          string  `json:"state"` // HEALTHY / DEGRADED / CRITICAL
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	P99LatencyMs   float64 `json:"p99_latency_ms"`
	AvgLossPct     float64 `json:"avg_loss_pct"`
	RouteFlaps     int     `json:"route_flaps"`
	Recommendation string  `json:"recommendation,omitempty"`
}

// PathThresholds encode healthy cross-cloud behaviour.
type PathThresholds struct {
	MaxHealthyLatencyMs float64
	MaxHealthyLossPct   float64
	MaxRouteFlaps       int
}

func DefaultPathThresholds() PathThresholds {
	return PathThresholds{MaxHealthyLatencyMs: 50, MaxHealthyLossPct: 0.1, MaxRouteFlaps: 2}
}

// ScorePath computes health from samples.
func ScorePath(path string, samples []PathSample, th PathThresholds) *PathHealth {
	h := &PathHealth{Path: path}
	if len(samples) == 0 {
		h.State = "UNKNOWN"
		h.Score = 0
		return h
	}
	lats := make([]float64, 0, len(samples))
	var lossSum float64
	for _, s := range samples {
		lats = append(lats, s.LatencyMs)
		lossSum += s.PacketLossPct
		if s.RouteChanged {
			h.RouteFlaps++
		}
	}
	sort.Float64s(lats)
	h.AvgLatencyMs = mean(lats)
	h.P99LatencyMs = lats[int(math.Min(float64(len(lats)-1), math.Floor(0.99*float64(len(lats)))))]
	h.AvgLossPct = lossSum / float64(len(samples))

	score := 100.0
	if h.AvgLatencyMs > th.MaxHealthyLatencyMs {
		score -= math.Min(30, (h.AvgLatencyMs/th.MaxHealthyLatencyMs-1)*30)
	}
	if h.P99LatencyMs > 2*th.MaxHealthyLatencyMs {
		score -= math.Min(20, (h.P99LatencyMs/(2*th.MaxHealthyLatencyMs)-1)*20)
	}
	if h.AvgLossPct > th.MaxHealthyLossPct {
		score -= math.Min(40, h.AvgLossPct/th.MaxHealthyLossPct*20)
	}
	if h.RouteFlaps > th.MaxRouteFlaps {
		score -= 10
	}
	if score < 0 {
		score = 0
	}
	h.Score = score

	switch {
	case score >= 85:
		h.State = "HEALTHY"
	case score >= 60:
		h.State = "DEGRADED"
		h.Recommendation = "degraded cross-cloud path: check interconnect utilisation and BGP sessions before p99s surface in services"
	default:
		h.State = "CRITICAL"
		h.Recommendation = "critical path degradation: fail traffic to the alternate path / region and page network on-call"
	}
	return h
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// ── Circuit capacity planning ───────────────────────────────────────────────

// Circuit is one physical/committed connection.
type Circuit struct {
	Name         string  `json:"name"`
	CapacityMbps float64 `json:"capacity_mbps"`
	// Provider, e.g. "payment-scheme-direct", "interconnect".
	Provider string `json:"provider"`
}

// CircuitSample is one utilisation observation.
type CircuitSample struct {
	At           time.Time `json:"at"`
	UtilisedMbps float64   `json:"utilised_mbps"`
}

// Forecast projects utilisation forward.
type Forecast struct {
	Circuit        string  `json:"circuit"`
	CurrentUtilPct float64 `json:"current_util_pct"`
	// ProjectedPctAt maps days-ahead → projected utilisation %.
	ProjectedPctAt  map[int]float64 `json:"projected_pct_at"`
	GrowthPctPerDay float64         `json:"growth_pct_per_day"`
	// DaysToThreshold: days until the alert threshold is crossed (negative =
	// already over, math.MaxInt = never at current trend).
	DaysToThreshold int    `json:"days_to_threshold"`
	Verdict         string `json:"verdict"`
}

// CapacityPlanner forecasts circuit saturation from observed growth.
type CapacityPlanner struct {
	// AlertAtPct: utilisation % at which expansion must be underway.
	AlertAtPct float64
	// HorizonDays: how far to project.
	HorizonDays int
}

func NewCapacityPlanner() *CapacityPlanner {
	return &CapacityPlanner{AlertAtPct: 80, HorizonDays: 90}
}

// Plan fits a linear growth trend over the samples and projects it.
// Linear (not exponential) is deliberate for committed circuits: observed
// traffic growth on payment-scheme links is dominated by linear customer
// growth; compounding forecasts produce panic PRs.
func (p *CapacityPlanner) Plan(c Circuit, samples []CircuitSample) *Forecast {
	f := &Forecast{Circuit: c.Name, ProjectedPctAt: map[int]float64{}}
	if len(samples) < 2 {
		f.Verdict = "insufficient samples"
		f.DaysToThreshold = math.MaxInt32
		return f
	}
	sorted := append([]CircuitSample(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	// Least-squares fit of utilised Mbps against time (days).
	n := float64(len(sorted))
	var sumX, sumY, sumXY, sumXX float64
	t0 := sorted[0].At
	for _, s := range sorted {
		x := s.At.Sub(t0).Hours() / 24
		y := s.UtilisedMbps
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		f.Verdict = "no trend (constant utilisation)"
		f.DaysToThreshold = math.MaxInt32
		f.CurrentUtilPct = sorted[len(sorted)-1].UtilisedMbps / c.CapacityMbps * 100
		return f
	}
	slope := (n*sumXY - sumX*sumY) / denom // Mbps per day
	intercept := (sumY - slope*sumX) / n
	lastX := sorted[len(sorted)-1].At.Sub(t0).Hours() / 24
	currentMbps := intercept + slope*lastX
	f.CurrentUtilPct = currentMbps / c.CapacityMbps * 100
	f.GrowthPctPerDay = slope / c.CapacityMbps * 100

	thresholdMbps := c.CapacityMbps * p.AlertAtPct / 100
	if slope > 0 {
		f.DaysToThreshold = int((thresholdMbps - currentMbps) / slope)
	} else {
		f.DaysToThreshold = math.MaxInt32 // declining or flat: never crosses
	}

	for day := 30; day <= p.HorizonDays; day += 30 {
		f.ProjectedPctAt[day] = (intercept + slope*(lastX+float64(day))) / c.CapacityMbps * 100
	}

	switch {
	case f.CurrentUtilPct >= p.AlertAtPct:
		f.Verdict = "OVER_THRESHOLD_NOW"
	case f.DaysToThreshold <= 30:
		f.Verdict = "EXPAND_WITHIN_30_DAYS"
	case f.DaysToThreshold <= 90:
		f.Verdict = "EXPANSION_NEEDED_THIS_QUARTER"
	default:
		f.Verdict = "OK"
	}
	return f
}

// ── Cross-cloud traffic cost accounting ─────────────────────────────────────

// TrafficFlow is one observed service-to-service flow across clouds.
type TrafficFlow struct {
	Source       string        `json:"source"` // "payment-service@aws"
	Destination  string        `json:"destination"`
	CrossCloud   bool          `json:"cross_cloud"`
	BytesPerDay  int64         `json:"bytes_per_day"`
	AvgLatencyMs float64       `json:"avg_latency_ms"`
	Window       time.Duration `json:"window"`
}

// CostModel prices cross-cloud egress. Rates are configuration, not gospel.
type CostModel struct {
	// EgressCostPerGB charged on cross-cloud egress.
	EgressCostPerGB float64
	// LatencyPenaltyCostPerMsPerMillionReq: optional modelling hook for the
	// latency tax on synchronous cross-cloud calls.
	LatencyPenaltyCostPerMsPerMillionReq float64
}

// DefaultCostModel uses a representative £/GB egress figure.
func DefaultCostModel() CostModel {
	return CostModel{EgressCostPerGB: 0.08, LatencyPenaltyCostPerMsPerMillionReq: 0.5}
}

// CrossCloudOptimizer ranks flows by wasted spend and recommends co-location.
type CrossCloudOptimizer struct {
	mu    sync.RWMutex
	flows []TrafficFlow
	model CostModel
}

func NewCrossCloudOptimizer(model CostModel) *CrossCloudOptimizer {
	return &CrossCloudOptimizer{model: model}
}

// Observe records a flow.
func (o *CrossCloudOptimizer) Observe(f TrafficFlow) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flows = append(o.flows, f)
}

// CostRecommendation is one optimisation opportunity.
type CostRecommendation struct {
	Flow           TrafficFlow `json:"flow"`
	MonthlyCost    float64     `json:"monthly_cost_gbp"`
	Recommendation string      `json:"recommendation"`
}

// Recommendations returns cross-cloud flows ranked by monthly cost.
func (o *CrossCloudOptimizer) Recommendations() []CostRecommendation {
	o.mu.RLock()
	defer o.mu.RUnlock()
	var out []CostRecommendation
	for _, f := range o.flows {
		if !f.CrossCloud {
			continue
		}
		gbPerDay := float64(f.BytesPerDay) / (1 << 30)
		monthly := gbPerDay * 30 * o.model.EgressCostPerGB
		rec := CostRecommendation{Flow: f, MonthlyCost: monthly}
		switch {
		case monthly > 500:
			rec.Recommendation = fmt.Sprintf("£%.0f/month cross-cloud: co-locate %s with %s or add a same-cloud replica of %s", monthly, f.Source, f.Destination, f.Destination)
		case monthly > 50:
			rec.Recommendation = fmt.Sprintf("£%.0f/month cross-cloud: review call pattern; batch or cache to cut round trips", monthly)
		default:
			rec.Recommendation = "acceptable"
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MonthlyCost > out[j].MonthlyCost })
	return out
}
