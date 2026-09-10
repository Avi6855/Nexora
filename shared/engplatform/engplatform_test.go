package engplatform

import (
	"strings"
	"testing"
	"time"
)

func TestBootstrapRequiresOwnership(t *testing.T) {
	if _, err := Bootstrap(ServiceSpec{Name: "pay", Team: ""}); err == nil {
		t.Fatal("unowned service must be rejected")
	}
	if _, err := Bootstrap(ServiceSpec{Name: ""}); err == nil {
		t.Fatal("nameless service must be rejected")
	}
	if _, err := Bootstrap(ServiceSpec{Name: "pay svc", Team: "payments", Tier: "standard"}); err == nil {
		t.Fatal("space in name must be rejected")
	}
	s, err := Bootstrap(ServiceSpec{Name: "payment-api", Team: "payments", Tier: "critical"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	paths := map[string]bool{}
	for _, f := range s.Files {
		paths[f.Path] = true
	}
	for _, want := range []string{
		"services/payment-api/OWNERS",
		"services/payment-api/tracing/tracing.go",
		"services/payment-api/.slo.yaml",
		".github/workflows/payment-api.yml",
	} {
		if !paths[want] {
			t.Fatalf("scaffold missing %s", want)
		}
	}
}

func TestGoldenPathBlocksDeploy(t *testing.T) {
	good := ServiceAudit{Service: "ok", HasHealth: true, HasTimeouts: true, HasMetrics: true,
		HasTracing: true, HasOwnership: true, SecurityScanClean: true, TestCoverage: 0.75}
	if v := Verdict(VerifyGoldenPath(good)); !v.CanDeploy {
		t.Fatalf("good service must deploy: %+v", v)
	}

	bad := good
	bad.HasTracing = false // the exact example from the spec
	bad.TestCoverage = 0.40
	v := Verdict(VerifyGoldenPath(bad))
	if v.CanDeploy {
		t.Fatal("missing tracing + low coverage must block deploy")
	}
	if len(v.Blockers) != 2 {
		t.Fatalf("expected 2 blockers, got %+v", v.Blockers)
	}
	for _, b := range v.Blockers {
		if b.Name != "tracing" && b.Name != "test_coverage" {
			t.Fatalf("unexpected blocker %s", b.Name)
		}
	}
}

func TestMaturityScoreWeakestDimension(t *testing.T) {
	m, err := ScoreMaturity(MaturityInput{Service: "cards", Reliability: 91, Security: 87,
		Observability: 30, Ownership: 100, Testing: 93, CostEfficiency: 81},
		DefaultMaturityWeights())
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if m.Weakest != "observability" || m.WeakestVal != 30 {
		t.Fatalf("weakest must be observability: %+v", m)
	}
	if m.Score >= 85 {
		t.Fatalf("collapsed observability must drag the headline below its raw mean: %.1f", m.Score)
	}
}

func TestAssessUpgradeRiskLadder(t *testing.T) {
	patch := AssessUpgrade(DependencyChange{Library: "x", From: "1.2.0", To: "1.2.1", SemverKind: "patch"})
	if patch.Risk != "LOW" {
		t.Fatalf("patch should be LOW: %+v", patch)
	}

	secFix := AssessUpgrade(DependencyChange{Library: "y", From: "1.2.0", To: "1.3.0",
		SemverKind: "minor", SecurityFixes: 2})
	if secFix.Risk != "MEDIUM" || !strings.Contains(secFix.Strategy, "expedite") {
		t.Fatalf("security fix should be expedited MEDIUM: %+v", secFix)
	}

	major := AssessUpgrade(DependencyChange{Library: "z", From: "1.9.0", To: "2.0.0",
		SemverKind: "major", UsedByTransitiveServices: 14, BreakingNotes: "API removed"})
	if major.Risk != "HIGH" || !strings.Contains(major.Strategy, "shadow") {
		t.Fatalf("major should be HIGH with staged migration: %+v", major)
	}
}

func TestVulnPrioritisationRealExposure(t *testing.T) {
	fire := Vulnerability{ID: "CVE-1", Service: "payment-api", CVSS: 7.5, Exploitable: true,
		InternetFacing: true, HandlesPII: true, ServiceTier: "critical"}
	noise := Vulnerability{ID: "CVE-2", Service: "report-tool", CVSS: 9.8, Exploitable: false,
		ServiceTier: "internal", ReachDepth: 5}
	ranked := PrioritiseVulnerabilities([]Vulnerability{noise, fire})
	if ranked[0].Vuln.ID != "CVE-1" || ranked[0].Priority != "CRITICAL" {
		t.Fatalf("reachable internet-facing vuln must outrank the CVSS-9.8 unreachable one: %+v", ranked)
	}
	if ranked[1].Vuln.ID != "CVE-2" || ranked[1].Priority != "LOW" {
		t.Fatalf("unreachable deep transitive vuln must rank LOW: %+v", ranked[1])
	}
}

func TestEphemeralEnvLifecycle(t *testing.T) {
	r := NewEnvRegistry()
	now := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	e, err := r.Grant(EnvRequest{Branch: "feature/payment-v2", Owner: "avi",
		Services: []string{"payment-api"}, TTL: 8 * time.Hour}, now)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !e.DataSanitised {
		t.Fatal("ephemeral envs must declare sanitised data")
	}
	if _, err := r.Grant(EnvRequest{Branch: "feature/payment-v2"}, now); err == nil {
		t.Fatal("duplicate live branch env must conflict")
	}
	if r.Live(now.Add(7*time.Hour)) != 1 {
		t.Fatal("env must live 8h")
	}
	reaped := r.Reap(now.Add(9 * time.Hour))
	if len(reaped) != 1 || r.Live(now.Add(9*time.Hour)) != 0 {
		t.Fatalf("env must reap after TTL: %+v", reaped)
	}
	// TTL is capped at 8h even if requested longer.
	long, _ := r.Grant(EnvRequest{Branch: "feature/x", TTL: 100 * time.Hour}, now)
	if long.ExpiresAt.Sub(now) != 8*time.Hour {
		t.Fatalf("TTL must cap at 8h, got %v", long.ExpiresAt.Sub(now))
	}
}

func TestContractRegistryBlocksBreakingProvider(t *testing.T) {
	contracts := []DependencyContract{
		{Consumer: "payment-api", Provider: "ledger", MinAPIVersion: "2.3.0", MaxLatencyMs: 500, SchemaVersion: 7},
	}
	ok := map[string]ProviderState{"ledger": {Service: "ledger", APIVersion: "2.4.0", P99LatencyMs: 120, SchemaVersion: 8}}
	if v := VerifyContracts(contracts, ok); len(v) != 0 {
		t.Fatalf("healthy provider must pass: %+v", v)
	}

	broken := map[string]ProviderState{"ledger": {Service: "ledger", APIVersion: "2.2.0", P99LatencyMs: 900, SchemaVersion: 6}}
	v := VerifyContracts(contracts, broken)
	if len(v) != 3 {
		t.Fatalf("api+latency+schema all violated, got %+v", v)
	}

	missing := map[string]ProviderState{}
	v = VerifyContracts(contracts, missing)
	if len(v) != 1 || !strings.Contains(v[0].Detail, "unknown") {
		t.Fatalf("unknown provider must violate: %+v", v)
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"2.3.0", "2.4.0", true}, {"2.10.0", "2.9.0", false}, {"v1.0.0", "1.0.1", true}, {"1.0.0", "1.0.0", false}}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Fatalf("versionLess(%s,%s)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
