package mldata

import (
	"testing"
	"time"
)

func TestImpactSimulate(t *testing.T) {
	g := NewImpactGraph()
	edges := [][2]string{
		{"feat-txn-amount", "model-fraud-v3"},
		{"model-fraud-v3", "dash-fraud-ops"},
		{"model-fraud-v3", "report-reg-42"},
		{"dash-fraud-ops", "regulatory-filing"},
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1], "feeds"); err != nil {
			t.Fatal(err)
		}
	}
	imp, err := g.Simulate("model-fraud-v3")
	if err != nil {
		t.Fatal(err)
	}
	if imp.Count != 3 {
		t.Fatalf("expected blast radius 3, got %+v", imp)
	}
	want := map[string]bool{"dash-fraud-ops": true, "report-reg-42": true, "regulatory-filing": true}
	for _, n := range imp.Impacted {
		if !want[n] {
			t.Fatalf("unexpected impacted node %q in %v", n, imp.Impacted)
		}
	}
	leaf, err := g.Simulate("regulatory-filing")
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Count != 0 {
		t.Fatalf("leaf must have zero blast radius, got %+v", leaf)
	}
	if _, err := g.Simulate("ghost"); err == nil {
		t.Fatalf("expected unknown node error")
	}
}

func TestFreshnessGateway(t *testing.T) {
	g := NewFreshnessGateway()
	if err := g.SetSLA("txn-amount", time.Hour); err != nil {
		t.Fatal(err)
	}
	allow, err := g.Check("txn-amount", 30*time.Minute)
	if err != nil || allow.Verdict != FreshAllow {
		t.Fatalf("expected ALLOW, got %+v %v", allow, err)
	}
	warn, err := g.Check("txn-amount", 50*time.Minute)
	if err != nil || warn.Verdict != FreshWarn {
		t.Fatalf("expected WARN past 80%% SLA, got %+v %v", warn, err)
	}
	block, err := g.Check("txn-amount", 2*time.Hour)
	if err != nil || block.Verdict != FreshBlock {
		t.Fatalf("expected BLOCK, got %+v %v", block, err)
	}
	if _, err := g.Check("unknown-feat", time.Minute); err == nil {
		t.Fatalf("expected unknown feature error")
	}
	if err := g.SetSLA("", time.Hour); err == nil {
		t.Fatalf("expected feature validation error")
	}
}

func TestDriftMonitor(t *testing.T) {
	m := NewDriftMonitor(20)
	if err := m.SetBaseline("score", Distribution{Median: 100, P95: 200}); err != nil {
		t.Fatal(err)
	}
	ok, err := m.Check("score", Distribution{Median: 105, P95: 205})
	if err != nil || ok.Alert {
		t.Fatalf("expected no alert for small drift, got %+v %v", ok, err)
	}
	bad, err := m.Check("score", Distribution{Median: 150, P95: 205})
	if err != nil || !bad.Alert {
		t.Fatalf("expected drift alert, got %+v %v", bad, err)
	}
	if _, err := m.Check("missing", Distribution{}); err == nil {
		t.Fatalf("expected no-baseline error")
	}
}

func TestDependencyRegistry(t *testing.T) {
	r := NewDependencyRegistry()
	m := ModelDeps{ID: "fraud-v3", Features: []string{"txn-amount"}, Datasets: []string{"txns"}, Policies: []string{"pii-redact"}}
	if err := r.Register(m); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(m); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
	healthy, err := r.VerifyDeps("fraud-v3", map[string]bool{"txns": true})
	if err != nil || !healthy.Healthy {
		t.Fatalf("expected healthy, got %+v %v", healthy, err)
	}
	sick, err := r.VerifyDeps("fraud-v3", map[string]bool{"txns": false})
	if err != nil || sick.Healthy {
		t.Fatalf("expected unhealthy on sick dataset, got %+v %v", sick, err)
	}
	if _, err := r.VerifyDeps("ghost", nil); err == nil {
		t.Fatalf("expected unknown model error")
	}
	thin := ModelDeps{ID: "thin"}
	if err := r.Register(thin); err != nil {
		t.Fatal(err)
	}
	res, err := r.VerifyDeps("thin", nil)
	if err != nil || res.Healthy {
		t.Fatalf("model without features/policies must verify unhealthy, got %+v", res)
	}
}

func TestRollbackCompat(t *testing.T) {
	safe := VerifyRollbackCompat(
		map[string]string{"amount": "float", "currency": "string"},
		map[string]string{"amount": "float", "currency": "string", "extra": "int"},
	)
	if safe.Verdict != RollbackSafe {
		t.Fatalf("expected ROLLBACK_SAFE, got %+v", safe)
	}
	missing := VerifyRollbackCompat(
		map[string]string{"amount": "float", "new-field": "string"},
		map[string]string{"amount": "float"},
	)
	if missing.Verdict != RollbackUnsafe || len(missing.Mismatches) == 0 {
		t.Fatalf("expected UNSAFE on missing field, got %+v", missing)
	}
	mismatch := VerifyRollbackCompat(
		map[string]string{"amount": "float"},
		map[string]string{"amount": "int"},
	)
	if mismatch.Verdict != RollbackUnsafe {
		t.Fatalf("expected UNSAFE on type mismatch, got %+v", mismatch)
	}
}
