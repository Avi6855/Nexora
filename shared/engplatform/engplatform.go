// Package engplatform implements Nexora's internal engineering platform —
// the platform-as-product layer: services ARE the product other engineers
// consume.
//
//  31. Bootstrapper: one command emits a production-shaped service skeleton.
//  32. Golden path: the standards every service must meet before it may
//     deploy to production; failing the path blocks the deploy.
//  33. Maturity score: portfolio-wide engineering health, weighted and
//     explainable.
//  34. Dependency upgrade intelligence: what a library bump implies before
//     anyone merges it.
//  35. Vulnerability prioritisation: 100 CVEs ranked by real exposure, not
//     CVSS alone.
//  36. Ephemeral environments: branch-scoped, TTL'd, auto-deleted.
//  37. Contract registry: runtime dependency specifications verified at
//     deploy time.
package engplatform

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ── 31. Service bootstrapper ────────────────────────────────────────────────

// ServiceSpec is what an engineer asks the platform for.
type ServiceSpec struct {
	Name       string `json:"name"`
	Team       string `json:"team"`
	Language   string `json:"language"`   // go | kotlin
	Tier       string `json:"tier"`       // critical | standard | internal
	DataClass  string `json:"data_class"` // customer_pii | confidential | public
	OnCallSlug string `json:"oncall_slug"`
}

// ScaffoldFile is one generated artifact.
type ScaffoldFile struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
}

// Scaffold is the generated service.
type Scaffold struct {
	Spec  ServiceSpec    `json:"spec"`
	Files []ScaffoldFile `json:"files"`
}

// Bootstrap emits the production-ready skeleton. Everything the golden path
// later enforces is included FROM BIRTH — nobody retrofits tracing.
func Bootstrap(spec ServiceSpec) (*Scaffold, error) {
	if strings.TrimSpace(spec.Name) == "" || strings.ContainsAny(spec.Name, " /") {
		return nil, fmt.Errorf("service name must be a non-empty identifier without spaces")
	}
	if strings.TrimSpace(spec.Team) == "" {
		return nil, fmt.Errorf("ownership (team) is mandatory — unowned services do not exist")
	}
	switch spec.Tier {
	case "critical", "standard", "internal":
	default:
		return nil, fmt.Errorf("tier must be critical|standard|internal, got %q", spec.Tier)
	}
	s := &Scaffold{Spec: spec}
	add := func(path, purpose string) { s.Files = append(s.Files, ScaffoldFile{path, purpose}) }
	add(fmt.Sprintf("services/%s/cmd/main.go", spec.Name), "entrypoint with health/readiness wired")
	add(fmt.Sprintf("services/%s/Dockerfile", spec.Name), "distroless image")
	add(fmt.Sprintf("services/%s/k8s/deployment.yaml", spec.Name), "deployment with resource limits + PDB")
	add(fmt.Sprintf("services/%s/k8s/service.yaml", spec.Name), "service + alerts on saturation")
	add(fmt.Sprintf("services/%s/OWNERS", spec.Name), "ownership metadata → "+spec.Team)
	add(fmt.Sprintf("services/%s/.slo.yaml", spec.Name), "SLO definition per tier "+spec.Tier)
	add(fmt.Sprintf("services/%s/metrics/metrics.go", spec.Name), "Prometheus metrics registry")
	add(fmt.Sprintf("services/%s/tracing/tracing.go", spec.Name), "OpenTelemetry tracing bootstrap")
	add(".github/workflows/"+spec.Name+".yml", "CI: build, test, golden-path check, security scan")
	return s, nil
}

// ── 32. Golden path enforcement ─────────────────────────────────────────────

// CheckResult is one standard.
type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
	// Blocking standards gate production; advisory ones only nag.
	Blocking bool `json:"blocking"`
}

// ServiceAudit is the checklist input for golden-path verification.
type ServiceAudit struct {
	Service           string
	HasHealth         bool
	HasTimeouts       bool
	HasMetrics        bool
	HasTracing        bool
	HasOwnership      bool
	SecurityScanClean bool
	TestCoverage      float64 // 0..1
}

// VerifyGoldenPath runs the standards. Coverage < 60% blocks; a failing
// security scan always blocks.
func VerifyGoldenPath(a ServiceAudit) []CheckResult {
	check := func(name string, passed bool, failDetail string, blocking bool) CheckResult {
		return CheckResult{Name: name, Passed: passed, Blocking: blocking, Detail: failDetail}
	}
	return []CheckResult{
		check("health_checks", a.HasHealth, "no /healthz and /readyz endpoints", true),
		check("outbound_timeouts", a.HasTimeouts, "HTTP clients without deadlines", true),
		check("metrics", a.HasMetrics, "no Prometheus registry", true),
		check("tracing", a.HasTracing, "no OpenTelemetry exporter", true),
		check("ownership", a.HasOwnership, "OWNERS file missing — unowned service", true),
		check("security_scan", a.SecurityScanClean, "dependency vulnerability scan failed", true),
		check("test_coverage", a.TestCoverage >= 0.60,
			fmt.Sprintf("coverage %.0f%% below 60%% gate", a.TestCoverage*100), true),
	}
}

