package lifeevents

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func TestBereavementProtectionFirst(t *testing.T) {
	c := NewBereavementCase("bv1", "cust1", now)
	if !c.OutgoingBlocked {
		t.Fatal("reporting a death must block outgoing payments immediately")
	}
	if c.Stage != BVReported {
		t.Fatalf("stage %s", c.Stage)
	}
}

func TestBereavementHappyPath(t *testing.T) {
	c := NewBereavementCase("bv2", "cust1", now)
	c.Obligations = []string{"dd1", "card1"}
	c.DiscoverAsset("acc_main")

	// Advance without evidence must fail at the executor gate.
	if err := c.Advance(now, "agent", "id ok"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "agent", "protected"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "agent", "skip evidence"); err == nil {
		t.Fatal("executor verification without verified evidence must be gated")
	}
	c.VerifyExecutorDoc("probate-123")
	if err := c.Advance(now, "agent", "grant verified"); err != nil {
		t.Fatal(err)
	}
	if c.Stage != BVExecutorVerified {
		t.Fatalf("stage %s", c.Stage)
	}
	if err := c.Advance(now, "agent", "discovery"); err != nil {
		t.Fatal(err)
	}
	if c.Stage != BVAssetDiscovery {
		t.Fatalf("stage %s", c.Stage)
	}
	// Obligations gate the step into obligations-settled.
	if err := c.Advance(now, "agent", "try settle"); err == nil {
		t.Fatal("unsettled obligations must gate the workflow")
	}
	c.SettleObligation("dd1")
	if err := c.Advance(now, "agent", "still one left"); err == nil {
		t.Fatal("ALL obligations must clear")
	}
	c.SettleObligation("card1")
	if err := c.Advance(now, "agent", "settled"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "agent", "statement issued"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "agent", "distribute"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "agent", "close"); err != nil {
		t.Fatal(err)
	}
	if c.Stage != BVClosed {
		t.Fatalf("stage %s", c.Stage)
	}
	if len(c.Audit) == 0 {
		t.Fatal("audit trail required")
	}
}

func TestBereavementDispute(t *testing.T) {
	c := NewBereavementCase("bv3", "cust1", now)
	c.VerifyExecutorDoc("doc1")
	if err := c.Advance(now, "a", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "a", "r2"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advance(now, "a", "r3"); err != nil {
		t.Fatal(err)
	}
	at := c.Stage
	if err := c.Dispute(now, "legal", "will contested"); err != nil {
		t.Fatal(err)
	}
	if c.Stage != BVDisputed {
		t.Fatalf("stage %s", c.Stage)
	}
	// Advancing while disputed is invalid.
	if err := c.Advance(now, "a", "nope"); err == nil {
		t.Fatal("disputed case must not advance")
	}
	if err := c.ResolveDispute(now, "legal"); err != nil {
		t.Fatal(err)
	}
	if c.Stage != at {
		t.Fatalf("must return to %s, got %s", at, c.Stage)
	}
}

func TestBereavementClosureDisputeBlocked(t *testing.T) {
	c := NewBereavementCase("bv4", "cust1", now)
	c.VerifyExecutorDoc("d")
	c.DiscoverAsset("a")
	for i := 0; i < 8; i++ {
		if err := c.Advance(now, "a", "flow"); err != nil {
			t.Fatal(err)
		}
	}
	if c.Stage != BVClosed {
		t.Fatalf("stage %s", c.Stage)
	}
	if err := c.Dispute(now, "x", "too late"); err == nil {
		t.Fatal("closed case cannot be disputed")
	}
}

func TestWorkspaceTaskDAG(t *testing.T) {
	w, _ := NewWorkspace("w1", EventMovingHouse, now)
	if err := w.AddTask(Task{ID: "notify", Title: "Notify landlord"}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddTask(Task{ID: "deposit", Title: "Pay deposit", DependsOn: []string{"notify"}}); err != nil {
		t.Fatal(err)
	}
	// Out-of-order completion refused.
	if err := w.CompleteTask("deposit", now); err == nil {
		t.Fatal("dependency must gate completion")
	}
	if err := w.CompleteTask("notify", now); err != nil {
		t.Fatal(err)
	}
	if err := w.CompleteTask("deposit", now); err != nil {
		t.Fatal(err)
	}
	if w.Progress() != 100 {
		t.Fatalf("progress %d", w.Progress())
	}
	// Self-dependency and duplicates rejected.
	if err := w.AddTask(Task{ID: "loop", DependsOn: []string{"loop"}}); err == nil {
		t.Fatal("self-dependency rejected")
	}
	if err := w.AddTask(Task{ID: "notify"}); err == nil {
		t.Fatal("duplicate task rejected")
	}
}

func TestWorkspaceBeneficiaries(t *testing.T) {
	w, _ := NewWorkspace("w2", EventMarriage, now)
	if err := w.SetBeneficiaries([]Beneficiary{{Name: "a", ShareBps: 6000}, {Name: "b", ShareBps: 3000}}); err == nil {
		t.Fatal("shares summing to 90% must be rejected")
	}
	if err := w.SetBeneficiaries([]Beneficiary{{Name: "a", ShareBps: 10000}}); err != nil {
		t.Fatal(err)
	}
	if len(w.Beneficiaries) != 1 {
		t.Fatal("beneficiaries not set")
	}
	if err := w.SetBeneficiaries([]Beneficiary{{Name: "a", ShareBps: 0}}); err == nil {
		t.Fatal("non-positive share rejected")
	}
}

func TestWorkspaceOverdueAndArchive(t *testing.T) {
	w, _ := NewWorkspace("w3", EventDivorce, now)
	if err := w.AddTask(Task{ID: "t1", Due: now.Add(-24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddTask(Task{ID: "t2", Due: now.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	od := w.Overdue(now)
	if len(od) != 1 || od[0].ID != "t1" {
		t.Fatalf("overdue: %+v", od)
	}
	if w.ShouldArchive(now) {
		t.Fatal("incomplete workspace must not archive")
	}
	if err := w.CompleteTask("t1", now); err != nil {
		t.Fatal(err)
	}
	if err := w.CompleteTask("t2", now); err != nil {
		t.Fatal(err)
	}
	if w.ShouldArchive(now.Add(24 * time.Hour)) {
		t.Fatal("90-day retention must hold")
	}
	if !w.ShouldArchive(now.Add(91 * 24 * time.Hour)) {
		t.Fatal("past retention the workspace must archive")
	}
}
