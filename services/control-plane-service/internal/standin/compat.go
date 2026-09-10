// Package standin implements Nexora's Primary ↔ Stand-in platform
// compatibility machinery.
//
// Two components:
//
//  1. Compatibility Certification: before a deploy, the same request is
//     replayed against the primary's API and the stand-in's API and the
//     RESPONSE SEMANTICS are compared — status classes, decision fields,
//     state transitions, error codes. This is banking-continuity
//     certification, not ordinary contract testing: a divergence means the
//     stand-in would make a DIFFERENT payment decision during an outage.
//  2. Capability Declaration: the active platform announces what it can do
//     (cards ✅, investments ❌ …) so clients discover capabilities at
//     runtime instead of hard-coding outage-mode logic.
package standin

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Verdict is the certification outcome.
type Verdict string

const (
	VerdictCompatible Verdict = "COMPATIBLE" // safe to deploy / stand-in can absorb
	VerdictPartial    Verdict = "PARTIAL"    // degradations only; deploy with documented limits
	VerdictBreaking   Verdict = "BREAKING"   // stand-in would behave differently — BLOCK
)

// Comparison dimension weights for scoring.
const (
	weightStatusClass   = 30
	weightDecisionField = 25
	weightStateTrans    = 25
	weightErrorCode     = 15
	weightLimits        = 5
)

// RequestResponse is one replayed interaction.
type RequestResponse struct {
	// Endpoint under test, e.g. "POST /v1/cards/authorize".
	Endpoint string `json:"endpoint"`
	// Request payload (canonical JSON).
	Request string `json:"request"`
	// Response fields from each platform.
	Primary Response `json:"primary"`
	StandIn Response `json:"stand_in"`
}

// Response is a platform's answer, normalised for comparison.
type Response struct {
	// StatusClass buckets 2xx/4xx/5xx — exact code differences within a class
	// are tolerated (an outage-mode 503 vs primary 502 both mean "retryable
	// upstream"), but class flips (2xx→4xx) are breaking.
	StatusClass string `json:"status_class"` // "2xx","4xx","5xx"
	// Decision is the semantic outcome, e.g. "APPROVED","DECLINED","STEP_UP".
	Decision string `json:"decision"`
	// StateTransition the request causes, e.g. "PENDING→AUTHORIZED".
	StateTransition string `json:"state_transition"`
	// ErrorCode on failure, e.g. "INSUFFICIENT_FUNDS".
	ErrorCode string `json:"error_code,omitempty"`
	// LimitsApplied echoes any limit ids that fired.
	LimitsApplied []string `json:"limits_applied,omitempty"`
}

// EndpointResult is the comparison for one endpoint.
type EndpointResult struct {
	Endpoint     string   `json:"endpoint"`
	Verdict      Verdict  `json:"verdict"`
	Differences  []string `json:"differences"`
	ScorePenalty int      `json:"score_penalty"`
}

// Certification is the full report.
type Certification struct {
	CertifiedAt  time.Time        `json:"certified_at"`
	DeployTarget string           `json:"deploy_target"` // e.g. "payment-service@sha256:abc"
	Overall      Verdict          `json:"overall_verdict"`
	Score        int              `json:"score"` // 0-100
	Endpoints    []EndpointResult `json:"endpoints"`
	Summary      string           `json:"summary"`
}

// Certifier compares primary vs stand-in responses.
type Certifier struct {
	mu sync.Mutex
	// history keeps recent certifications for regression tracking.
	history []Certification
}

func NewCertifier() *Certifier { return &Certifier{} }

// Certify replays each RequestResponse pair and scores the semantic
// differences. Any DECISION or STATE_TRANSITION divergence is automatically
// BREAKING regardless of score — a stand-in that approves what the primary
// would decline is not a degradation, it is a different bank.
func (c *Certifier) Certify(deployTarget string, cases []RequestResponse) *Certification {
	cert := &Certification{
		CertifiedAt:  time.Now().UTC(),
		DeployTarget: deployTarget,
		Score:        100,
	}
	overall := VerdictCompatible
	for _, tc := range cases {
		res := compare(tc)
		cert.Endpoints = append(cert.Endpoints, res)
		cert.Score -= res.ScorePenalty
		if res.Verdict == VerdictBreaking {
			overall = VerdictBreaking
		} else if res.Verdict == VerdictPartial && overall != VerdictBreaking {
			overall = VerdictPartial
		}
	}
	if cert.Score < 0 {
		cert.Score = 0
	}
	cert.Overall = overall
	n := len(cases)
	cert.Summary = fmt.Sprintf("%d endpoints certified against stand-in: %s (score %d)", n, overall, cert.Score)

	c.mu.Lock()
	c.history = append(c.history, *cert)
	c.mu.Unlock()
	return cert
}

