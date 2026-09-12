package releaseops

import (
	"testing"

	shared "github.com/nexora/nexora/shared/releaseops"
)

func TestForecastWiring(t *testing.T) {
	s := NewService()
	if err := s.AddSamples("payments", []float64{100, 200, 300}); err != nil {
		t.Fatal(err)
	}
	fc, err := s.Forecast("payments", nil, []shared.EventUplift{{Name: "payday", Multiplier: 1.5}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ForecastRPS != 300 || fc.Replicas != 3 {
		t.Fatalf("200×1.5=300 → 3: %+v", fc)
	}
	if _, err := s.Forecast("ghost", nil, nil, 10); err == nil {
		t.Fatal("no samples must fail")
	}
}

func TestReleaseWiring(t *testing.T) {
	s := NewService()
	if err := s.RegisterDep("checkout", "ledger"); err != nil {
		t.Fatal(err)
	}
	ok, _ := s.GateDeploy("checkout")
	if !ok {
		t.Fatal("healthy allows")
	}
	if err := s.ReportHealth("ledger", shared.HealthDegraded); err != nil {
		t.Fatal(err)
	}
	if ok, reason := s.GateDeploy("checkout"); ok || reason[:7] != "BLOCKED" {
		t.Fatalf("blocked: %v %q", ok, reason)
	}
	if _, err := s.StartCanary("checkout", "v2"); err != shared.ErrDeployBlocked {
		t.Fatalf("blocked start: %v", err)
	}
	if err := s.ReportHealth("ledger", shared.HealthHealthy); err != nil {
		t.Fatal(err)
	}
	c, err := s.StartCanary("checkout", "v2")
	if err != nil || c.Stage != 1 {
		t.Fatalf("start: %+v %v", c, err)
	}
	c, err = s.AdvanceCanary(c.ID)
	if err != nil || c.Stage != 25 {
		t.Fatalf("advance: %+v %v", c, err)
	}
	if _, err := s.AdvanceCanary("missing"); err != shared.ErrUnknownCanary {
		t.Fatalf("unknown: %v", err)
	}
}

func TestObservatoryWiring(t *testing.T) {
	s := NewService()
	for i := 0; i < 100; i++ {
		if err := s.RecordUsage("GET /v1/x", "old", i < 2); err != nil {
			t.Fatal(err)
		}
	}
	safe, st, err := s.SafeToRemove("GET /v1/x", "old", 5)
	if err != nil || !safe || st.UsagePct != 2 {
		t.Fatalf("safe: %v %+v %v", safe, st, err)
	}
	if err := s.Deprecate("GET /v1/x", "old"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.FieldStats("GET /v1/x", "old")
	if !st.Deprecated {
		t.Fatalf("deprecated: %+v", st)
	}
	if _, err := s.FieldStats("GET /v1/x", "missing"); err != shared.ErrUnknownField {
		t.Fatalf("unknown: %v", err)
	}
}
