package releaseops

import (
	"testing"
)

func TestForecastBaselineAndUplifts(t *testing.T) {
	f := NewForecaster()
	if err := f.AddSamples("payments", []float64{100, 200, 300}); err != nil {
		t.Fatal(err)
	}
	fc, err := f.Forecast("payments", nil, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if fc.BaselineRPS != 200 || fc.ForecastRPS != 200 || fc.Replicas != 2 {
		t.Fatalf("baseline mean 200 → 2 replicas: %+v", fc)
	}
	// Payday uplift ×1.5 and black-friday ×3 compound.
	fc, err = f.Forecast("payments", nil, []EventUplift{
		{Name: "payday", Multiplier: 1.5},
		{Name: "black-friday", Multiplier: 3},
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ForecastRPS != 900 || fc.Replicas != 9 {
		t.Fatalf("200×4.5=900 → 9 replicas: %+v", fc)
	}
	if len(fc.AppliedUplifts) != 2 {
		t.Fatalf("uplifts recorded: %+v", fc)
	}
	// Explicit samples override stored ones; ceiling rounds up.
	fc, err = f.Forecast("ledger", []float64{10, 15}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if fc.BaselineRPS != 12.5 || fc.Replicas != 2 {
		t.Fatalf("12.5/10 ceils to 2: %+v", fc)
	}
}

func TestForecastValidation(t *testing.T) {
	f := NewForecaster()
	if err := f.AddSamples("", []float64{1}); err == nil {
		t.Fatal("empty service must fail")
	}
	if err := f.AddSamples("a", nil); err == nil {
		t.Fatal("empty samples must fail")
	}
	if err := f.AddSamples("a", []float64{-1}); err == nil {
		t.Fatal("negative samples must fail")
	}
	if _, err := f.Forecast("ghost", nil, nil, 10); err == nil {
		t.Fatal("no samples must fail")
	}
	if _, err := f.Forecast("a", []float64{1}, nil, 0); err == nil {
		t.Fatal("non-positive capacity must fail")
	}
	if _, err := f.Forecast("a", []float64{1}, []EventUplift{{Name: "x", Multiplier: 0}}, 10); err == nil {
		t.Fatal("non-positive uplift must fail")
	}
}

func TestDeployGateBlockedByDependency(t *testing.T) {
	m := NewReleaseManager()
	if err := m.RegisterDep("checkout", "ledger"); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterDep("checkout", "fraud"); err != nil {
		t.Fatal(err)
	}
	ok, reason := m.GateDeploy("checkout")
	if !ok {
		t.Fatalf("healthy graph allows: %s", reason)
	}
	if err := m.ReportHealth("ledger", HealthDegraded); err != nil {
		t.Fatal(err)
	}
	ok, reason = m.GateDeploy("checkout")
	if ok {
		t.Fatal("degraded dependency must block")
	}
	if reason == "" || reason[:7] != "BLOCKED" {
		t.Fatalf("BLOCKED verdict: %q", reason)
	}
	// The degraded service itself is blocked too.
	if ok, _ := m.GateDeploy("ledger"); ok {
		t.Fatal("degraded service blocks itself")
	}
	// Unrelated services still deploy.
	if ok, _ := m.GateDeploy("fraud"); !ok {
		t.Fatal("healthy service must stay allowed")
	}
	if err := m.ReportHealth("ledger", "NOPE"); err == nil {
		t.Fatal("unknown health must fail")
	}
	if err := m.RegisterDep("a", "a"); err == nil {
		t.Fatal("self-dependency must fail")
	}
}

func TestCanaryStagesWithChecks(t *testing.T) {
	m := NewReleaseManager()
	if err := m.RegisterDep("checkout", "ledger"); err != nil {
		t.Fatal(err)
	}
	c, err := m.StartCanary("checkout", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if c.Stage != 1 || c.Status != "RUNNING" {
		t.Fatalf("canary starts at 1%%: %+v", c)
	}
	// A dependency degrading mid-canary blocks advancement.
	if err := m.ReportHealth("ledger", HealthDegraded); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdvanceCanary(c.ID); err != ErrDeployBlocked {
		t.Fatalf("mid-canary degradation blocks: %v", err)
	}
	if err := m.ReportHealth("ledger", HealthHealthy); err != nil {
		t.Fatal(err)
	}
	c, err = m.AdvanceCanary(c.ID)
	if err != nil || c.Stage != 25 {
		t.Fatalf("advance to 25%%: %+v %v", c, err)
	}
	c, err = m.AdvanceCanary(c.ID)
	if err != nil || c.Stage != 100 || c.Status != "RUNNING" {
		t.Fatalf("advance to 100%%: %+v %v", c, err)
	}
	c, err = m.AdvanceCanary(c.ID)
	if err != nil || c.Status != "COMPLETE" {
		t.Fatalf("100%% advances to complete: %+v %v", c, err)
	}
	if _, err := m.AdvanceCanary(c.ID); err != ErrCanaryComplete {
		t.Fatalf("completed canary: %v", err)
	}
	if _, err := m.AdvanceCanary("missing"); err != ErrUnknownCanary {
		t.Fatalf("unknown canary: %v", err)
	}
	// Cannot start a canary while blocked.
	if err := m.ReportHealth("ledger", HealthDegraded); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartCanary("checkout", "v3"); err != ErrDeployBlocked {
		t.Fatalf("blocked start: %v", err)
	}
	if _, err := m.GetCanary("missing"); err != ErrUnknownCanary {
		t.Fatalf("unknown get: %v", err)
	}
}

func TestContractObservatory(t *testing.T) {
	o := NewObservatory()
	// legacy_field: 2 uses in 100 samples → 2%.
	for i := 0; i < 100; i++ {
		if err := o.RecordUsage("GET /v1/accounts", "legacy_field", i < 2); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 50; i++ {
		if err := o.RecordUsage("GET /v1/accounts", "balance", true); err != nil {
			t.Fatal(err)
		}
	}
	st, err := o.Stats("GET /v1/accounts", "legacy_field")
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 100 || st.Used != 2 || st.UsagePct != 2 {
		t.Fatalf("stats: %+v", st)
	}
	safe, _, err := o.SafeToRemove("GET /v1/accounts", "legacy_field", 5)
	if err != nil || !safe {
		t.Fatalf("2%% < 5%% is safe: %v %v", safe, err)
	}
	safe, _, err = o.SafeToRemove("GET /v1/accounts", "balance", 5)
	if err != nil || safe {
		t.Fatalf("100%% is never safe: %v %v", safe, err)
	}
	if err := o.Deprecate("GET /v1/accounts", "legacy_field"); err != nil {
		t.Fatal(err)
	}
	st, _ = o.Stats("GET /v1/accounts", "legacy_field")
	if !st.Deprecated {
		t.Fatalf("deprecation workflow: %+v", st)
	}
	if _, _, err := o.SafeToRemove("GET /v1/x", "nope", 5); err != ErrUnknownField {
		t.Fatalf("unknown field: %v", err)
	}
	if err := o.Deprecate("GET /v1/x", "nope"); err != ErrUnknownField {
		t.Fatalf("deprecate unknown: %v", err)
	}
	if err := o.RecordUsage("", "f", true); err == nil {
		t.Fatal("empty endpoint must fail")
	}
}