// History returns past certifications (most recent last).
func (c *Certifier) History() []Certification {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]Certification(nil), c.history...)
	return out
}

func compare(tc RequestResponse) EndpointResult {
	res := EndpointResult{Endpoint: tc.Endpoint}
	p, s := tc.Primary, tc.StandIn

	if p.StatusClass != s.StatusClass {
		res.Differences = append(res.Differences,
			fmt.Sprintf("status class: primary=%s stand_in=%s", p.StatusClass, s.StatusClass))
		res.ScorePenalty += weightStatusClass
	}
	if !strings.EqualFold(p.Decision, s.Decision) {
		res.Differences = append(res.Differences,
			fmt.Sprintf("DECISION DIVERGENCE: primary=%s stand_in=%s", p.Decision, s.Decision))
		res.ScorePenalty += weightDecisionField
	}
	if p.StateTransition != s.StateTransition {
		res.Differences = append(res.Differences,
			fmt.Sprintf("state transition: primary=%q stand_in=%q", p.StateTransition, s.StateTransition))
		res.ScorePenalty += weightStateTrans
	}
	if p.ErrorCode != s.ErrorCode {
		res.Differences = append(res.Differences,
			fmt.Sprintf("error code: primary=%q stand_in=%q", p.ErrorCode, s.ErrorCode))
		res.ScorePenalty += weightErrorCode
	}
	if !equalSets(p.LimitsApplied, s.LimitsApplied) {
		res.Differences = append(res.Differences,
			fmt.Sprintf("limits: primary=%v stand_in=%v", p.LimitsApplied, s.LimitsApplied))
		res.ScorePenalty += weightLimits
	}

	// Decision/state divergence is categorically breaking.
	if strings.Contains(strings.Join(res.Differences, "|"), "DECISION DIVERGENCE") ||
		(p.StateTransition != s.StateTransition && bothNonEmpty(p.StateTransition, s.StateTransition)) {
		res.Verdict = VerdictBreaking
		return res
	}

	switch {
	case len(res.Differences) == 0:
		res.Verdict = VerdictCompatible
	case res.ScorePenalty <= weightErrorCode: // cosmetic-only drift
		res.Verdict = VerdictPartial
	default:
		res.Verdict = VerdictBreaking
	}
	return res
}

func bothNonEmpty(a, b string) bool { return a != "" && b != "" }

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// ── Capability declaration ──────────────────────────────────────────────────

// Capability is one platform capability.
type Capability struct {
	Name      string `json:"name"` // "cards","cash","transfers","investments"
	Available bool   `json:"available"`
	// Degraded marks reduced functionality (e.g. cards auth-only, no refunds).
	Degraded bool   `json:"degraded,omitempty"`
	Note     string `json:"note,omitempty"`
}

// PlatformCapabilities is the runtime-discoverable answer to
// GET /platform-capabilities.
type PlatformCapabilities struct {
	Platform     string       `json:"platform"` // "primary" | "stand-in"
	Epoch        uint64       `json:"epoch"`
	Active       bool         `json:"active"`
	Version      string       `json:"version"`
	Updated      time.Time    `json:"updated"`
	Capabilities []Capability `json:"capabilities"`
}

// CanServe answers "can the active platform do X?" for client feature flags.
func (p *PlatformCapabilities) CanServe(capability string) bool {
	for _, c := range p.Capabilities {
		if strings.EqualFold(c.Name, capability) {
			return c.Available
		}
	}
	return false // undeclared = unavailable: fail closed
}

// Registry stores per-platform capability documents.
type Registry struct {
	mu    sync.RWMutex
	byKey map[string]*PlatformCapabilities // key: platform:version
}

func NewRegistry() *Registry { return &Registry{byKey: map[string]*PlatformCapabilities{}} }

// Publish replaces the capability document for a platform.
func (r *Registry) Publish(p *PlatformCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.Updated = time.Now().UTC()
	r.byKey[p.Platform+":"+p.Version] = p
}

// Active returns the capability document for a platform version, or nil.
func (r *Registry) Active(platform, version string) *PlatformCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[platform+":"+version]
}
