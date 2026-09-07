package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/control-plane-service/internal/domain"
	"github.com/nexora/nexora/services/control-plane-service/internal/repository"
)

// platformGraph is the declared service wiring. It mirrors the envoy clusters
// and the real call paths (payments→fraud→ledger, cards→ledger, etc.).
var platformGraph = []domain.DependencyEdge{
	{From: "payment-service", To: "fraud-service"},
	{From: "payment-service", To: "ledger-service"},
	{From: "payment-service", To: "account-service"},
	{From: "card-service", To: "fraud-service"},
	{From: "card-service", To: "ledger-service"},
	{From: "card-service", To: "account-service"},
	{From: "transfer-service", To: "ledger-service"},
	{From: "transfer-service", To: "account-service"},
	{From: "pot-service", To: "ledger-service"},
	{From: "pot-service", To: "account-service"},
	{From: "dispute-service", To: "ledger-service"},
	{From: "insights-service", To: "ledger-service"},
	{From: "account-service", To: "ledger-service"},
	{From: "reconciliation-service", To: "payment-service"},
	{From: "notification-service", To: "ledger-service"},
}

// DependencyService maintains live health + renders the graph.
type DependencyService struct {
	repo     repository.HealthRepository
	health   *HealthService
	logger   zerolog.Logger

	mu       sync.RWMutex
	// shedding[service] = current % of non-critical traffic to shed.
	shedding map[string]int
}

// NewDependencyService builds the service.
func NewDependencyService(repo repository.HealthRepository, health *HealthService, logger zerolog.Logger) *DependencyService {
	return &DependencyService{
		repo:     repo,
		health:   health,
		logger:   logger,
		shedding: make(map[string]int),
	}
}

// RecordReport stores a service's latest health report.
func (s *DependencyService) RecordReport(ctx context.Context, rep *domain.HealthReport) error {
	if rep.Service == "" {
		return fmt.Errorf("service name is required")
	}
	status := domain.HealthStatus(rep.Status)
	if status == "" {
		status = domain.HealthStatusHealthy
	}
	return s.repo.UpdateServiceHealth(ctx, rep.Service, &domain.ServiceHealth{
		ServiceName:  rep.Service,
		Status:       status,
		ResponseTime: fmt.Sprintf("%.1fms", rep.LatencyMs),
		LastError:    rep.LastError,
		CheckedAt:    time.Now().UTC(),
	})
}

// GetGraph renders the live dependency graph: each node's health, its
// declared dependencies and its blast radius (how many upstream services are
// transitively affected if it degrades), plus advisories and derived
// business-aware shedding decisions.
func (s *DependencyService) GetGraph(ctx context.Context) (*domain.DependencyGraph, error) {
	live, err := s.repo.GetAllServiceHealth(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	// Stale reports (> 90s) are UNKNOWN → treated as degraded for advisory
	// purposes (a service that stops reporting is a signal, not silence).
	statusOf := func(name string) domain.HealthStatus {
		h, ok := live[name]
		if !ok || now.Sub(h.CheckedAt) > 90*time.Second {
			return domain.HealthStatusDegraded
		}
		return h.Status
	}

	// adjacency + reverse adjacency.
	dependsOn := map[string][]string{}
	dependents := map[string][]string{}
	nodes := map[string]bool{}
	for _, e := range platformGraph {
		dependsOn[e.From] = append(dependsOn[e.From], e.To)
		dependents[e.To] = append(dependents[e.To], e.From)
		nodes[e.From] = true
		nodes[e.To] = true
	}
	// Include reporting services that aren't in the static wiring.
	for name := range live {
		nodes[name] = true
	}

	// Blast radius: transitive dependents per node (memoised).
	var countDependents func(string, map[string]bool) int
	visitedAll := map[string]int{}
	countDependents = func(name string, seen map[string]bool) int {
		if v, ok := visitedAll[name]; ok {
			return v
		}
		seen[name] = true
		total := 0
		for _, up := range dependents[name] {
			if !seen[up] {
				total += 1 + countDependents(up, seen)
			}
		}
		visitedAll[name] = total
		return total
	}

	out := &domain.DependencyGraph{
		Edges:      platformGraph,
		Advisories: []string{},
		CheckedAt:  now,
	}
	overall := domain.HealthStatusHealthy

	names := make([]string, 0, len(nodes))
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		st := statusOf(name)
		node := domain.GraphNode{
			Name:        name,
			Status:      st,
			DependsOn:   dependsOn[name],
			BlastRadius: countDependents(name, map[string]bool{}),
		}
		out.Nodes = append(out.Nodes, node)

		if st == domain.HealthStatusUnhealthy {
			if overall == domain.HealthStatusHealthy || overall == domain.HealthStatusDegraded {
				overall = domain.HealthStatusUnhealthy
			}
			if node.BlastRadius > 0 {
				out.Advisories = append(out.Advisories,
					fmt.Sprintf("%s is DOWN. %d dependent service(s) affected — payments may fail.", name, node.BlastRadius))
				s.mu.Lock()
				s.shedding[name] = 0 // the node itself is down; nothing to shed onto it
				s.mu.Unlock()
			}
		} else if st == domain.HealthStatusDegraded {
			if overall == domain.HealthStatusHealthy {
				overall = domain.HealthStatusDegraded
			}
			out.Advisories = append(out.Advisories,
				fmt.Sprintf("%s is degraded. New %s operations are being throttled.", name, name))
			// Business-aware shedding: degrade → shed 40% of non-critical load.
			s.mu.Lock()
			s.shedding[name] = 40
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			delete(s.shedding, name)
			s.mu.Unlock()
		}
	}
	out.Overall = overall
	return out, nil
}

// GetThrottle returns the current adaptive load-shedding instruction for a
// service (called by the shedding middleware on each request batch).
func (s *DependencyService) GetThrottle(ctx context.Context, service string) (*domain.ThrottleDecision, error) {
	s.mu.RLock()
	pct, active := s.shedding[service]
	s.mu.RUnlock()
	if !active {
		return &domain.ThrottleDecision{Service: service, ShedPct: 0, Reason: "all dependencies healthy"}, nil
	}
	return &domain.ThrottleDecision{
		Service: service,
		ShedPct: pct,
		Reason:  "dependency degraded: shedding non-critical traffic to protect critical paths",
	}, nil
}
