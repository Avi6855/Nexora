package distcoord

import (
	"testing"
	"time"
)

func TestCorrelationIssuePropagateValidate(t *testing.T) {
	s := NewCorrelationStore()
	root, err := s.Issue("payment.authorize")
	if err != nil {
		t.Fatal(err)
	}
	if root.CorrelationID == "" || root.ID == "" || root.CausationID != "" {
		t.Fatalf("bad root: %+v", root)
	}
	child, err := s.Propagate(root, "fraud.check")
	if err != nil {
		t.Fatal(err)
	}
	if child.CorrelationID != root.CorrelationID {
		t.Fatalf("child must inherit correlation id")
	}
	if child.CausationID != root.ID {
		t.Fatalf("child causation must be parent id, got %q", child.CausationID)
	}
	if err := ValidateChain([]Correlation{root, child}); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}
}

func TestCorrelationValidateIncomplete(t *testing.T) {
	s := NewCorrelationStore()
	root, _ := s.Issue("a")
	child, _ := s.Propagate(root, "b")
	orphan := Correlation{ID: "x", CorrelationID: root.CorrelationID, CausationID: "missing", Action: "c"}
	if err := ValidateChain([]Correlation{root, child, orphan}); err == nil {
		t.Fatalf("expected dangling causation error")
	}
	if err := ValidateChain(nil); err == nil {
		t.Fatalf("expected empty chain error")
	}
	if _, err := s.Issue(""); err == nil {
		t.Fatalf("expected action required")
	}
}

func TestCausalityWhyHappened(t *testing.T) {
	tr := NewCausalityTracker()
	for _, e := range [][2]string{{"A", "B"}, {"B", "C"}} {
		if err := tr.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	chain, err := tr.WhyHappened("C")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain[0] != "A" || chain[1] != "B" {
		t.Fatalf("expected [A B], got %v", chain)
	}
	if _, err := tr.WhyHappened("nope"); err == nil {
		t.Fatalf("expected unknown node error")
	}
}

func TestCausalityCycleSafe(t *testing.T) {
	tr := NewCausalityTracker()
	for _, e := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}} {
		if err := tr.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	chain, err := tr.WhyHappened("C")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 causes cycle-safe, got %v", chain)
	}
}

func TestSkewMonitorPolicy(t *testing.T) {
	m := NewSkewMonitor(100)
	if err := m.RecordSample("n1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordSample("n2", 1050); err != nil {
		t.Fatal(err)
	}
	if got := m.MaxSkew(); got != 50 {
		t.Fatalf("expected skew 50, got %d", got)
	}
	if p := m.Policy(); p.DisableCritical || p.Action != "ALLOW" {
		t.Fatalf("expected ALLOW, got %+v", p)
	}
	if err := m.RecordSample("n3", 2000); err != nil {
		t.Fatal(err)
	}
	p := m.Policy()
	if !p.DisableCritical || p.Action != "DISABLE_CRITICAL" {
		t.Fatalf("expected DISABLE_CRITICAL, got %+v", p)
	}
	matrix := m.SkewMatrix()
	if matrix["n1"]["n3"] != 1000 {
		t.Fatalf("bad skew matrix: %v", matrix)
	}
}

func TestHLCMonotonicWithRegression(t *testing.T) {
	c := NewClock("n1")
	t1 := c.Issue(1000)
	t2 := c.Issue(900) // wall regressed: logical must advance, wall pinned
	if Compare(t2, t1) <= 0 {
		t.Fatalf("HLC must stay monotonic on regression: %+v vs %+v", t1, t2)
	}
	if t2.WallMs != t1.WallMs || t2.Logical != t1.Logical+1 {
		t.Fatalf("expected pinned wall + bumped logical, got %+v", t2)
	}
	remote := Timestamp{WallMs: 2000, Logical: 3, Node: "n2"}
	t3 := c.Receive(remote, 950)
	if t3.WallMs != 2000 || t3.Logical != 4 {
		t.Fatalf("expected merge to wall 2000 logical 4, got %+v", t3)
	}
	if Compare(Timestamp{WallMs: 1}, Timestamp{WallMs: 2}) != -1 {
		t.Fatalf("compare broken")
	}
}

func TestLockStuckAndStats(t *testing.T) {
	r := NewLockRegistry()
	now := time.Now().UTC()
	ok, err := r.Acquire("ledger", "svc-a", 100, now)
	if err != nil || !ok {
		t.Fatalf("acquire failed: %v %v", ok, err)
	}
	ok, err = r.Acquire("ledger", "svc-b", 100, now.Add(10*time.Millisecond))
	if err != nil || ok {
		t.Fatalf("second acquire must queue, got %v %v", ok, err)
	}
	stuck := r.StuckLocks(2, now.Add(500*time.Millisecond))
	if len(stuck) != 1 || stuck[0].Resource != "ledger" {
		t.Fatalf("expected stuck lock, got %v", stuck)
	}
	stats := r.Stats()
	if len(stats) != 1 || stats[0].Contentions != 1 || stats[0].Waiters != 1 {
		t.Fatalf("bad stats: %+v", stats)
	}
	if err := r.Release("ledger", "svc-b"); err == nil {
		t.Fatalf("non-owner release must fail")
	}
	if err := r.Release("ledger", "svc-a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Release("ledger", "svc-a"); err == nil {
		t.Fatalf("double release must fail")
	}
}

func TestContentionAdvisor(t *testing.T) {
	hot := []WaitEdge{}
	for i := 0; i < 6; i++ {
		hot = append(hot, WaitEdge{Waiter: string(rune('a' + i)), Holder: "h", Resource: "hot-key", HoldMs: 10})
	}
	adv := AdviseContention(hot)
	if len(adv) != 1 || adv[0].Recommendation != "shard" {
		t.Fatalf("expected shard, got %+v", adv)
	}
	long := []WaitEdge{{Waiter: "w1", Holder: "h", Resource: "ledger", HoldMs: 900}}
	if got := AdviseContention(long); got[0].Recommendation != "shorten" {
		t.Fatalf("expected shorten, got %+v", got)
	}
	wide := []WaitEdge{
		{Waiter: "w1", Holder: "big", Resource: "r1"},
		{Waiter: "w2", Holder: "big", Resource: "r2"},
		{Waiter: "w3", Holder: "big", Resource: "r3"},
	}
	got := AdviseContention(wide)
	found := false
	for _, a := range got {
		if a.Recommendation == "partition" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected partition for wide holder, got %+v", got)
	}
	mild := []WaitEdge{{Waiter: "w1", Holder: "h", Resource: "q", HoldMs: 5}}
	if got := AdviseContention(mild); got[0].Recommendation != "serialize-queue" {
		t.Fatalf("expected serialize-queue, got %+v", got)
	}
}
