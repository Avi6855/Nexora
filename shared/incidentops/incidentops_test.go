package incidentops

import (
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func seedSpike(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.Ingest("ledger", KindDeploy, "deploy v42", false, tnow.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.Ingest("ledger", KindMetric, "5xx error spike", true, tnow.Add(time.Duration(-10+i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIngestValidation(t *testing.T) {
	s := NewStore()
	if _, err := s.Ingest("", KindMetric, "m", false, tnow); err == nil {
		t.Fatal("empty service must fail")
	}
	if _, err := s.Ingest("a", "nope", "m", false, tnow); err == nil {
		t.Fatal("unknown kind must fail")
	}
	if _, err := s.Ingest("a", KindLog, "m", false, time.Time{}); err == nil {
		t.Fatal("zero timestamp must fail")
	}
	for _, k := range []string{KindMetric, KindLog, KindTrace, KindDeploy, KindChange} {
		if _, err := s.Ingest("svc", k, "ok", false, tnow); err != nil {
			t.Fatalf("kind %s: %v", k, err)
		}
	}
}

func TestCorrelateAndTriage(t *testing.T) {
	s := NewStore()
	seedSpike(t, s)
	// Noise: a singleton service must not become a candidate.
	if _, err := s.Ingest("lonely", KindLog, "one-off", false, tnow); err != nil {
		t.Fatal(err)
	}
	cands := s.Correlate(30*time.Minute, tnow)
	if len(cands) != 1 {
		t.Fatalf("one candidate expected: %+v", cands)
	}
	c := cands[0]
	if c.Service != "ledger" || c.Count != 5 {
		t.Fatalf("candidate: %+v", c)
	}
	if !c.RecentDeploy || !c.ErrorSpike {
		t.Fatalf("deploy + spike flags: %+v", c)
	}
	res, err := s.Triage(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Severity != "SEV2" {
		t.Fatalf("4 errors → SEV2: %+v", res)
	}
	if len(res.LikelyCauses) < 2 {
		t.Fatalf("deploy + spike causes: %+v", res)
	}
	// Deploy recency outranks the bare spike.
	if res.LikelyCauses[0].Score < res.LikelyCauses[1].Score {
		t.Fatalf("deploy must rank first: %+v", res.LikelyCauses)
	}
	if _, err := s.Triage("missing"); err != ErrUnknownCandidate {
		t.Fatalf("unknown candidate: %v", err)
	}
}

func TestTriageSeverityScale(t *testing.T) {
	s := NewStore()
	for i := 0; i < 6; i++ {
		_, _ = s.Ingest("pay", KindLog, "fail", true, tnow.Add(time.Duration(i)*time.Minute))
	}
	cands := s.Correlate(time.Hour, tnow.Add(10*time.Minute))
	if len(cands) != 1 {
		t.Fatalf("cands: %+v", cands)
	}
	res, err := s.Triage(cands[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Severity != "SEV1" {
		t.Fatalf("6 errors → SEV1: %+v", res)
	}
}

func TestImpactCalculate(t *testing.T) {
	s := NewStore()
	snap := []ServiceLedger{
		{Service: "ledger", Customers: 1000, Transactions: 5000, AmountMinor: 200000},
		{Service: "payments", Customers: 500, Transactions: 1500, AmountMinor: 75000},
	}
	imp := s.Calculate([]string{"ledger", "payments", "ledger"}, snap)
	if imp.Customers != 1500 || imp.Transactions != 6500 || imp.AmountAtRiskMinor != 275000 {
		t.Fatalf("impact sums (deduped): %+v", imp)
	}
	if len(imp.Services) != 2 || imp.Services[0] != "ledger" {
		t.Fatalf("sorted services: %+v", imp.Services)
	}
	empty := s.Calculate([]string{"ghost"}, snap)
	if empty.Customers != 0 || len(empty.Services) != 0 {
		t.Fatalf("unknown services contribute nothing: %+v", empty)
	}
}

func TestCompensationPolicies(t *testing.T) {
	s := NewStore()
	if err := s.AddPolicy("p1", 30, "all", 500); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPolicy("p1", 30, "all", 500); err != ErrDuplicatePolicy {
		t.Fatalf("duplicate policy: %v", err)
	}
	if err := s.AddPolicy("", 30, "all", 500); err == nil {
		t.Fatal("empty id must fail")
	}
	ok, amount, pid := s.Evaluate("a1", 10, "standard")
	if ok {
		t.Fatalf("below threshold must not match: %v %v %v", ok, amount, pid)
	}
	ok, amount, pid = s.Evaluate("a1", 60, "standard")
	if !ok || amount != 500 || pid != "p1" {
		t.Fatalf("eligible: %v %v %v", ok, amount, pid)
	}
	// Tier-restricted policy does not match other tiers.
	if err := s.AddPolicy("p-vip", 30, "vip", 2000); err != nil {
		t.Fatal(err)
	}
	ok, amount, _ = s.Evaluate("a2", 60, "standard")
	if !ok || amount != 500 {
		t.Fatalf("standard must get p1 only: %v %v", ok, amount)
	}
	ok, amount, pid = s.Evaluate("a3", 60, "vip")
	if !ok || amount != 2000 || pid != "p-vip" {
		t.Fatalf("vip gets best policy: %v %v %v", ok, amount, pid)
	}
}

func TestAwardDedupFraudOverride(t *testing.T) {
	s := NewStore()
	if err := s.AddPolicy("p1", 15, "all", 500); err != nil {
		t.Fatal(err)
	}
	a1, err := s.Award("claim-1", "acct-9", 30, "standard", tnow)
	if err != nil || a1.Status != "AWARDED" || a1.FraudFlag {
		t.Fatalf("first award clean: %+v %v", a1, err)
	}
	if _, err := s.Award("claim-1", "acct-9", 30, "standard", tnow); err != ErrDuplicateClaim {
		t.Fatalf("duplicate claim key: %v", err)
	}
	if _, err := s.Award("claim-2", "acct-9", 30, "standard", tnow); err != nil {
		t.Fatal(err)
	}
	// Third claim from the same account is fraud-flagged.
	a3, err := s.Award("claim-3", "acct-9", 30, "standard", tnow)
	if err != nil || !a3.FraudFlag || a3.Status != "FLAGGED" {
		t.Fatalf("repeated claims flag: %+v %v", a3, err)
	}
	// Ineligible outage awards nothing.
	if _, err := s.Award("claim-4", "acct-1", 1, "standard", tnow); err == nil {
		t.Fatal("ineligible must fail")
	}
	// Manual override corrects the award.
	newAmt := int64(1200)
	over, err := s.Override(a1.ID, "ops-lead", &newAmt, "", tnow)
	if err != nil || over.AmountMinor != 1200 || over.Status != "OVERRIDDEN" {
		t.Fatalf("override: %+v %v", over, err)
	}
	if _, err := s.Override("missing", "ops-lead", nil, "AWARDED", tnow); err != ErrUnknownAward {
		t.Fatalf("unknown award: %v", err)
	}
	if _, err := s.Override(a1.ID, "", nil, "", tnow); err == nil {
		t.Fatal("approver required")
	}
}
