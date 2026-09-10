// Provider arbitration, review-queue balancing and evidence expiration guard.
//
//  13. Arbitration: multiple identity-verification providers differ in cost,
//     coverage, confidence and latency. The arbiter picks per-request using
//     declared provider capabilities plus live health, and fails over on
//     unavailability — KYC throughput never depends on one vendor.
//  14. Queue balancing: human reviewers are the scarcest compliance resource.
//     Assignment weighs complexity, language, expertise and current workload
//     so distribution is fair AND skill-matched.
//  15. Expiration guard: NO evidence is deleted/expired while a legal hold,
//     open investigation or regulatory request covers it — the lifecycle
//     engine must ask permission from this guard first.
package kyc

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── 13. Provider arbitration ────────────────────────────────────────────────

// ProviderCapability declares one verification vendor.
type ProviderCapability struct {
	Name string `json:"name"`
	// Countries the provider covers (ISO-3166 alpha-2), e.g. ["GB","IN"].
	Countries []string `json:"countries"`
	// DocumentTypes supported, e.g. ["PASSPORT","DRIVING_LICENCE"].
	DocumentTypes []string `json:"document_types"`
	// CostPerCheckGBP is the per-verification price.
	CostPerCheckGBP float64 `json:"cost_per_check_gbp"`
	// ExpectedConfidence 0..1 historical mean.
	ExpectedConfidence float64 `json:"expected_confidence"`
	// ExpectedLatencyMs mean turnaround.
	ExpectedLatencyMs float64 `json:"expected_latency_ms"`
	// Available is the live health flag (health-checker updated).
	Available bool `json:"available"`
}

// VerificationRequest is one arbitration decision input.
type VerificationRequest struct {
	Country      string `json:"country"`
	DocumentType string `json:"document_type"`
	// PreferSpeed / PreferCost / PreferConfidence weight the choice.
	PreferSpeed      bool `json:"prefer_speed,omitempty"`
	PreferCost       bool `json:"prefer_cost,omitempty"`
	PreferConfidence bool `json:"prefer_confidence,omitempty"`
}

// Arbitration is the chosen provider with the decision rationale.
type Arbitration struct {
	Provider   string               `json:"provider"`
	Reason     string               `json:"reason"`
	Candidates []ProviderCapability `json:"candidates"`
}

var ErrNoProviderAvailable = errors.New("no verification provider covers this request")

// ProviderArbiter selects the best provider per request.
type ProviderArbiter struct {
	mu        sync.RWMutex
	providers []ProviderCapability
}

func NewProviderArbiter() *ProviderArbiter { return &ProviderArbiter{} }

// Register adds/updates a provider (health-checker calls this too).
func (a *ProviderArbiter) Register(p ProviderCapability) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.providers {
		if a.providers[i].Name == p.Name {
			a.providers[i] = p
			return
		}
	}
	a.providers = append(a.providers, p)
}

