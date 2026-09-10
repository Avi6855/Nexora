// Package changerisk implements Engineering Change Risk Scoring.
//
// A PR that touches 12 services, 4 schemas and a payment path is not the
// same risk as a PR that renames a field in one service — but human reviewers
// weight by recency and confidence, not by blast radius. This package
// quantifies change risk from structural facts (what changed, what depends on
// it, how critical it is, how well it is tested, its incident history) and
// drives the review/rollout policy: higher risk → more gates.
package changerisk

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RiskLevel is the final classification.
type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

// ServiceFacts are the structural facts about one touched service.
type ServiceFacts struct {
	Name string `json:"name"`
	// Criticality: PAYMENT_PATH services dominate risk.
	Criticality string `json:"criticality"` // PAYMENT_PATH / IMPORTANT / DEFERABLE
	// DirectDependents count (blast radius, first hop).
	DirectDependents int `json:"direct_dependents"`
	// TransitiveDependents count (full graph reach).
	TransitiveDependents int `json:"transitive_dependents"`
	// TestCoveragePct 0-100.
	TestCoveragePct float64 `json:"test_coverage_pct"`
	// IncidentsLast90d on this service.
	IncidentsLast90d int `json:"incidents_last_90d"`
	// PaymentPathChanged flags a change inside a money-movement path.
	PaymentPathChanged bool `json:"payment_path_changed"`
	// SchemaChanged flags a data-model/schema change (hardest to roll back).
	SchemaChanged bool `json:"schema_changed"`
	// AuthorFamiliarity: PRs the author has merged in this service in 90d.
	AuthorFamiliarity int `json:"author_familiarity"`
}

// ChangeRequest describes the PR under assessment.
type ChangeRequest struct {
	PRID     string         `json:"pr_id"`
	Author   string         `json:"author"`
	Title    string         `json:"title"`
	Services []ServiceFacts `json:"services"`
	// AIGenerated flags machine-authored changes — they carry the same gates
	// as human changes plus an explicit provenance review, per the platform's
	// position that AI contributions need engineering controls, not bans.
	AIGenerated bool `json:"ai_generated"`
}

// Assessment is the scored result.
type Assessment struct {
	PRID     string    `json:"pr_id"`
	Score    float64   `json:"score"` // 0-100
	Level    RiskLevel `json:"level"`
	Drivers  []string  `json:"drivers"` // top contributing factors, ranked
	Gates    []string  `json:"required_gates"`
	ScoredAt time.Time `json:"scored_at"`
}

// Scorer computes change risk.
type Scorer struct{}

func NewScorer() *Scorer { return &Scorer{} }

// Weights — documented so teams can tune deliberately.
const (
	wCriticalityPaymentPath = 30
	wSchemaChange           = 20
	wBlastRadiusMax         = 20 // scaled by transitive dependents
	wIncidentHistoryMax     = 15
	wCoverageDeficitMax     = 10
	wLowFamiliarityMax      = 5
)

// Assess scores the change.
func (s *Scorer) Assess(req ChangeRequest) *Assessment {
	a := &Assessment{PRID: req.PRID, ScoredAt: time.Now().UTC()}
	type factor struct {
		name   string
		points float64
	}
	var factors []factor
	add := func(name string, pts float64) {
		if pts > 0 {
			factors = append(factors, factor{name, pts})
		}
	}

	paymentPath := false
	schema := false
	maxTransitive := 0
	totalIncidents := 0
	worstCoverage := 100.0
	minFamiliarity := 1 << 30

	for _, svc := range req.Services {
		if strings.EqualFold(svc.Criticality, "PAYMENT_PATH") {
			paymentPath = true
		}
		if svc.SchemaChanged {
			schema = true
		}
		if svc.TransitiveDependents > maxTransitive {
			maxTransitive = svc.TransitiveDependents
		}
		totalIncidents += svc.IncidentsLast90d
		if svc.TestCoveragePct < worstCoverage {
			worstCoverage = svc.TestCoveragePct
		}
		if svc.AuthorFamiliarity < minFamiliarity {
			minFamiliarity = svc.AuthorFamiliarity
		}
		if svc.PaymentPathChanged {
			paymentPath = true
		}
	}

	if paymentPath {
		add("money-movement path changed", wCriticalityPaymentPath)
	}
	if schema {
		add("schema/data-model change (hard to roll back)", wSchemaChange)
	}
	// Blast radius saturates at 100 transitive dependents.
	blast := float64(maxTransitive) / 100 * wBlastRadiusMax
	if blast > wBlastRadiusMax {
		blast = wBlastRadiusMax
	}
	add(fmt.Sprintf("blast radius: %d transitive dependents", maxTransitive), blast)

	// Incident history: 3+ incidents in 90d saturates.
	inc := float64(totalIncidents) / 3 * wIncidentHistoryMax
	if inc > wIncidentHistoryMax {
		inc = wIncidentHistoryMax
	}
	add(fmt.Sprintf("%d incidents in the last 90d on touched services", totalIncidents), inc)

	// Coverage deficit below 80%.
	cov := (80 - worstCoverage) / 80 * wCoverageDeficitMax
	if cov < 0 {
		cov = 0
	}
	add(fmt.Sprintf("test coverage %.0f%% on weakest touched service", worstCoverage), cov)

	// Unfamiliar author (0 prior PRs in these services).
	if minFamiliarity != 1<<30 && minFamiliarity == 0 {
		add("author has no prior merged changes in touched services", wLowFamiliarityMax)
	}
	if req.AIGenerated {
		add("AI-authored change: provenance review required", 5)
	}

	var score float64
	for _, f := range factors {
		score += f.points
	}
	if score > 100 {
		score = 100
	}
	a.Score = score

	sort.Slice(factors, func(i, j int) bool { return factors[i].points > factors[j].points })
	for _, f := range factors {
		a.Drivers = append(a.Drivers, fmt.Sprintf("%s (+%.0f)", f.name, f.points))
	}

	switch {
	case score >= 75:
		a.Level = RiskCritical
	case score >= 50:
		a.Level = RiskHigh
	case score >= 25:
		a.Level = RiskMedium
	default:
		a.Level = RiskLow
	}
	a.Gates = gatesFor(a.Level, paymentPath, schema)
	return a
}

// gatesFor maps risk level to the rollout policy. Higher risk never means
// "no deploy" — it means more evidence before deploy.
func gatesFor(level RiskLevel, paymentPath, schema bool) []string {
	var g []string
	switch level {
	case RiskLow:
		g = append(g, "standard CI", "single approver")
	case RiskMedium:
		g = append(g, "standard CI", "two approvers", "canary 5% for 30m")
	case RiskHigh:
		g = append(g, "standard CI", "two approvers incl. service owner",
			"canary 5% for 1h with auto-rollback", "shadow verification (shared/verify)")
	default:
		g = append(g, "standard CI", "two approvers incl. service owner",
			"canary 5% for 1h with auto-rollback", "shadow verification (shared/verify)",
			"compatibility certification vs stand-in (standin.Certifier)")
	}
	if schema {
		g = append(g, "expand-migrate-contract migration plan required")
	}
	if paymentPath {
		g = append(g, "financial invariant tests green (make test-financial)")
	}
	return g
}
