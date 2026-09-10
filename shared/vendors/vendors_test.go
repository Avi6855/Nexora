package vendors

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

func TestSLABreachAndEscalation(t *testing.T) {
	s := &SLAState{Target: SLATarget{Vendor: "paypoint", Metric: "AVAILABILITY", Target: 0.999, Window: 30 * 24 * time.Hour, CreditTierBps: 250}}
	if breached, _ := s.Observe(Measurement{At: t0, Name: "AVAILABILITY", Value: 0.9995}); breached {
		t.Fatal("healthy observation must not breach")
	}
	breached, detail := s.Observe(Measurement{At: t0.Add(time.Hour), Name: "AVAILABILITY", Value: 0.98})
	if !breached {
		t.Fatal("0.98 < 0.999 must breach")
	}
	if !strings.Contains(detail, "0.9") {
		t.Fatalf("detail must carry evidence: %s", detail)
	}
	esc := BuildEscalation(s, "outage 14:00–15:00", t0.Add(2*time.Hour))
	if esc.Vendor != "paypoint" || esc.ServiceCreditBps != 250 {
		t.Fatalf("escalation package incomplete: %+v", esc)
	}
	if len(esc.Evidence) != 2 {
		t.Fatalf("evidence trail must include all measurements: %d", len(esc.Evidence))
	}
	if !strings.Contains(esc.CustomerImpact, "material outage") {
		t.Fatalf("impact must escalate on <0.99: %s", esc.CustomerImpact)
	}
	// Recovery re-arms; next breach fires again.
	if breached, _ := s.Observe(Measurement{At: t0.Add(3 * time.Hour), Name: "AVAILABILITY", Value: 0.9999}); breached {
		t.Fatal("recovered observation must clear breach")
	}
	if breached, _ := s.Observe(Measurement{At: t0.Add(4 * time.Hour), Name: "AVAILABILITY", Value: 0.5}); !breached {
		t.Fatal("re-breach after recovery must fire")
	}
	// Latency is a ceiling metric.
	lat := &SLAState{Target: SLATarget{Vendor: "kyc", Metric: "LATENCY_P99", Target: 800}}
	if breached, _ := lat.Observe(Measurement{At: t0, Value: 400}); breached {
		t.Fatal("400ms < 800ms target is healthy")
	}
	if breached, _ := lat.Observe(Measurement{At: t0.Add(time.Minute), Value: 1200}); !breached {
		t.Fatal("1200ms > 800ms must breach")
	}
}

func TestCapabilityRegistryAndMaintenance(t *testing.T) {
	reg := NewRegistry()
	reg.Register(VendorRecord{Name: "vendorA", Capabilities: []Capability{
		{Name: "PAYMENTS", Version: "2.1", Regions: []string{"UK", "EU"}, Limits: map[string]int64{"max_amount_minor": 100000}},
		{Name: "REFUNDS", Version: "1.0", Regions: []string{"UK"}},
	}})
	reg.Register(VendorRecord{Name: "vendorB", Capabilities: []Capability{
		{Name: "PAYMENTS", Version: "1.9", Regions: []string{"GLOBAL"}},
	}})
	ok, why := reg.Supports("vendorA", "PAYMENTS", "UK", t0)
	if !ok {
		t.Fatalf("vendorA payments UK must work: %s", why)
	}
	if ok, why := reg.Supports("vendorA", "REFUNDS", "EU", t0); ok {
		t.Fatalf("refunds are UK-only: %s", why)
	}
	if ok, _ := reg.Supports("vendorA", "REFUNDS", "UK", t0); !ok {
		t.Fatal("refunds UK must work")
	}
	if ok, _ := reg.Supports("vendorB", "REFUNDS", "UK", t0); ok {
		t.Fatal("vendorB has no refunds")
	}
	if ok, _ := reg.Supports("ghost", "PAYMENTS", "UK", t0); ok {
		t.Fatal("unknown vendor must fail")
	}
	// Maintenance window blocks routing at 2:30am but not 4am.
	reg.Register(VendorRecord{Name: "vendorC", Capabilities: []Capability{{Name: "PAYMENTS", Version: "1", Regions: []string{"GLOBAL"}}},
		Maintenance: []MaintenanceWindow{{Vendor: "vendorC", Start: t0.Add(2 * time.Hour), End: t0.Add(3 * time.Hour), Summary: "schema upgrade"}}})
	if ok, why := reg.Supports("vendorC", "PAYMENTS", "UK", t0.Add(150*time.Minute)); ok {
		t.Fatal("routing must be blocked inside the window")
	} else if !strings.Contains(why, "maintenance") {
		t.Fatalf("reason must mention maintenance: %s", why)
	}
	if ok, _ := reg.Supports("vendorC", "PAYMENTS", "UK", t0.Add(4*time.Hour)); !ok {
		t.Fatal("routing resumes after the window")
	}
}

