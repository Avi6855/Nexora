package domain

import (
	"time"

	"github.com/google/uuid"
)

type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "HEALTHY"
	HealthStatusDegraded  HealthStatus = "DEGRADED"
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
)

type SystemHealth struct {
	SystemID   uuid.UUID              `json:"system_id"`
	Status     HealthStatus           `json:"status"`
	Services   map[string]ServiceHealth `json:"services"`
	Uptime     string                 `json:"uptime"`
	CheckedAt  time.Time              `json:"checked_at"`
}

type ServiceHealth struct {
	ServiceName  string       `json:"service_name"`
	Status       HealthStatus `json:"status"`
	ResponseTime string       `json:"response_time"`
	LastError    string       `json:"last_error,omitempty"`
	CheckedAt    time.Time    `json:"checked_at"`
}

// ── Dependency health graph (Service Dependency Health Graph) ───────────────

// DependencyEdge declares that `from` calls `to`. The graph is the platform's
// wiring (mirrors envoy clusters); health propagates downstream→upstream.
type DependencyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// GraphNode is one service in the dependency graph view.
type GraphNode struct {
	Name       string       `json:"name"`
	Status     HealthStatus `json:"status"`
	DependsOn  []string     `json:"depends_on"`
	// Worst upstream impact: how many services are (transitively) affected
	// if this node degrades. Computed over the graph on every read.
	BlastRadius int         `json:"blast_radius"`
}

// DependencyGraph is the rendered health graph.
type DependencyGraph struct {
	Nodes      []GraphNode `json:"nodes"`
	Edges      []DependencyEdge `json:"edges"`
	Overall    HealthStatus `json:"overall"`
	Advisories []string     `json:"advisories"`
	CheckedAt  time.Time    `json:"checked_at"`
}

// HealthReport is what every service POSTs to the control plane on a timer.
type HealthReport struct {
	Service      string       `json:"service"`
	Status       HealthStatus `json:"status"`
	LatencyMs    float64      `json:"latency_ms"`
	ErrorRatePct float64      `json:"error_rate_pct"`
	LastError    string       `json:"last_error,omitempty"`
}

// ThrottleDecision is the business-aware shedding instruction derived from
// the graph: when a critical dependency degrades, non-critical traffic to
// dependents gets shed before the failure cascades.
type ThrottleDecision struct {
	Service     string `json:"service"`
	ShedPct     int    `json:"shed_pct"`
	Reason      string `json:"reason"`
	TriggeredBy string `json:"triggered_by,omitempty"`
}
