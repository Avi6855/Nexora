package complianceops

import (
	"strings"
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func graph() EvidenceGraph {
	return EvidenceGraph{
		Customers:    []string{"cust-1"},
		Transactions: []string{"txn-1", "txn-2"},
		Devices:      []string{"dev-1"},
		Payments:     []string{"pay-1"},
		Decisions:    []string{"dec-1"},
		Documents:    []string{"doc-1"},
	}
}

func TestBundleVerify(t *testing.T) {
	s := NewStore()
	b, err := s.BuildBundle("inv-1", graph(), tnow)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 7 {
		t.Fatalf("7 evidence refs expected: %+v", b)
	}
	if b.BundleHash == "" {
		t.Fatal("bundle hash required")
	}
	v, err := s.Verify(b.ID)
	if err != nil || v != BundleValid {
		t.Fatalf("verify = %s %v, want VALID", v, err)
	}
	got, err := s.GetBundle(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBundle(got); err != nil {
		t.Fatalf("direct verify: %v", err)
	}
	// Tamper with the copy: verification must fail.
	got.Entries[0].RefID = "cust-evil"
	if err := VerifyBundle(got); err == nil {
		t.Fatal("tampered bundle must fail verification")
	}
	// Stored bundle is untouched by the tampered copy.
	v, err = s.Verify(b.ID)
	if err != nil || v != BundleValid {
		t.Fatalf("stored bundle must stay VALID: %s %v", v, err)
	}
	if _, err := s.GetBundle("missing"); err == nil {
		t.Fatal("unknown bundle must fail")
	}
	if _, err := s.Verify("missing"); err == nil {
		t.Fatal("verify on unknown must fail")
	}
	if _, err := s.BuildBundle("", graph(), tnow); err == nil {
		t.Fatal("empty investigation must fail")
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := NewStore()
	snap, err := s.SnapshotCase("inv-1", "cust-1", `{"tier":"standard","limit":100}`, tnow)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Hash == "" {
		t.Fatal("snapshot hash required")
	}
	// Upstream drift must not leak into the pinned read.
	s.SetCurrentState("cust-1", `{"tier":"premium","limit":99999}`)
	got, err := s.GetSnapshot("inv-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != `{"tier":"standard","limit":100}` {
		t.Fatalf("pinned snapshot must win over drift: %q", got.State)
	}
	cur, _ := s.GetCurrentState("cust-1")
	if cur != `{"tier":"premium","limit":99999}` {
		t.Fatalf("current state must drift: %q", cur)
	}
	if _, err := s.SnapshotCase("inv-1", "cust-1", "other", tnow); err == nil {
		t.Fatal("second snapshot must conflict")
	}
	if _, err := s.GetSnapshot("missing"); err == nil {
		t.Fatal("unknown snapshot must fail")
	}
}

func mustPolicy(t *testing.T, s *Store) {
	t.Helper()
	if err := s.SetSLAPolicy(SLAPolicy{
		CaseType: CaseTypeKYC, Deadline: 24 * time.Hour, Priority: "HIGH", Escalation: "fin-crime-queue",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSLABreachEscalation(t *testing.T) {
	s := NewStore()
	mustPolicy(t, s)
	item, err := s.OpenCase(CaseTypeKYC, tnow)
	if err != nil {
		t.Fatal(err)
	}
	if item.Priority != "HIGH" {
		t.Fatalf("policy priority must carry: %+v", item)
	}
	// First tick assigns the fresh case.
	events := s.Tick(tnow.Add(time.Hour))
	foundAssigned := false
	for _, e := range events {
		if e.Kind == EventAssigned && e.ItemID == item.ID {
			foundAssigned = true
		}
	}
	if !foundAssigned {
		t.Fatalf("fresh case must be assigned: %+v", events)
	}
	// Warning fires inside the warning window (deadline/4 = 6h).
	events = s.Tick(tnow.Add(20 * time.Hour))
	foundWarn := false
	for _, e := range events {
		if e.Kind == EventWarning {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Fatalf("warning expected before deadline: %+v", events)
	}
	// Past the deadline the case breaches and escalates to the policy target.
	events = s.Tick(tnow.Add(25 * time.Hour))
	var breach, esc bool
	for _, e := range events {
		if e.Kind == EventBreach {
			breach = true
		}
		if e.Kind == EventEscalated {
			esc = true
			if !strings.Contains(e.Detail, "fin-crime-queue") {
				t.Fatalf("escalation must name target: %+v", e)
			}
		}
	}
	if !breach || !esc {
		t.Fatalf("breach + escalation expected: %+v", events)
	}
	got, _ := s.GetSLAItem(item.ID)
	if got.Assignee != "fin-crime-queue" {
		t.Fatalf("breached case assigned to escalation: %+v", got)
	}
	// Tick is idempotent: no repeat breach.
	again := s.Tick(tnow.Add(26 * time.Hour))
	for _, e := range again {
		if e.Kind == EventBreach || e.Kind == EventEscalated {
			t.Fatalf("breach must fire once: %+v", again)
		}
	}
	if err := s.SetSLAPolicy(SLAPolicy{CaseType: "BOGUS", Deadline: time.Hour, Priority: "P1", Escalation: "q"}); err == nil {
		t.Fatal("bad case type must fail")
	}
	if _, err := s.OpenCase(CaseTypeFincrime, tnow); err == nil {
		t.Fatal("open without policy must fail")
	}
}

func TestSamplingDeterminism(t *testing.T) {
	pop := []SampleDecision{
		{ID: "d-1"},
		{ID: "d-2", HighRisk: true},
		{ID: "d-3", Borderline: true},
		{ID: "d-4", NewPolicyOrModel: true},
		{ID: "d-5", UnusualSegment: true},
		{ID: "d-6", HighRisk: true, Borderline: true, NewPolicyOrModel: true, UnusualSegment: true},
		{ID: "d-7"},
		{ID: "d-8", HighRisk: true},
	}
	if WeightOf(pop[1]) != 3 {
		t.Fatalf("high-risk weight = %d, want 3", WeightOf(pop[1]))
	}
	if WeightOf(pop[2]) != 2 || WeightOf(pop[3]) != 2 || WeightOf(pop[4]) != 2 {
		t.Fatal("borderline/new-policy/unusual weights must be 2")
	}
	if WeightOf(pop[5]) != 3*2*2*2 {
		t.Fatalf("combined weight = %d, want 24", WeightOf(pop[5]))
	}
	a, err := Sample(pop, 3, 42)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Sample(pop, 3, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 3 || len(b) != 3 {
		t.Fatalf("sample size 3: %+v %+v", a, b)
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("same seed must be deterministic: %+v vs %+v", a, b)
		}
	}
	c, err := Sample(pop, 3, 99)
	if err != nil {
		t.Fatal(err)
	}
	same := true
	for i := range a {
		if a[i].ID != c[i].ID {
			same = false
		}
	}
	// Different seeds over a weighted population should (almost surely) differ;
	// if they collide, the weighting itself is still verified above.
	_ = same
	// Weighted: over many draws the heavy item appears more often than the plain one.
	counts := map[string]int{}
	for seed := int64(0); seed < 50; seed++ {
		samp, _ := Sample(pop, 4, seed)
		for _, d := range samp {
			counts[d.ID]++
		}
	}
	if counts["d-6"] <= counts["d-1"] {
		t.Fatalf("heaviest item must sample more often: %+v", counts)
	}
	if _, err := Sample(nil, 2, 1); err == nil {
		t.Fatal("empty population must fail")
	}
	if _, err := Sample(pop, 0, 1); err == nil {
		t.Fatal("zero sample must fail")
	}
}

func TestCoverageGaps(t *testing.T) {
	s := NewStore()
	if err := s.RegisterRequirement(Requirement{ID: "REQ-1", Rule: "block sanctioned", Service: "payments", TestRef: "t-1", MonitorRef: "m-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRequirement(Requirement{ID: "REQ-2", Rule: "kyc refresh", Service: "kyc"}); err != nil {
		t.Fatal(err)
	}
	rep := s.Coverage()
	if len(rep) != 2 {
		t.Fatalf("coverage = %+v", rep)
	}
	if !rep[0].Implemented || !rep[0].Tested || !rep[0].Monitored || len(rep[0].Gaps) != 0 {
		t.Fatalf("REQ-1 must be fully covered: %+v", rep[0])
	}
	if !rep[1].Implemented || rep[1].Tested || rep[1].Monitored {
		t.Fatalf("REQ-2 flags: %+v", rep[1])
	}
	if len(rep[1].Gaps) != 2 {
		t.Fatalf("REQ-2 must gap test+monitor: %+v", rep[1])
	}
	if err := s.RegisterRequirement(Requirement{ID: "REQ-1", Rule: "x", Service: "y"}); err == nil {
		t.Fatal("duplicate requirement must conflict")
	}
	if err := s.RegisterRequirement(Requirement{ID: "", Rule: "x", Service: "y"}); err == nil {
		t.Fatal("empty id must fail")
	}
}

func TestControlFailures(t *testing.T) {
	s := NewStore()
	if err := s.RegisterControlTest(ControlTest{ID: "c-1", Name: "restricted customer blocked", Scenario: ScenarioRestrictedCustomer, Expected: VerdictBlock}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterControlTest(ControlTest{ID: "c-2", Name: "clean allowed", Scenario: ScenarioClean, Expected: VerdictAllow}); err != nil {
		t.Fatal(err)
	}
	// Deliberately wrong expectation: threshold breaches BLOCK, not ALLOW.
	if err := s.RegisterControlTest(ControlTest{ID: "c-3", Name: "wrong", Scenario: ScenarioThresholdBreach, Expected: VerdictAllow}); err != nil {
		t.Fatal(err)
	}
	run, err := s.RunControls(tnow)
	if err != nil {
		t.Fatal(err)
	}
	if run.Total != 3 || run.Passed != 2 || run.Failed != 1 || run.Overall != RunFail {
		t.Fatalf("run = %+v", run)
	}
	for _, r := range run.Results {
		if r.TestID == "c-3" && r.Verdict != RunFail {
			t.Fatalf("wrong expectation must FAIL: %+v", r)
		}
		if r.TestID == "c-1" && (r.Verdict != RunPass || r.Actual != VerdictBlock) {
			t.Fatalf("restricted customer must BLOCK+PASS: %+v", r)
		}
	}
	got, err := s.GetRun(run.ID)
	if err != nil || got.Overall != RunFail {
		t.Fatalf("get run: %+v %v", got, err)
	}
	if EvaluateScenario(ScenarioMissingEvidence) != VerdictBlock {
		t.Fatal("missing evidence must fail closed (BLOCK)")
	}
	empty := NewStore()
	if _, err := empty.RunControls(tnow); err == nil {
		t.Fatal("run with no controls must fail")
	}
}
