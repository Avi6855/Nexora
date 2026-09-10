package compliance

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)

func TestRuleAcknowledgementGating(t *testing.T) {
	d := NewRuleDistributor([]string{"payment", "card", "ledger"}, func() time.Time { return t0 })
	err := d.Publish(RuleVersion{ID: "R-41", Version: 1, Payload: "limit=75000", ActivateAfter: t0})
	if err != nil {
		t.Fatal(err)
	}
	// No acks yet → not activated, effective version stays the old one (error
	// here since there is no previous complete version).
	if st, _ := d.ActivateWhenReady("R-41"); st.Activated {
		t.Fatal("rule must not activate with zero acknowledgements")
	}
	if _, err := d.EffectiveVersion("R-41", t0); err == nil {
		t.Fatal("no complete version means no effective version")
	}
	// Partial acks.
	if err := d.Acknowledge("R-41", 1, "payment"); err != nil {
		t.Fatal(err)
	}
	st, _ := d.ActivateWhenReady("R-41")
	if len(st.Missing) != 2 || !strings.Contains(strings.Join(st.Missing, ","), "card") {
		t.Fatalf("missing services must be named: %+v", st)
	}
	if err := d.Acknowledge("R-41", 1, "card"); err != nil {
		t.Fatal(err)
	}
	if err := d.Acknowledge("R-41", 1, "ledger"); err != nil {
		t.Fatal(err)
	}
	st, _ = d.ActivateWhenReady("R-41")
	if !st.Activated {
		t.Fatalf("all acked + activation time passed: %+v", st)
	}
	if v, err := d.EffectiveVersion("R-41", t0); err != nil || v != 1 {
		t.Fatalf("effective version %d err %v", v, err)
	}
	// Stale acknowledgement (older version) is rejected.
	if err := d.Publish(RuleVersion{ID: "R-41", Version: 2, Payload: "limit=80000", ActivateAfter: t0}); err != nil {
		t.Fatal(err)
	}
	if err := d.Acknowledge("R-41", 1, "payment"); err == nil {
		t.Fatal("acknowledging a superseded version must fail")
	}
	// Until all services ack v2, v1 keeps ruling.
	if v, err := d.EffectiveVersion("R-41", t0); err != nil || v != 1 {
		t.Fatalf("partial v2 acks: v1 must stay effective, got v%d err %v", v, err)
	}
	_ = d.Acknowledge("R-41", 2, "payment")
	_ = d.Acknowledge("R-41", 2, "card")
	_ = d.Acknowledge("R-41", 2, "ledger")
	if v, _ := d.EffectiveVersion("R-41", t0); v != 2 {
		t.Fatalf("v2 must take over after full acks, got v%d", v)
	}
}

