// Package depgraph is the control-plane live dependency health graph:
// per-service health + latency ingestion, a blast-radius-ordered graph view,
// and automatic throttle advice ("X degraded → throttle Y to Z%").
package depgraph

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// defaultEdges mirrors the platform wiring (payment→fraud→ledger, etc.).
var defaultEdges = [][2]string{
	{"payment-service", "fraud-service"},
	{"payment-service", "ledger-service"},
	{"payment-service", "account-service"},
	{"card-service", "fraud-service"},
	{"card-service", "ledger-service"},
	{"card-service", "account-service"},
	{"transfer-service", "ledger-service"},
	{"transfer-service", "account-service"},
	{"pot-service", "ledger-service"},
	{"pot-service", "account-service"},
	{"dispute-service", "ledger-service"},
	{"insights-service", "ledger-service"},
	{"account-service", "ledger-service"},
	{"reconciliation-service", "payment-service"},
	{"notification-service", "ledger-service"},
}

// HealthEntry is one service's live health.
type HealthEntry struct {
	Service   string    `json:"service"`
	Status    string    `json:"status"`
	LatencyMs float64   `json:"latency_ms"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Node is one graph node with its blast radius.
type Node struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	LatencyMs   float64  `json:"latency_ms"`
	DependsOn   []string `json:"depends_on"`
	BlastRadius int      `json:"blast_radius"`
}

// Graph is the blast-radius-ordered view.
type Graph struct {
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	Overall   string    `json:"overall"`
	CheckedAt time.Time `json:"checked_at"`
}

// Edge is a declared dependency.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Advice is one throttle instruction.
type Advice struct {
	Dependency  string `json:"dependency"`
	Target      string `json:"target"`
	ThrottlePct int    `json:"throttle_pct"`
	Message     string `json:"message"`
}

// Service holds live health and the static wiring.
type Service struct {
	mu     sync.Mutex
	health map[string]HealthEntry
	edges  [][2]string
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{health: map[string]HealthEntry{}, edges: defaultEdges, logger: logger}
}

// ReportHealth ingests one service's status + latency.
func (s *Service) ReportHealth(service, status string, latencyMs float64) error {
	if strings.TrimSpace(service) == "" {
		return fmt.Errorf("service is required")
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "" {
		status = "HEALTHY"
	}
	if status != "HEALTHY" && status != "DEGRADED" && status != "UNHEALTHY" {
		return fmt.Errorf("status must be HEALTHY, DEGRADED or UNHEALTHY")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health[service] = HealthEntry{Service: service, Status: status, LatencyMs: latencyMs, UpdatedAt: time.Now().UTC()}
	if status != "HEALTHY" {
		s.logger.Info().Str("service", service).Str("status", status).Float64("latency_ms", latencyMs).Msg("dependency health degraded")
	}
	return nil
}

// Graph renders nodes ordered by blast radius (desc), then name.
func (s *Service) Graph() Graph {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.graphLocked()
}

func (s *Service) graphLocked() Graph {
	dependsOn := map[string][]string{}
	dependents := map[string][]string{}
	nodes := map[string]bool{}
	for _, e := range s.edges {
		dependsOn[e[0]] = append(dependsOn[e[0]], e[1])
		dependents[e[1]] = append(dependents[e[1]], e[0])
		nodes[e[0]] = true
		nodes[e[1]] = true
	}
	for name := range s.health {
		nodes[name] = true
	}
	memo := map[string]int{}
	var blast func(string, map[string]bool) int
	blast = func(name string, seen map[string]bool) int {
		if v, ok := memo[name]; ok {
			return v
		}
		seen[name] = true
		total := 0
		for _, up := range dependents[name] {
			if !seen[up] {
				total += 1 + blast(up, seen)
			}
		}
		memo[name] = total
		return total
	}
	out := Graph{CheckedAt: time.Now().UTC(), Overall: "HEALTHY"}
	for name := range nodes {
		status := "HEALTHY"
		lat := 0.0
		if h, ok := s.health[name]; ok {
			status = h.Status
			lat = h.LatencyMs
		}
		out.Nodes = append(out.Nodes, Node{
			Name: name, Status: status, LatencyMs: lat,
			DependsOn: dependsOn[name], BlastRadius: blast(name, map[string]bool{}),
		})
		for _, e := range s.edges {
			_ = e
			break
		}
	}
	for _, e := range s.edges {
		out.Edges = append(out.Edges, Edge{From: e[0], To: e[1]})
	}
	if out.Edges == nil {
		out.Edges = []Edge{}
	}
	overall := "HEALTHY"
	for _, n := range out.Nodes {
		if n.Status == "UNHEALTHY" {
			overall = "UNHEALTHY"
			break
		}
		if n.Status == "DEGRADED" {
			overall = "DEGRADED"
		}
	}
	out.Overall = overall
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].BlastRadius != out.Nodes[j].BlastRadius {
			return out.Nodes[i].BlastRadius > out.Nodes[j].BlastRadius
		}
		return out.Nodes[i].Name < out.Nodes[j].Name
	})
	return out
}

// Advice returns throttle instructions for every non-healthy dependency:
// degraded → throttle dependents to 40%, unhealthy → 100%.
func (s *Service) Advice() []Advice {
	s.mu.Lock()
	defer s.mu.Unlock()
	dependents := map[string][]string{}
	for _, e := range s.edges {
		dependents[e[1]] = append(dependents[e[1]], e[0])
	}
	out := []Advice{}
	for svc, h := range s.health {
		if h.Status == "HEALTHY" {
			continue
		}
		pct := 40
		if h.Status == "UNHEALTHY" {
			pct = 100
		}
		for _, dep := range dependents[svc] {
			out = append(out, Advice{
				Dependency: svc, Target: dep, ThrottlePct: pct,
				Message: fmt.Sprintf("%s %s → throttle %s to %d%%", svc, strings.ToLower(h.Status), dep, pct),
			})
		}
		if len(dependents[svc]) == 0 {
			out = append(out, Advice{
				Dependency: svc, Target: svc, ThrottlePct: pct,
				Message: fmt.Sprintf("%s %s → throttle %s to %d%%", svc, strings.ToLower(h.Status), svc, pct),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dependency != out[j].Dependency {
			return out[i].Dependency < out[j].Dependency
		}
		return out[i].Target < out[j].Target
	})
	if out == nil {
		out = []Advice{}
	}
	return out
}
