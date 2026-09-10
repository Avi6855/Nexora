package openbanking

import (
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func TestClassificationLanes(t *testing.T) {
	// Rate limit → backoff lane.
	p := ClassifyFailure(FailRateLimited, 0)
	if p.Kind != LaneBackoff || !p.AutoRetry {
		t.Fatalf("rate limit must back off: %+v", p)
	}
	// Provider down → backoff with more patience.
	p = ClassifyFailure(FailProviderDown, 0)
	if p.Kind != LaneBackoff || p.MaxAttempts < 5 {
		t.Fatalf("provider outage needs a patient retry budget: %+v", p)
	}
	// Auth expired → silent refresh: customer never prompted.
	p = ClassifyFailure(FailAuthExpired, 0)
	if p.Kind != LaneSilentRefresh || p.CustomerMsg != "" {
		t.Fatalf("silent refresh must be silent: %+v", p)
	}
	// Consent expired → customer must act.
	p = ClassifyFailure(FailConsentExpired, 0)
	if p.Kind != LaneCustomerReauth || p.CustomerMsg == "" {
		t.Fatalf("consent expiry needs customer action: %+v", p)
	}
	// Schema change → engineering, not customer.
	p = ClassifyFailure(FailSchemaChanged, 0)
	if p.Kind != LaneEngineering {
		t.Fatalf("schema drift is an engineering problem: %+v", p)
	}
}

func TestRecoveryBackoffAndExhaustion(t *testing.T) {
	p := ClassifyFailure(FailRateLimited, 30*time.Second)
	tr := NewRecoveryTracker(p)
	ok, wait := tr.ShouldRetry(tnow)
	if !ok || wait != 30*time.Second {
		t.Fatalf("first retry: %v %v", ok, wait)
	}
	tr.RecordAttempt(tnow)
	// Too early → wait the remainder.
	ok, wait = tr.ShouldRetry(tnow.Add(10 * time.Second))
	if ok {
		t.Fatal("must respect backoff window")
	}
	if wait != 20*time.Second {
		t.Fatalf("remaining wait %v", wait)
	}
	// Attempt budget exhausts.
	for i := 0; i < p.MaxAttempts; i++ {
		tr.RecordAttempt(tnow)
	}
	if ok, _ := tr.ShouldRetry(tnow); ok {
		t.Fatal("exhausted budget must stop retrying")
	}
	if !tr.Exhausted() {
		t.Fatal("Exhausted must report")
	}
}

func TestFreshnessVerdicts(t *testing.T) {
	f := NewFreshnessChecker()
	f.SetSLA("balance", 5*time.Minute)
	f.SetSLA("transactions", 20*time.Minute)

	r := f.Evaluate("balance", tnow.Add(-2*time.Minute), tnow)
	if r.Verdict != FreshFresh {
		t.Fatalf("2min-old balance must be fresh: %+v", r)
	}
	r = f.Evaluate("transactions", tnow.Add(-25*time.Minute), tnow)
	if r.Verdict != FreshStale {
		t.Fatalf("25min-old transactions must be stale: %+v", r)
	}
	// Undeclared SLA → unknown.
	if r := f.Evaluate("payments", tnow, tnow); r.Verdict != FreshUnknown {
		t.Fatalf("undeclared contract is unknown, not fresh: %+v", r)
	}
	// Never-refreshed → unknown, not stale.
	if r := f.Evaluate("balance", time.Time{}, tnow); r.Verdict != FreshUnknown {
		t.Fatalf("no data must be unknown: %+v", r)
	}
	// Overall = worst.
	overall := OverallVerdict([]FreshnessReport{
		f.Evaluate("balance", tnow, tnow),
		f.Evaluate("transactions", tnow.Add(-25*time.Minute), tnow),
	})
	if overall != FreshStale {
		t.Fatalf("worst verdict wins: %s", overall)
	}
	// Unknown poisons everything.
	overall = OverallVerdict([]FreshnessReport{
		f.Evaluate("balance", tnow, tnow),
		{Dataset: "x", Verdict: FreshUnknown},
	})
	if overall != FreshUnknown {
		t.Fatalf("unknown must poison the surface: %s", overall)
	}
}

func TestCapabilityMatrix(t *testing.T) {
	p := NewProviderCapabilities("BankA", []Capability{CapBalance, CapTransactions, CapPayments})
	if got := p.Check(CapBalance, tnow); got != AvailSupported {
		t.Fatalf("declared capability: %s", got)
	}
	if got := p.Check(CapIdentity, tnow); got != AvailUnsupported {
		t.Fatalf("undeclared capability: %s", got)
	}
	// Outage window → temporarily unavailable, then recovers.
	p.DeclareOutage(CapPayments, tnow.Add(time.Hour))
	if got := p.Check(CapPayments, tnow); got != AvailTemporarilyUnavailable {
		t.Fatalf("outage: %s", got)
	}
	if got := p.Check(CapPayments, tnow.Add(2*time.Hour)); got != AvailSupported {
		t.Fatalf("post-outage recovery: %s", got)
	}
	// Auth failure → reauth required until a success clears it.
	p.RecordAuthFailure(CapBalance)
	if got := p.Check(CapBalance, tnow); got != AvailRequiresReauth {
		t.Fatalf("auth failure: %s", got)
	}
	p.RecordSuccess(CapBalance)
	if got := p.Check(CapBalance, tnow); got != AvailSupported {
		t.Fatalf("success clears auth flag: %s", got)
	}
	// Observed reality overrides the spec.
	p.ObserveUnsupported(CapPayments)
	if got := p.Check(CapPayments, tnow); got != AvailUnsupported {
		t.Fatalf("observed unsupported must win over declared: %s", got)
	}
}

func TestMatrixSummary(t *testing.T) {
	a := NewProviderCapabilities("BankA", []Capability{CapBalance, CapTransactions})
	b := NewProviderCapabilities("BankB", []Capability{CapBalance, CapTransactions, CapPayments})
	b.DeclareOutage(CapPayments, tnow.Add(time.Hour))
	sum := MatrixSummary([]*ProviderCapabilities{b, a}, tnow)
	if len(sum) == 0 || sum[0] != 'B' {
		t.Fatalf("summary should render providers in order: %q", sum)
	}
	if !contains(sum, "PAYMENTS=TEMPORARILY_UNAVAILABLE") {
		t.Fatalf("outage must appear: %q", sum)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