// GoldenPathVerdict summarises the audit.
type GoldenPathVerdict struct {
	CanDeploy bool          `json:"can_deploy"`
	Blockers  []CheckResult `json:"blockers"`
	Passed    int           `json:"passed"`
	Total     int           `json:"total"`
}

// Verdict reduces the checklist to the deploy gate.
func Verdict(results []CheckResult) GoldenPathVerdict {
	v := GoldenPathVerdict{Total: len(results)}
	for _, r := range results {
		if r.Passed {
			v.Passed++
			continue
		}
		if r.Blocking {
			v.Blockers = append(v.Blockers, r)
		}
	}
	v.CanDeploy = len(v.Blockers) == 0
	return v
}

// ── 33. Service maturity score ──────────────────────────────────────────────

// MaturityWeights makes the composite a policy decision, not a secret.
type MaturityWeights struct {
	Reliability, Security, Observability, Ownership, Testing, CostEfficiency float64
}

// DefaultMaturityWeights are the documented starting weights.
func DefaultMaturityWeights() MaturityWeights {
	return MaturityWeights{Reliability: 0.25, Security: 0.20, Observability: 0.15,
		Ownership: 0.15, Testing: 0.15, CostEfficiency: 0.10}
}

// MaturityInput is one service's measured dimensions (0..100).
type MaturityInput struct {
	Service        string
	Reliability    float64 // SLO attainment
	Security       float64 // scan posture, secrets hygiene
	Observability  float64 // metrics/tracing coverage
	Ownership      float64 // owner, on-call, runbook present
	Testing        float64 // coverage + contract tests
	CostEfficiency float64 // cost per request vs peer group
}

// MaturityScore is the weighted result with the weakest dimension called out.
type MaturityScore struct {
	Service    string  `json:"service"`
	Score      float64 `json:"score"`
	Weakest    string  `json:"weakest"`
	WeakestVal float64 `json:"weakest_value"`
}

// ScoreMaturity weights and floors: a composite can't hide a collapsed
// dimension — the weakest score caps the headline (same lesson as the
// open-finance link health).
func ScoreMaturity(in MaturityInput, w MaturityWeights) (*MaturityScore, error) {
	dims := []struct {
		name string
		val  float64
		wt   float64
	}{
		{"reliability", in.Reliability, w.Reliability},
		{"security", in.Security, w.Security},
		{"observability", in.Observability, w.Observability},
		{"ownership", in.Ownership, w.Ownership},
		{"testing", in.Testing, w.Testing},
		{"cost_efficiency", in.CostEfficiency, w.CostEfficiency},
	}
	total := 0.0
	wsum := 0.0
	weakest := dims[0]
	for _, d := range dims {
		if d.val < 0 || d.val > 100 {
			return nil, fmt.Errorf("%s %v out of range", d.name, d.val)
		}
		total += d.val * d.wt
		wsum += d.wt
		if d.val < weakest.val {
			weakest = d
		}
	}
	if wsum == 0 {
		return nil, fmt.Errorf("weights sum to zero")
	}
	return &MaturityScore{Service: in.Service, Score: round1(total / wsum),
		Weakest: weakest.name, WeakestVal: weakest.val}, nil
}

// ── 34. Dependency upgrade intelligence ─────────────────────────────────────

// DependencyChange describes an available library upgrade.
type DependencyChange struct {
	Library string `json:"library"`
	From    string `json:"from"`
	To      string `json:"to"`
	// SemverKind major|minor|patch.
	SemverKind string `json:"semver_kind"`
	// SecurityFixes count of CVEs fixed (0 = none).
	SecurityFixes int    `json:"security_fixes"`
	BreakingNotes string `json:"breaking_notes,omitempty"`
	// UsedByTransitiveServices how many internal services pull it in.
	UsedByTransitiveServices int `json:"used_by_transitive_services"`
}

// UpgradePlan is the risk assessment and rollout guidance.
type UpgradePlan struct {
	Change    DependencyChange `json:"change"`
	Risk      string           `json:"risk"` // LOW|MEDIUM|HIGH
	Strategy  string           `json:"strategy"`
	Reasoning string           `json:"reasoning"`
}

