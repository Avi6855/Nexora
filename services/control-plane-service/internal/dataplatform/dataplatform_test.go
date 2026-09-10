package dataplatform

import (
	"testing"
	"time"

	shared "github.com/nexora/nexora/shared/dataplatform"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func goodContract() shared.QualityContract {
	return shared.QualityContract{
		Dataset: "transactions", Consumer: "risk-models",
		MaxFreshness: 15 * time.Minute, MaxNullRatePct: 0.1, MaxDupRatePct: 0,
		MinRows: 9000, MaxRows: 11000,
	}
}

func TestContractEvaluation(t *testing.T) {
	s := NewService()
	v, err := s.PublishContractEvaluation(goodContract(), shared.QualityMeasurement{
		Dataset: "transactions", CheckedAt: testNow, NewestRowAge: 5 * time.Minute,
		NullRatePct: 0.01, Rows: 10000,
	})
	if err != nil || !v.Passed || v.Quarantine {
		t.Fatalf("healthy must pass: %+v %v", v, err)
	}
	v, _ = s.PublishContractEvaluation(goodContract(), shared.QualityMeasurement{NewestRowAge: time.Hour, Rows: 10000})
	if v.Passed || !v.Quarantine {
		t.Fatalf("stale must quarantine: %+v", v)
	}
	if _, err := s.PublishContractEvaluation(shared.QualityContract{}, shared.QualityMeasurement{}); err == nil {
		t.Fatal("empty dataset must fail")
	}
}

func TestDatasetRegistryHealth(t *testing.T) {
	s := NewService()
	if _, err := s.RegisterDataset("", "team", "CRITICAL", nil, testNow); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := s.RegisterDataset("txns", "payments-team", "CRITICAL", map[string]string{"id": "string"}, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DatasetHealth("ghost"); err == nil {
		t.Fatal("unknown dataset must fail")
	}
	if h, _ := s.DatasetHealth("txns"); h != shared.HealthHealthy {
		t.Fatalf("healthy: %s", h)
	}
	if err := s.OpenDatasetIncident("txns", shared.Incident{ID: "i1", OpenedAt: testNow, Severity: "SEV3", Summary: "late"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.DatasetHealth("txns"); h != shared.HealthDegraded {
		t.Fatalf("incident degrades: %s", h)
	}
	if err := s.OpenDatasetIncident("ghost", shared.Incident{ID: "i2", Summary: "x"}); err == nil {
		t.Fatal("incident on unknown must fail")
	}
	if err := s.OpenDatasetIncident("txns", shared.Incident{Summary: "no id"}); err == nil {
		t.Fatal("incident without id must fail")
	}
}

func TestQueryGateway(t *testing.T) {
	s := NewService()
	v, _ := s.EvaluateQuery(shared.QueryRequest{Engineer: "e", Purpose: "ANALYTICS", Environment: "PRODUCTION", Dataset: "txns"})
	if v != shared.QueryAllow {
		t.Fatalf("no PII allows: %s", v)
	}
	v, _ = s.EvaluateQuery(shared.QueryRequest{Purpose: "ANALYTICS", Environment: "PRODUCTION", Dataset: "txns", PIIColumns: []string{"name"}})
	if v != shared.QueryDeny {
		t.Fatalf("unapproved PII denies: %s", v)
	}
	v, _ = s.EvaluateQuery(shared.QueryRequest{Purpose: "SUPPORT_TICKET", Environment: "PRODUCTION", Dataset: "txns", PIIColumns: []string{"name"}, TicketRef: "T-1", NeedsRawPII: true})
	if v != shared.QueryRedact {
		t.Fatalf("raw PII redacts: %s", v)
	}
}

func TestAccessSessions(t *testing.T) {
	s := NewService()
	if _, err := s.StartAccessSession("s1", "eng", "", testNow); err == nil {
		t.Fatal("ticket is mandatory")
	}
	if _, err := s.StartAccessSession("", "eng", "T-1", testNow); err == nil {
		t.Fatal("id is mandatory")
	}
	if _, err := s.StartAccessSession("s1", "eng", "T-99", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAccessSession("s1", "eng", "T-99", testNow); err == nil {
		t.Fatal("duplicate session must conflict")
	}
	if err := s.RecordAccess("s1", shared.AccessEvent{Engineer: "eng", Customer: "c1", Fields: []string{"balance"}, Reason: "support"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAccess("ghost", shared.AccessEvent{Engineer: "eng", Customer: "c1", Fields: []string{"x"}, Reason: "y"}); err == nil {
		t.Fatal("unknown session must fail")
	}
	valid, err := s.VerifyAccessChain("s1")
	if err != nil || !valid {
		t.Fatalf("fresh chain verifies: %v %v", valid, err)
	}
	if _, err := s.VerifyAccessChain("ghost"); err == nil {
		t.Fatal("verify unknown must fail")
	}
	// Tampering breaks the chain.
	sess, _ := s.AccessSessionInfo("s1")
	sess.Events[0].Customer = "forged"
	valid, _ = s.VerifyAccessChain("s1")
	if valid {
		t.Fatal("tampered chain must fail")
	}
}

func TestMigrationPipeline(t *testing.T) {
	s := NewService()
	if _, err := s.SubmitMigration(shared.MigrationRequest{Target: "txns"}, testNow); err == nil {
		t.Fatal("id is mandatory")
	}
	if _, err := s.SubmitMigration(shared.MigrationRequest{ID: "m0", Target: "txns", DropColumn: "c", ReadByConsumers: []string{"r"}}, testNow); err == nil {
		t.Fatal("contractive drop with readers must be refused")
	}
	if _, err := s.SubmitMigration(shared.MigrationRequest{ID: "m1", Target: "txns", Statement: "ADD COLUMN", EstRows: 1000}, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitMigration(shared.MigrationRequest{ID: "m1", Target: "txns"}, testNow); err == nil {
		t.Fatal("duplicate migration must conflict")
	}
	if err := s.ApplyMigration("m1"); err == nil {
		t.Fatal("apply before canary must gate")
	}
	if err := s.ValidateMigration("m1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.MigrationStage("m1"); st != shared.MigEstimated {
		t.Fatalf("stage %s", st)
	}
	if err := s.CanaryMigration("m1", 1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyMigration("m1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.MigrationStage("m1"); st != shared.MigComplete {
		t.Fatalf("complete: %s", st)
	}
	if err := s.RollbackMigration("m1"); err == nil {
		t.Fatal("completed cannot roll back")
	}
	if _, err := s.MigrationStage("ghost"); err == nil {
		t.Fatal("unknown stage must fail")
	}
}

func TestMigrationPauseResumeRollback(t *testing.T) {
	s := NewService()
	if _, err := s.SubmitMigration(shared.MigrationRequest{ID: "m2", Target: "txns", EstRows: 100}, testNow); err != nil {
		t.Fatal(err)
	}
	_ = s.ValidateMigration("m2")
	_ = s.CanaryMigration("m2", 5, false)
	if err := s.PauseMigration("m2"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.MigrationStage("m2"); st != shared.MigPaused {
		t.Fatalf("paused: %s", st)
	}
	stage, err := s.ResumeMigration("m2")
	if err != nil || stage != shared.MigCanary {
		t.Fatalf("resume: %s %v", stage, err)
	}
	if err := s.RollbackMigration("m2"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.MigrationStage("m2"); st != shared.MigRolledBack {
		t.Fatalf("rolled back: %s", st)
	}
	// Verify-failed routes to repair.
	if _, err := s.SubmitMigration(shared.MigrationRequest{ID: "m3", Target: "txns", EstRows: 10}, testNow); err != nil {
		t.Fatal(err)
	}
	_ = s.ValidateMigration("m3")
	_ = s.CanaryMigration("m3", 5, false)
	if err := s.FailVerification("m3"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyMigration("m3"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.MigrationStage("m3"); st != shared.MigRepairing {
		t.Fatalf("repair: %s", st)
	}
}