func TestCredentialRotationLadder(t *testing.T) {
	now := t0
	r := NewRotator(func() time.Time { return now })
	old, _ := r.Issue("cred-old", "paypoint", "API_KEY", 90*24*time.Hour, []string{"payment-service", "card-service"})
	if got := RotationStage(*old, now); got != RotationHealthy {
		t.Fatalf("fresh credential %s, want HEALTHY", got)
	}

	// Age it toward expiry and check the warning ladder.
	now = t0.Add(83 * 24 * time.Hour) // 7 days remaining
	if got := RotationStage(*old, now); got != RotationWarn7 {
		t.Fatalf("stage %s, want WARN_7D", got)
	}
	now = t0.Add(89 * 24 * time.Hour) // 1 day remaining
	if got := RotationStage(*old, now); got != RotationCritical {
		t.Fatalf("stage %s, want CRITICAL_1D", got)
	}
	due := r.Due()
	if len(due) == 0 || due[0].ID != "cred-old" {
		t.Fatalf("due list must surface the credential: %+v", due)
	}

	// Rotate: issue overlapping credential, deploy to ALL services, verify, revoke.
	now = t0.Add(89 * 24 * time.Hour)
	_, _ = r.Issue("cred-new", "paypoint", "API_KEY", 90*24*time.Hour, nil)
	if err := r.Verify("cred-new", "cred-old"); err == nil {
		t.Fatal("verification must fail before full deployment")
	}
	_ = r.Deploy("cred-new", "payment-service")
	if err := r.Verify("cred-new", "cred-old"); err == nil {
		t.Fatal("verification must fail with card-service still on old credential")
	}
	_ = r.Deploy("cred-new", "card-service")
	if err := r.Verify("cred-new", "cred-old"); err != nil {
		t.Fatalf("dual-active verification must pass: %v", err)
	}
	if err := r.Revoke("cred-old"); err != nil {
		t.Fatal(err)
	}
	if old.Active {
		t.Fatal("old credential must be inactive after revocation")
	}
	// Expired new credential cannot pass verification.
	now = t0.Add(200 * 24 * time.Hour)
	if err := r.Verify("cred-new", "cred-old"); err == nil {
		t.Fatal("expired credential must fail verification")
	}
	if got := RotationStage(*old, now); got != RotationExpired {
		t.Fatalf("stage %s, want EXPIRED", got)
	}
}

func TestMaintenanceCoordinator(t *testing.T) {
	reg := NewRegistry()
	reg.Register(VendorRecord{Name: "paypoint", Capabilities: []Capability{
		{Name: "CASH_DEPOSIT", Version: "1"}, {Name: "STATUS_WEBHOOK", Version: "1"},
	}})
	c := NewCoordinator(reg,
		map[string][]string{ // service → vendors
			"cash-deposit-service": {"paypoint"},
			"card-service":         {"someother"},
		},
		map[string][]string{ // journey → services
			"deposit-cash":    {"cash-deposit-service"},
			"pay-contactless": {"card-service"},
		},
		map[string]string{
			"CASH_DEPOSIT": "hide deposit option; show 'try again in an hour'",
		},
	)
	w := MaintenanceWindow{Vendor: "paypoint", Start: t0.Add(2 * time.Hour), End: t0.Add(3 * time.Hour), Summary: "network upgrade"}
	imp := c.Assess(w)
	if len(imp.Capabilities) != 2 {
		t.Fatalf("capabilities affected: %+v", imp.Capabilities)
	}
	if len(imp.Services) != 1 || imp.Services[0] != "cash-deposit-service" {
		t.Fatalf("services affected: %+v", imp.Services)
	}
	if len(imp.Journeys) != 1 || imp.Journeys[0] != "deposit-cash" {
		t.Fatalf("journeys affected: %+v", imp.Journeys)
	}
	if !strings.Contains(imp.DegradeMode, "hide deposit option") {
		t.Fatalf("degraded mode must come from policy map: %s", imp.DegradeMode)
	}
	if strings.Contains(imp.Recommended, "no customer-facing") {
		t.Fatalf("impact exists so recommendation must be proactive: %s", imp.Recommended)
	}
}