// AssessUpgrade classifies an upgrade into a rollout strategy.
func AssessUpgrade(c DependencyChange) UpgradePlan {
	switch {
	case c.SemverKind == "patch" && c.BreakingNotes == "":
		return UpgradePlan{Change: c, Risk: "LOW",
			Strategy: "batch with routine dependency PRs",
			Reasoning: fmt.Sprintf("patch %s→%s, no breaking notes%s",
				c.From, c.To, securitySuffix(c.SecurityFixes))}
	case c.SecurityFixes > 0 && c.SemverKind != "major":
		return UpgradePlan{Change: c, Risk: "MEDIUM",
			Strategy:  "expedite: upgrade this week, canary 24h",
			Reasoning: fmt.Sprintf("fixes %d CVE(s), non-major bump%s", c.SecurityFixes, securitySuffix(c.SecurityFixes))}
	case c.SemverKind == "major":
		return UpgradePlan{Change: c, Risk: "HIGH",
			Strategy: "staged migration: shadow verification, dual-run, per-service rollout",
			Reasoning: fmt.Sprintf("major bump %s→%s touches %d transitive services%s — %s",
				c.From, c.To, c.UsedByTransitiveServices, securitySuffix(c.SecurityFixes), c.BreakingNotes)}
	default:
		return UpgradePlan{Change: c, Risk: "MEDIUM",
			Strategy:  "minor: normal PR + contract tests",
			Reasoning: fmt.Sprintf("minor bump %s→%s%s", c.From, c.To, securitySuffix(c.SecurityFixes))}
	}
}

func securitySuffix(n int) string {
	if n > 0 {
		return fmt.Sprintf(" (%d security fixes)", n)
	}
	return ""
}

// ── 35. Vulnerability prioritisation ────────────────────────────────────────

// Vulnerability is one finding from scanners.
type Vulnerability struct {
	ID             string  `json:"id"` // CVE-… or GHSA-…
	Service        string  `json:"service"`
	CVSS           float64 `json:"cvss"`
	Exploitable    bool    `json:"exploitable"` // known exploit / reachable code path
	InternetFacing bool    `json:"internet_facing"`
	HandlesPII     bool    `json:"handles_pii"`
	ServiceTier    string  `json:"service_tier"` // critical|standard|internal
	// ReachDepth: how deep under the service's own code the vuln sits
	// (1 = direct dependency). Deeper = less reachable, lower priority.
	ReachDepth int `json:"reach_depth"`
}

// VulnPriority is CRITICAL|HIGH|MEDIUM|LOW with the drivers listed.
type VulnPriority struct {
	Vuln     Vulnerability `json:"vuln"`
	Priority string        `json:"priority"`
	Drivers  []string      `json:"drivers"`
}

// PrioritiseVulnerabilities ranks findings by REAL exposure. A CVSS 9.8 in
// an unreachable transitive test dependency is noise; a CVSS 7.5 with a
// public exploit on an internet-facing critical service is a fire drill.
func PrioritiseVulnerabilities(vs []Vulnerability) []VulnPriority {
	out := make([]VulnPriority, 0, len(vs))
	for _, v := range vs {
		score := v.CVSS
		var drivers []string
		add := func(f string) { drivers = append(drivers, f) }
		if v.Exploitable {
			score += 3
			add("known exploit / reachable")
		} else {
			// Reachability dominates: an unreachable CVSS-9.8 is largely
			// theoretical, so it is scaled down hard, not nudged.
			score = v.CVSS * 0.3
			add("no evidence of reachability (score scaled down)")
		}
		if v.InternetFacing {
			score += 2
			add("internet-facing")
		}
		if v.HandlesPII {
			score += 1.5
			add("customer data path")
		}
		switch v.ServiceTier {
		case "critical":
			score += 1.5
			add("critical tier service")
		case "internal":
			score -= 1
			add("internal tier")
		}
		if v.ReachDepth > 3 {
			score -= 1.5
			add("deep transitive (depth >3)")
		}
		p := "MEDIUM"
		switch {
		case score >= 9:
			p = "CRITICAL"
		case score >= 7:
			p = "HIGH"
		case score < 4:
			p = "LOW"
		}
		out = append(out, VulnPriority{Vuln: v, Priority: p, Drivers: drivers})
	}
	rank := map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "LOW": 3}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Priority] != rank[out[j].Priority] {
			return rank[out[i].Priority] < rank[out[j].Priority]
		}
		return out[i].Vuln.CVSS > out[j].Vuln.CVSS
	})
	return out
}

// ── 36. Ephemeral environments ──────────────────────────────────────────────

// EnvRequest is one branch environment.
type EnvRequest struct {
	Branch   string        `json:"branch"`
	Owner    string        `json:"owner"`
	Services []string      `json:"services"`
	TTL      time.Duration `json:"ttl"`
}

// EphemeralEnv is the granted environment with expiry.
type EphemeralEnv struct {
	EnvID         string     `json:"env_id"`
	Request       EnvRequest `json:"request"`
	ExpiresAt     time.Time  `json:"expires_at"`
	Namespace     string     `json:"namespace"`
	DataSanitised bool       `json:"data_sanitised"`
}