func TestTimeTravelReconstruction(t *testing.T) {
	s := NewTimeTravelStore()
	v1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	v2 := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := s.AddPolicy("cash-limit", PolicySnapshot{Version: 12, Body: "limit=500", EffectiveFrom: v1, EffectiveUntil: v2}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPolicy("cash-limit", PolicySnapshot{Version: 13, Body: "limit=750", EffectiveFrom: v2}); err != nil {
		t.Fatal(err)
	}
	// Overlap is rejected — ambiguity is the enemy of audit.
	if err := s.AddPolicy("cash-limit", PolicySnapshot{Version: 14, Body: "x", EffectiveFrom: v2}); err == nil {
		t.Fatal("overlapping policy windows must fail")
	}
	// Customer state evolves.
	s.AddState("c1", CustomerStateAt{At: v1, Field: "kyc_tier", Value: "BASIC"})
	s.AddState("c1", CustomerStateAt{At: time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC), Field: "kyc_tier", Value: "FULL"})

	june := time.Date(2024, 6, 12, 12, 0, 0, 0, time.UTC)
	rec, err := s.Reconstruct("tx-9", "cash-limit", "c1", june, func(body string, state map[string]string) (string, string) {
		return "ALLOWED", "policy " + body + " tier " + state["kyc_tier"]
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.PolicyVersion != 12 || !strings.Contains(rec.PolicyBody, "500") {
		t.Fatalf("June 2024 must use v12: %+v", rec)
	}
	if rec.CustomerState["kyc_tier"] != "BASIC" {
		t.Fatalf("June 2024 must see BASIC tier, got %q", rec.CustomerState["kyc_tier"])
	}
	// A date before any policy exists errors honestly.
	if _, err := s.Reconstruct("tx-9", "cash-limit", "c1", time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), nil); err == nil {
		t.Fatal("no policy in force must error, not guess")
	}
	// Today sees v13 + FULL.
	today, _ := s.Reconstruct("tx-10", "cash-limit", "c1", t0, func(body string, state map[string]string) (string, string) {
		return "ALLOWED", body + "/" + state["kyc_tier"]
	})
	if today.PolicyVersion != 13 || today.CustomerState["kyc_tier"] != "FULL" {
		t.Fatalf("today must use v13+FULL: %+v", today)
	}
}

func TestEvidenceProvenance(t *testing.T) {
	good := EvidenceFigure{
		Name: "total_deposits_gbp", ValueMinor: 2481920000, Currency: "GBP",
		ModelVersion: "reg-model-v7",
		Chain: []LineageStep{
			{Stage: "SOURCE", Detail: "ledger.ledger_entries", DatasetIDs: []string{"ds-1", "ds-2"}},
			{Stage: "QUERY", Detail: "sum(amount) where currency=GBP"},
			{Stage: "TRANSFORM", Detail: "aggregate by month"},
		},
	}
	if err := ValidateProvenance(good); err != nil {
		t.Fatalf("complete chain must validate: %v", err)
	}
	// deep copies: Chain shares backing arrays across struct copies, so
	// mutating a variant's steps would corrupt the shared `good` figure.
	clone := func(f EvidenceFigure) EvidenceFigure {
		c := f
		c.Chain = append([]LineageStep(nil), f.Chain...)
		for i := range c.Chain {
			c.Chain[i].DatasetIDs = append([]string(nil), c.Chain[i].DatasetIDs...)
		}
		return c
	}
	// Missing source.
	noSrc := clone(good)
	noSrc.Chain = noSrc.Chain[1:]
	if err := ValidateProvenance(noSrc); err == nil {
		t.Fatal("chain without SOURCE must fail")
	}
	// Source with no datasets.
	badSrc := clone(good)
	badSrc.Chain[0].DatasetIDs = nil
	if err := ValidateProvenance(badSrc); err == nil {
		t.Fatal("SOURCE without dataset ids must fail")
	}
	// Missing transform.
	noTf := clone(good)
	noTf.Chain = noTf.Chain[:1]
	if err := ValidateProvenance(noTf); err == nil {
		t.Fatal("chain without TRANSFORM must fail")
	}
	// Unpinned model.
	noModel := good
	noModel.ModelVersion = ""
	if err := ValidateProvenance(noModel); err == nil {
		t.Fatal("unpinned model version must fail")
	}
	// Report submission validates all figures.
	r := Report{Regime: "FCA", Period: "2026-08", Figures: []EvidenceFigure{good}}
	if err := Submit(r); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.Figures = []EvidenceFigure{good, noModel}
	if err := Submit(bad); err == nil {
		t.Fatal("one bad figure blocks the whole submission")
	}
}

func TestImpactAnalysis(t *testing.T) {
	g := ServiceGraph{
		Services: map[string][]string{
			"payment-service": {"ledger_entries"},
			"card-service":    {"ledger_entries", "card_credentials"},
			"search-service":  {"search_index"},
		},
		Endpoints: map[string][]string{
			"payment-service": {"POST /payments", "GET /payments"},
			"card-service":    {"POST /cards"},
			"search-service":  {"GET /search"},
		},
		Journeys: map[string][]string{
			"pay-contactless": {"card-service"},
			"send-money":      {"payment-service", "card-service"},
			"search-txns":     {"search-service"},
		},
		CustomersPerModel: map[string]int{"ledger_entries": 420000, "card_credentials": 400000, "search_index": 300000},
	}
	imp := AnalyzeImpact("R-50", []string{"ledger_entries"}, g)
	want := map[string]bool{"payment-service": true, "card-service": true}
	if len(imp.Services) != 2 {
		t.Fatalf("services: %+v", imp.Services)
	}
	for s := range want {
		if !contains(imp.Services, s) {
			t.Fatalf("missing service %s: %+v", s, imp.Services)
		}
	}
	if contains(imp.Services, "search-service") {
		t.Fatal("search-service untouched by ledger_entries")
	}
	if len(imp.Endpoints) != 3 {
		t.Fatalf("endpoints: %+v", imp.Endpoints)
	}
	if len(imp.Journeys) != 2 || !contains(imp.Journeys, "pay-contactless") {
		t.Fatalf("journeys: %+v", imp.Journeys)
	}
	if imp.CustomersAffected != 420000 {
		t.Fatalf("customers %d, want 420000 (one model)", imp.CustomersAffected)
	}
}

func TestConfigLifecycleGates(t *testing.T) {
	p := NewConfigPlatform()
	validated := false
	p.AddValidator(func(c ConfigChange) error {
		validated = true
		if c.NewValue == "boom" {
			return errors.New("value out of range")
		}
		return nil
	})
	p.AddSimulator(func(c ConfigChange) error {
		if c.NewValue == "bad-sim" {
			return errors.New("simulation: payouts would fail")
		}
		return nil
	})
	ch, err := p.Propose(ConfigChange{ID: "cfg-1", Target: "payment-service", Key: "transfer_limit_minor", OldValue: "250000", NewValue: "500000"})
	if err != nil {
		t.Fatal(err)
	}
	// Approval before validation is rejected.
	if err := p.Approve("cfg-1", "alice"); err == nil {
		t.Fatal("cannot approve an unvalidated change")
	}
	if err := p.Validate("cfg-1"); err != nil {
		t.Fatal(err)
	}
	if !validated {
		t.Fatal("validator must run")
	}
	if err := p.Approve("cfg-1", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := p.Simulate("cfg-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Release("cfg-1", t0); err != nil {
		t.Fatal(err)
	}
	if ch.RollbackTo != "250000" {
		t.Fatalf("rollback target %q, want 250000", ch.RollbackTo)
	}
	if v, err := p.Rollback("cfg-1"); err != nil || v != "250000" {
		t.Fatalf("rollback to %q err %v", v, err)
	}
	if _, err := p.Rollback("cfg-1"); err == nil {
		t.Fatal("double rollback must fail")
	}
	// Validation failure blocks the path.
	ch2, _ := p.Propose(ConfigChange{ID: "cfg-2", Target: "payment-service", Key: "k", OldValue: "1", NewValue: "boom"})
	if err := p.Validate("cfg-2"); err == nil {
		t.Fatal("failing validator must block")
	}
	_ = ch2
	// Simulation failure blocks release.
	_, _ = p.Propose(ConfigChange{ID: "cfg-3", Target: "payment-service", Key: "k", OldValue: "1", NewValue: "bad-sim"})
	_ = p.Validate("cfg-3")
	_ = p.Approve("cfg-3", "bob")
	if err := p.Simulate("cfg-3"); err == nil {
		t.Fatal("failing simulator must block")
	}
}

func TestDriftDetection(t *testing.T) {
	desired := map[string]map[string]string{
		"payment-service": {"timeout_ms": "500", "retries": "3"},
		"card-service":    {"timeout_ms": "500"},
	}
	actual := map[string]map[string]string{
		"payment-service": {"timeout_ms": "5000", "retries": "3"}, // drifted timeout
		"card-service":    {"timeout_ms": "500"},                  // clean
		"fraud-service":   {"timeout_ms": "100"},                  // undeclared target
	}
	drifts := DetectDrift(desired, actual)
	if len(drifts) != 2 {
		t.Fatalf("want payment timeout drift + fraud undeclared, got %+v", drifts)
	}
	found := false
	for _, d := range drifts {
		if d.Target == "payment-service" && d.Key == "timeout_ms" && d.Desired == "500" && d.Actual == "5000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("timeout drift not detected: %+v", drifts)
	}
	// Perfect match yields no drift.
	if got := DetectDrift(desired, map[string]map[string]string{
		"payment-service": {"timeout_ms": "500", "retries": "3"},
		"card-service":    {"timeout_ms": "500"},
	}); len(got) != 0 {
		t.Fatalf("clean fleet must have zero drift: %+v", got)
	}
}

func TestSafeMode(t *testing.T) {
	c := NewSafeModeController(DefaultSafeModePolicy(), 10*time.Minute)
	if !c.Policy.Allows(ModeNormal, CapMoveMoney) {
		t.Fatal("normal mode allows money movement")
	}
	// Escalation ladder: 3 failures → DEGRADED, 6 → SAFE_MODE.
	now := t0
	for i := 0; i < 3; i++ {
		c.RecordFailure(now)
	}
	if c.Mode != ModeDegraded {
		t.Fatalf("3 failures must reach DEGRADED, got %s", c.Mode)
	}
	if !c.Policy.Allows(c.Mode, CapMoveMoney) {
		t.Fatal("degraded keeps money movement")
	}
	if c.Policy.Allows(c.Mode, CapChangeDetails) {
		t.Fatal("degraded blocks account detail changes")
	}
	for i := 0; i < 3; i++ {
		c.RecordFailure(now)
	}
	if c.Mode != ModeSafe {
		t.Fatalf("6 failures must reach SAFE_MODE, got %s", c.Mode)
	}
	if c.Policy.Allows(c.Mode, CapMoveMoney) {
		t.Fatal("safe mode blocks money movement")
	}
	if !c.Policy.Allows(c.Mode, CapReadBalance) {
		t.Fatal("safe mode keeps balance visible")
	}
	if c.CustomerMessage() == "" {
		t.Fatal("safe mode must explain itself to customers")
	}
	// De-escalation needs sustained health, not one success.
	c.RecordSuccess(now.Add(time.Minute))
	if c.Mode != ModeSafe {
		t.Fatalf("one success must not de-escalate, got %s", c.Mode)
	}
	c.RecordSuccess(now.Add(11 * time.Minute))
	if c.Mode != ModeDegraded {
		t.Fatalf("sustained health steps down to DEGRADED, got %s", c.Mode)
	}
	c.RecordSuccess(now.Add(42 * time.Minute))
	if c.Mode != ModeNormal {
		t.Fatalf("sustained health returns to NORMAL, got %s", c.Mode)
	}
}
