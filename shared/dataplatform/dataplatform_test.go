package dataplatform

import (
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func goodContract() QualityContract {
	return QualityContract{
		Dataset: "transactions", Consumer: "risk-models",
		MaxFreshness: 15 * time.Minute, MaxNullRatePct: 0.1, MaxDupRatePct: 0,
		MinRows: 9000, MaxRows: 11000,
	}
}

func TestQualityContractPass(t *testing.T) {
	v := EvaluateContract(goodContract(), QualityMeasurement{
		Dataset: "transactions", CheckedAt: tnow, NewestRowAge: 5 * time.Minute,
		NullRatePct: 0.01, DupRatePct: 0, Rows: 10000,
	})
	if !v.Passed || v.Quarantine {
		t.Fatalf("healthy dataset must pass: %+v", v)
	}
}

func TestQualityContractViolationsQuarantine(t *testing.T) {
	c := goodContract()
	// Freshness breach.
	v := EvaluateContract(c, QualityMeasurement{NewestRowAge: 30 * time.Minute, Rows: 10000})
	if v.Passed || !v.Quarantine || len(v.Violations) != 1 || v.Violations[0].Field != "freshness" {
		t.Fatalf("freshness breach: %+v", v)
	}
	// Duplicate keys are never tolerated.
	v = EvaluateContract(c, QualityMeasurement{NewestRowAge: time.Minute, Rows: 10000, DupRatePct: 0.5})
	if v.Violations[0].Field != "duplicate_rate" {
		t.Fatalf("dup breach: %+v", v)
	}
	// Volume collapse — the partition at 3% of normal is an outage, not a quiet day.
	v = EvaluateContract(c, QualityMeasurement{NewestRowAge: time.Minute, Rows: 300})
	if v.Violations[0].Field != "row_volume_min" {
		t.Fatalf("collapse must trip the floor: %+v", v)
	}
	// Volume explosion — duplicate ingestion.
	v = EvaluateContract(c, QualityMeasurement{NewestRowAge: time.Minute, Rows: 50000})
	if v.Violations[0].Field != "row_volume_max" {
		t.Fatalf("explosion must trip the ceiling: %+v", v)
	}
}

func TestRegistryHealth(t *testing.T) {
	r := NewDatasetRegistry()
	r.Register("txns", "payments-team", "CRITICAL", map[string]string{"id": "string", "amount": "decimal"}, tnow)
	r.RecordVerdict("txns", EvaluateContract(goodContract(), QualityMeasurement{Rows: 10000}))
	if h, _ := r.Health("txns"); h != HealthHealthy {
		t.Fatalf("healthy: %s", h)
	}
	// Open incident degrades.
	r.OpenIncident("txns", Incident{ID: "i1", OpenedAt: tnow, Severity: "SEV3", Summary: "late partition"})
	if h, _ := r.Health("txns"); h != HealthDegraded {
		t.Fatalf("incident must degrade: %s", h)
	}
	// Quarantine breaks.
	r.RecordVerdict("txns", EvaluateContract(goodContract(), QualityMeasurement{Rows: 5}))
	if h, _ := r.Health("txns"); h != HealthBroken {
		t.Fatalf("quarantine must break: %s", h)
	}
	// Unowned dataset is visibly unowned, not silently healthy.
	r.Register("orphan", "", "STANDARD", map[string]string{"x": "int"}, tnow)
	if h, _ := r.Health("orphan"); h != HealthUnowned {
		t.Fatalf("unowned: %s", h)
	}
	// Schema hash is stable and order-independent.
	h1 := SchemaHash(map[string]string{"a": "int", "b": "string"})
	h2 := SchemaHash(map[string]string{"b": "string", "a": "int"})
	if h1 != h2 || h1 == "" {
		t.Fatal("schema hash must be canonical")
	}
}

func TestQueryGateway(t *testing.T) {
	p := DefaultGovernancePolicy()
	// No PII → allow.
	v, why := EvaluateQuery(QueryRequest{Engineer: "e", Purpose: "ANALYTICS", Environment: "PRODUCTION", Dataset: "txns"}, p)
	if v != QueryAllow {
		t.Fatalf("no-PII query: %s %s", v, why)
	}
	// Unapproved purpose + PII → deny.
	v, _ = EvaluateQuery(QueryRequest{Purpose: "ANALYTICS", Environment: "PRODUCTION", PIIColumns: []string{"name"}}, p)
	if v != QueryDeny {
		t.Fatalf("analytics over PII must be denied: %s", v)
	}
	// Production PII without ticket → deny.
	v, why = EvaluateQuery(QueryRequest{Purpose: "SUPPORT_TICKET", Environment: "PRODUCTION", PIIColumns: []string{"name"}}, p)
	if v != QueryDeny || why == "" {
		t.Fatalf("production PII needs a ticket: %s %s", v, why)
	}
	// With ticket, raw PII still redacted.
	v, _ = EvaluateQuery(QueryRequest{Purpose: "SUPPORT_TICKET", Environment: "PRODUCTION", PIIColumns: []string{"name"}, TicketRef: "T-1", NeedsRawPII: true}, p)
	if v != QueryRedact {
		t.Fatalf("raw PII must be redacted even with ticket: %s", v)
	}
	// Staging bulk export without raw need → aggregate at k.
	v, _ = EvaluateQuery(QueryRequest{Purpose: "SUPPORT_TICKET", Environment: "SANDBOX", PIIColumns: []string{"postcode"}}, p)
	if v != QueryAggregate {
		t.Fatalf("aggregate expected: %s", v)
	}
}

func TestAccessSessionRecording(t *testing.T) {
	a := NewAccessRecorder()
	if _, err := a.StartSession("s1", "eng", "", tnow); err == nil {
		t.Fatal("no ticket, no session")
	}
	s, err := a.StartSession("s2", "eng", "T-99", tnow)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(AccessEvent{Engineer: "eng", Customer: "c1", Fields: []string{"balance"}, Reason: "support"})
	s.Record(AccessEvent{Engineer: "eng", Customer: "c2", Fields: []string{"name", "address"}, Reason: "support"})
	if !s.VerifyChain() {
		t.Fatal("fresh chain must verify")
	}
	// Tampering breaks the chain.
	s.Events[0].Customer = "forged"
	if s.VerifyChain() {
		t.Fatal("tampered chain must fail verification")
	}
}

func TestMigrationSafetyPipeline(t *testing.T) {
	e := NewMigrationEngine()
	// Contractive change still read by consumers → refused at the door.
	_, err := e.Submit(MigrationRequest{ID: "m0", Target: "txns", DropColumn: "legacy_ref", ReadByConsumers: []string{"risk-model"}}, tnow)
	if err == nil {
		t.Fatal("drop column with readers must be refused")
	}
	if _, err := e.Submit(MigrationRequest{ID: "m1", Target: "txns", Statement: "ADD COLUMN country TEXT", EstRows: 1_000_000}, tnow); err != nil {
		t.Fatal(err)
	}
	// Apply before pipeline → gate error.
	if err := e.Apply("m1"); err == nil {
		t.Fatal("apply before canary must be gated")
	}
	if err := e.Validate("m1"); err != nil {
		t.Fatal(err)
	}
	if stage, _ := e.Stage("m1"); stage != MigEstimated {
		t.Fatalf("stage %s", stage)
	}
	if err := e.Canary("m1", 1, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply("m1"); err != nil {
		t.Fatal(err)
	}
	if stage, _ := e.Stage("m1"); stage != MigComplete {
		t.Fatalf("expected complete, got %s", stage)
	}
	// Completed migration cannot roll back.
	if err := e.Rollback("m1"); err == nil {
		t.Fatal("completed migration needs a reverse migration, not rollback")
	}
}

func TestMigrationCanaryFailureBlocksApply(t *testing.T) {
	e := NewMigrationEngine()
	if _, err := e.Submit(MigrationRequest{ID: "m2", Target: "txns", EstRows: 100}, tnow); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate("m2"); err != nil {
		t.Fatal(err)
	}
	if err := e.Canary("m2", 1, true); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply("m2"); err == nil {
		t.Fatal("failed canary must block apply")
	}
	// Rollback path works pre-completion.
	if err := e.Rollback("m2"); err != nil {
		t.Fatal(err)
	}
	if stage, _ := e.Stage("m2"); stage != MigRolledBack {
		t.Fatalf("stage %s", stage)
	}
}

func TestMigrationPauseResume(t *testing.T) {
	e := NewMigrationEngine()
	if _, err := e.Submit(MigrationRequest{ID: "m3", Target: "txns", EstRows: 100}, tnow); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate("m3"); err != nil {
		t.Fatal(err)
	}
	if err := e.Canary("m3", 1, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Pause("m3"); err != nil {
		t.Fatal(err)
	}
	if stage, _ := e.Stage("m3"); stage != MigPaused {
		t.Fatalf("stage %s", stage)
	}
	stage, err := e.Resume("m3")
	if err != nil || stage != MigCanary {
		t.Fatalf("resume: %s %v", stage, err)
	}
	// Pause from a non-running stage is invalid.
	if err := e.Pause("m3"); err == nil && stage == MigComplete {
		t.Fatal("cannot pause a finished migration")
	}
}

func TestMigrationVerificationFailureRepairs(t *testing.T) {
	e := NewMigrationEngine()
	if _, err := e.Submit(MigrationRequest{ID: "m4", Target: "txns", EstRows: 100}, tnow); err != nil {
		t.Fatal(err)
	}
	_ = e.Validate("m4")
	_ = e.Canary("m4", 5, false)
	if err := e.MarkVerifyFailed("m4"); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply("m4"); err != nil {
		t.Fatal(err)
	}
	if stage, _ := e.Stage("m4"); stage != MigRepairing {
		t.Fatalf("verification mismatch must route to repair: %s", stage)
	}
}