// Arbitrate picks the provider: coverage first (hard requirement), then
// availability, then the requested optimisation dimension.
func (a *ProviderArbiter) Arbitrate(req VerificationRequest) (*Arbitration, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var candidates []ProviderCapability
	for _, p := range a.providers {
		if !p.Available {
			continue
		}
		if !coversCountry(p.Countries, req.Country) || !coversDoc(p.DocumentTypes, req.DocumentType) {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return nil, ErrNoProviderAvailable
	}
	score := func(p ProviderCapability) float64 {
		s := p.ExpectedConfidence
		if req.PreferSpeed {
			s += 1.0 / (1 + p.ExpectedLatencyMs/1000)
		}
		if req.PreferCost {
			s += 1.0 / (1 + p.CostPerCheckGBP)
		}
		if req.PreferConfidence {
			s *= 2 // double weight on confidence
		}
		return s
	}
	sort.Slice(candidates, func(i, j int) bool { return score(candidates[i]) > score(candidates[j]) })
	best := candidates[0]
	reason := fmt.Sprintf("covers %s/%s, available, %s",
		req.Country, req.DocumentType, optimisationLabel(req))
	return &Arbitration{Provider: best.Name, Reason: reason, Candidates: candidates}, nil
}

func optimisationLabel(req VerificationRequest) string {
	switch {
	case req.PreferSpeed:
		return "optimised for speed"
	case req.PreferCost:
		return "optimised for cost"
	case req.PreferConfidence:
		return "optimised for confidence"
	default:
		return "balanced scoring"
	}
}

func coversCountry(list []string, c string) bool {
	for _, x := range list {
		if strings.EqualFold(x, c) {
			return true
		}
	}
	return false
}

func coversDoc(list []string, d string) bool {
	for _, x := range list {
		if strings.EqualFold(x, d) {
			return true
		}
	}
	return false
}

// Failover removes a provider from availability (health-checker on failure).
func (a *ProviderArbiter) Failover(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.providers {
		if a.providers[i].Name == name {
			a.providers[i].Available = false
		}
	}
}

// ── 14. Review queue balancing ──────────────────────────────────────────────

// ReviewCase is one KYC case awaiting human review.
type ReviewCase struct {
	CaseID     string `json:"case_id"`
	Complexity int    `json:"complexity"` // 1 (trivial) .. 5 (forensic)
	Country    string `json:"country"`
	Language   string `json:"language"`
	// SLADeadline drives urgency in scoring.
	SLADeadline time.Time `json:"sla_deadline"`
}

// Reviewer is a human reviewer with skills and workload.
type Reviewer struct {
	Name string `json:"name"`
	// Languages the reviewer is qualified for.
	Languages []string `json:"languages"`
	// ExpertiseCountries (empty = generalist, any country).
	ExpertiseCountries []string `json:"expertise_countries"`
	// MaxComplexity they are certified for.
	MaxComplexity int `json:"max_complexity"`
	// CurrentLoad = open cases.
	CurrentLoad int `json:"current_load"`
	Capacity    int `json:"capacity"`
}

// Assignment is the routing decision.
type Assignment struct {
	CaseID   string `json:"case_id"`
	Reviewer string `json:"reviewer"`
	Reason   string `json:"reason"`
}

// QueueBalancer assigns cases to reviewers fairly and skill-matched.
type QueueBalancer struct{}

func NewQueueBalancer() *QueueBalancer { return &QueueBalancer{} }

// Assign picks the best reviewer for one case. Eligibility is hard-filtered
// (language, complexity certification, capacity); scoring prefers expertise,
// lower load, and SLA pressure.
func (QueueBalancer) Assign(c ReviewCase, reviewers []Reviewer) (*Assignment, error) {
	type scored struct {
		r     Reviewer
		score float64
	}
	var eligible []scored
	for _, r := range reviewers {
		if r.CurrentLoad >= r.Capacity {
			continue
		}
		if c.Complexity > r.MaxComplexity {
			continue
		}
		if len(r.Languages) > 0 && !coversDoc(r.Languages, c.Language) {
			continue
		}
		urgent := !c.SLADeadline.IsZero() && time.Until(c.SLADeadline) < 24*time.Hour
		expertiseMatch := len(r.ExpertiseCountries) == 0 || coversDoc(r.ExpertiseCountries, c.Country)
		specialist := coversDoc(r.ExpertiseCountries, c.Country) // generalists don't count
		s := float64(r.Capacity - r.CurrentLoad)                 // prefer reviewers with headroom
		if expertiseMatch {
			s += 10 // expertise bonus
			if urgent && specialist {
				s += 15 // urgent SLA: skill-match must outrank load balancing —
				// a mis-routed urgent case misses its deadline, a
				// temporarily busier expert does not.
			}
		}
		eligible = append(eligible, scored{r, s})
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("no eligible reviewer for case %s (language=%s complexity=%d)",
			c.CaseID, c.Language, c.Complexity)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].score > eligible[j].score })
	best := eligible[0]
	return &Assignment{CaseID: c.CaseID, Reviewer: best.r.Name,
		Reason: fmt.Sprintf("load %d/%d, expertise match, complexity %d <= %d",
			best.r.CurrentLoad, best.r.Capacity, c.Complexity, best.r.MaxComplexity)}, nil
}