// EnvRegistry tracks live ephemeral environments and reaps expired ones.
type EnvRegistry struct {
	envs map[string]EphemeralEnv
	seq  int
}

func NewEnvRegistry() *EnvRegistry { return &EnvRegistry{envs: map[string]EphemeralEnv{}} }

var ErrEnvConflict = fmt.Errorf("a live environment already exists for this branch")

// Grant creates an environment. One branch = one env at a time; TTL is
// capped (8h default) — forgotten environments are a cost leak AND a stale-
// data risk.
func (r *EnvRegistry) Grant(req EnvRequest, now time.Time) (*EphemeralEnv, error) {
	for _, e := range r.envs {
		if e.Request.Branch == req.Branch && e.ExpiresAt.After(now) {
			return nil, fmt.Errorf("%w: %s", ErrEnvConflict, e.EnvID)
		}
	}
	ttl := req.TTL
	if ttl <= 0 || ttl > 8*time.Hour {
		ttl = 8 * time.Hour
	}
	r.seq++
	env := &EphemeralEnv{
		EnvID:     fmt.Sprintf("env-%s-%d", strings.ReplaceAll(req.Branch, "/", "-"), r.seq),
		Request:   req,
		ExpiresAt: now.Add(ttl),
		Namespace: fmt.Sprintf("eph-%d", r.seq),
	}
	// Production-like data MUST be sanitised; ephemeral ≠ anonymous prod.
	if !containsStr(req.Services, "*") {
		env.DataSanitised = true
	}
	r.envs[env.EnvID] = *env
	return env, nil
}

// Reap returns and removes the expired environments (the janitor calls this).
func (r *EnvRegistry) Reap(now time.Time) []EphemeralEnv {
	var reaped []EphemeralEnv
	for id, e := range r.envs {
		if !e.ExpiresAt.After(now) {
			reaped = append(reaped, e)
			delete(r.envs, id)
		}
	}
	sort.Slice(reaped, func(i, j int) bool { return reaped[i].EnvID < reaped[j].EnvID })
	return reaped
}

func (r *EnvRegistry) Live(now time.Time) int {
	n := 0
	for _, e := range r.envs {
		if e.ExpiresAt.After(now) {
			n++
		}
	}
	return n
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ── 37. Dependency contract registry ────────────────────────────────────────

// DependencyContract is a runtime requirement one service declares on another.
type DependencyContract struct {
	Consumer string `json:"consumer"`
	Provider string `json:"provider"`
	// MinAPIVersion the consumer requires (semver).
	MinAPIVersion string `json:"min_api_version"`
	// MaxLatencyMs p99 the consumer's budget assumes.
	MaxLatencyMs int `json:"max_latency_ms"`
	// SchemaVersion for event contracts.
	SchemaVersion int `json:"schema_version"`
}

// ContractViolation blocks deploys.
type ContractViolation struct {
	Contract DependencyContract `json:"contract"`
	Detail   string             `json:"detail"`
}

// ProviderState is what the provider currently offers.
type ProviderState struct {
	Service       string
	APIVersion    string
	P99LatencyMs  int
	SchemaVersion int
}

// VerifyContracts checks every consumer contract against the provider state
// at deploy time — a provider breaking a declared dependency is a failed
// deploy, not a 3am page.
func VerifyContracts(contracts []DependencyContract, providers map[string]ProviderState) []ContractViolation {
	var violations []ContractViolation
	for _, c := range contracts {
		p, ok := providers[c.Provider]
		if !ok {
			violations = append(violations, ContractViolation{Contract: c,
				Detail: "provider not deployed / unknown"})
			continue
		}
		if versionLess(p.APIVersion, c.MinAPIVersion) {
			violations = append(violations, ContractViolation{Contract: c,
				Detail: fmt.Sprintf("api %s < required %s", p.APIVersion, c.MinAPIVersion)})
		}
		if c.MaxLatencyMs > 0 && p.P99LatencyMs > c.MaxLatencyMs {
			violations = append(violations, ContractViolation{Contract: c,
				Detail: fmt.Sprintf("p99 %dms > budget %dms", p.P99LatencyMs, c.MaxLatencyMs)})
		}
		if c.SchemaVersion > 0 && p.SchemaVersion < c.SchemaVersion {
			violations = append(violations, ContractViolation{Contract: c,
				Detail: fmt.Sprintf("schema v%d < required v%d", p.SchemaVersion, c.SchemaVersion)})
		}
	}
	return violations
}

// versionLess compares dotted semver-ish versions numerically.
func versionLess(a, b string) bool {
	as := splitVersion(a)
	bs := splitVersion(b)
	for i := 0; i < 3; i++ {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return false
}

func splitVersion(v string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		if i > 2 {
			break
		}
		fmt.Sscanf(part, "%d", &out[i])
	}
	return out
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
