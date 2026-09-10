package openbanking

import (
	"strings"
	"testing"
	"time"

	shared "github.com/nexora/nexora/shared/openbanking"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func TestRecoveryLifecycle(t *testing.T) {
	s := NewService()
	plan, err := s.ReportFailure("conn-1", shared.FailRateLimited, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MaxAttempts != 5 {
		t.Fatalf("rate-limit plan attempts: %+v", plan)
	}
	if _, err := s.ReportFailure("", shared.FailRateLimited, 0); err == nil {
		t.Fatal("empty connection must fail")
	}
	if _, err := s.ReportFailure("conn-x", shared.FailureKind("NOPE"), 0); err == nil {
		t.Fatal("unknown kind must fail")
	}
	ok, wait, err := s.ShouldRetry("conn-1", testNow)
	if err != nil || !ok || wait != 30*time.Second {
		t.Fatalf("first retry: %v %v %v", ok, wait, err)
	}
	if _, _, err := s.ShouldRetry("missing", testNow); err == nil {
		t.Fatal("unknown connection must 404")
	}
	if err := s.RecordAttempt("conn-1", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt("missing", testNow); err == nil {
		t.Fatal("record on unknown must fail")
	}
	exhausted, err := s.ConnectionExhausted("conn-1")
	if err != nil || exhausted {
		t.Fatalf("not exhausted yet: %v %v", exhausted, err)
	}
	for i := 0; i < plan.MaxAttempts; i++ {
		_ = s.RecordAttempt("conn-1", testNow)
	}
	exhausted, _ = s.ConnectionExhausted("conn-1")
	if !exhausted {
		t.Fatal("budget must exhaust")
	}
	if ok, _ := func() (bool, time.Duration) { ok, w, _ := s.ShouldRetry("conn-1", testNow); return ok, w }(); ok {
		t.Fatal("exhausted must not retry")
	}
	// Non-retryable lane surfaces immediately.
	if _, err := s.ReportFailure("conn-2", shared.FailConsentExpired, 0); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.ShouldRetry("conn-2", testNow); ok {
		t.Fatal("customer-reauth lane must not auto-retry")
	}
}

func TestFreshnessDefaults(t *testing.T) {
	s := NewService()
	// Defaults: balance 5m, transactions 30m; nothing refreshed yet → UNKNOWN.
	if got := s.DatasetFreshness("balance", testNow); got.Verdict != shared.FreshUnknown {
		t.Fatalf("no data must be unknown: %+v", got)
	}
	if err := s.SetSLA("", time.Minute); err == nil {
		t.Fatal("empty dataset must fail")
	}
	if err := s.SetSLA("balance", 0); err == nil {
		t.Fatal("non-positive SLA must fail")
	}
	if err := s.RecordRefresh("balance", testNow.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := s.DatasetFreshness("balance", testNow); got.Verdict != shared.FreshFresh {
		t.Fatalf("2m-old balance must be fresh: %+v", got)
	}
	if err := s.RecordRefresh("transactions", testNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := s.DatasetFreshness("transactions", testNow); got.Verdict != shared.FreshStale {
		t.Fatalf("1h-old transactions must be stale: %+v", got)
	}
	verdict, reports := s.OverallFreshness(testNow)
	if verdict != shared.FreshStale {
		t.Fatalf("worst wins: %s %+v", verdict, reports)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 default datasets: %+v", reports)
	}
	// Custom SLA + explicit evaluation records state.
	if err := s.SetSLA("payments", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	rep := s.DatasetFreshnessAt("payments", testNow.Add(-time.Minute), testNow)
	if rep.Verdict != shared.FreshFresh {
		t.Fatalf("explicit last-good: %+v", rep)
	}
}

func TestCapabilityRegistry(t *testing.T) {
	s := NewService()
	if err := s.RegisterProvider("", []shared.Capability{shared.CapBalance}); err == nil {
		t.Fatal("empty name must fail")
	}
	if err := s.RegisterProvider("BankA", nil); err == nil {
		t.Fatal("empty declared must fail")
	}
	if err := s.RegisterProvider("BankA", []shared.Capability{shared.Capability("NOPE")}); err == nil {
		t.Fatal("unknown capability must fail")
	}
	if err := s.RegisterProvider("BankA", []shared.Capability{shared.CapBalance, shared.CapPayments}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterProvider("BankA", []shared.Capability{shared.CapBalance}); err == nil {
		t.Fatal("duplicate provider must fail")
	}
	if got, err := s.CheckCapability("BankA", shared.CapBalance, testNow); err != nil || got != shared.AvailSupported {
		t.Fatalf("declared: %s %v", got, err)
	}
	if _, err := s.CheckCapability("Ghost", shared.CapBalance, testNow); err == nil {
		t.Fatal("unknown provider must fail")
	}
	if _, err := s.CheckCapability("BankA", shared.Capability("NOPE"), testNow); err == nil {
		t.Fatal("unknown capability must fail")
	}
	// Undeclared capability is unsupported.
	if got, _ := s.CheckCapability("BankA", shared.CapIdentity, testNow); got != shared.AvailUnsupported {
		t.Fatalf("undeclared: %s", got)
	}
	if err := s.DeclareOutage("BankA", shared.CapPayments, testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CheckCapability("BankA", shared.CapPayments, testNow); got != shared.AvailTemporarilyUnavailable {
		t.Fatalf("outage: %s", got)
	}
	if err := s.RecordAuthFailure("BankA", shared.CapBalance); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CheckCapability("BankA", shared.CapBalance, testNow); got != shared.AvailRequiresReauth {
		t.Fatalf("reauth: %s", got)
	}
	if err := s.RecordSuccess("BankA", shared.CapBalance); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CheckCapability("BankA", shared.CapBalance, testNow); got != shared.AvailSupported {
		t.Fatalf("success clears: %s", got)
	}
	if err := s.MarkUnsupported("BankA", shared.CapPayments); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CheckCapability("BankA", shared.CapPayments, testNow); got != shared.AvailUnsupported {
		t.Fatalf("observed unsupported wins: %s", got)
	}
	matrix := s.CapabilityMatrix(testNow)
	if !strings.Contains(matrix, "BankA:") || !strings.Contains(matrix, "UNSUPPORTED") {
		t.Fatalf("matrix must render: %q", matrix)
	}
}