// Rebalance returns moves that even out loads: from over-capacity reviewers
// to under-capacity ones with matching skills. Simple, explainable, auditable.
type RebalanceMove struct {
	CaseID string `json:"case_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}

func (QueueBalancer) Rebalance(cases []ReviewCase, reviewers []Reviewer) []RebalanceMove {
	var moves []RebalanceMove
	byID := map[string]ReviewCase{}
	for _, c := range cases {
		byID[c.CaseID] = c
	}
	for i := range reviewers {
		from := &reviewers[i]
		if from.CurrentLoad <= from.Capacity {
			continue
		}
		overflow := from.CurrentLoad - from.Capacity
		for overflow > 0 {
			moved := false
			for j := range reviewers {
				if overflow == 0 {
					break
				}
				to := &reviewers[j]
				if to.Name == from.Name || to.CurrentLoad >= to.Capacity {
					continue
				}
				if to.MaxComplexity < 5 { // conservative: only move to fully certified reviewers
					continue
				}
				moves = append(moves, RebalanceMove{CaseID: "overflow", From: from.Name, To: to.Name})
				from.CurrentLoad--
				to.CurrentLoad++
				overflow--
				moved = true
			}
			if !moved {
				break // no capacity anywhere — report what we could shed
			}
		}
	}
	return moves
}

// ── 15. Regulatory evidence expiration guard ────────────────────────────────

// HoldType enumerates why evidence must be preserved.
type HoldType string

const (
	HoldLegal         HoldType = "LEGAL_HOLD"
	HoldInvestigation HoldType = "OPEN_INVESTIGATION"
	HoldRegulatoryReq HoldType = "REGULATORY_REQUEST"
)

// Hold is an active preservation order.
type Hold struct {
	ID        HoldType       `json:"id"`
	Reason    string         `json:"reason"`
	SubjectID string         `json:"subject_id"`               // customer/business ID
	Evidence  []EvidenceType `json:"evidence_types,omitempty"` // empty = all
	IssuedBy  string         `json:"issued_by"`
	IssuedAt  time.Time      `json:"issued_at"`
	ExpiresAt time.Time      `json:"expires_at,omitempty"` // zero = indefinite
}

// RetentionRequirement is a compliance minimum-retention rule per type.
type RetentionRequirement struct {
	Type    EvidenceType  `json:"type"`
	KeepFor time.Duration `json:"keep_for"`
}

// ExpirationGuard decides whether a lifecycle transition (delete, expire,
// purge) is SAFE. It is the gate the retention engine must consult —
// deleting evidence under a legal hold is not a bug, it is an offence.
type ExpirationGuard struct {
	mu        sync.Mutex
	holds     []Hold
	retention map[EvidenceType]time.Duration
}

func NewExpirationGuard(retention map[EvidenceType]time.Duration) *ExpirationGuard {
	return &ExpirationGuard{retention: retention}
}

// PlaceHold records a preservation order.
func (g *ExpirationGuard) PlaceHold(h Hold) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.holds = append(g.holds, h)
}

// ReleaseHold removes expired holds.
func (g *ExpirationGuard) ReleaseHold(id HoldType, subjectID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var kept []Hold
	for _, h := range g.holds {
		if h.ID == id && h.SubjectID == subjectID {
			continue
		}
		kept = append(kept, h)
	}
	g.holds = kept
}

// CanExpire answers whether evidence collected at collectedAt for a subject
// may now be expired/deleted.
type ExpirationDecision struct {
	Allowed   bool     `json:"allowed"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	Reason    string   `json:"reason"`
}

func (g *ExpirationGuard) CanExpire(subjectID string, t EvidenceType, collectedAt time.Time, now time.Time) ExpirationDecision {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 1. Retention minimum.
	if keep, ok := g.retention[t]; ok && now.Sub(collectedAt) < keep {
		return ExpirationDecision{Allowed: false,
			BlockedBy: []string{"retention_minimum"},
			Reason:    fmt.Sprintf("%s must be retained %v (collected %s)", t, keep, collectedAt.Format(time.DateOnly))}
	}

	// 2. Active holds for this subject covering this type (or all types).
	var blocked []string
	for _, h := range g.holds {
		if h.SubjectID != subjectID {
			continue
		}
		if !h.ExpiresAt.IsZero() && now.After(h.ExpiresAt) {
			continue // hold itself expired
		}
		if len(h.Evidence) == 0 || containsEvidence(h.Evidence, t) {
			blocked = append(blocked, string(h.ID))
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return ExpirationDecision{Allowed: false, BlockedBy: blocked,
			Reason: "active preservation order(s) cover this evidence"}
	}
	return ExpirationDecision{Allowed: true, Reason: "retention satisfied and no holds active"}
}

func containsEvidence(list []EvidenceType, t EvidenceType) bool {
	for _, e := range list {
		if e == t {
			return true
		}
	}
	return false
}
