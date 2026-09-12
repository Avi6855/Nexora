package decisionops

import (
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/decisionops"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func TestExplainWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	exp, err := s.Explain(shared.Decision{
		Signals: []shared.Signal{
			{Name: "unusual_location", Value: 0.9, Weight: 1.5},
			{Name: "velocity_spike", Value: 0.6, Weight: 1.0},
		},
		PolicyID: "fraud-v3", PolicyVersion: "v12", Outcome: "BLOCK",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exp.Summary, "unusual_location") || !strings.Contains(exp.Summary, "BLOCK") {
		t.Fatalf("summary: %q", exp.Summary)
	}
	if _, err := s.Explain(shared.Decision{}); err == nil {
		t.Fatal("empty decision must fail")
	}
}

func TestCaseLifecycleWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	c, err := s.Enqueue("txn-1", 85, time.Hour, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if c.Priority != "CRITICAL" {
		t.Fatalf("priority: %+v", c)
	}
	if _, err := s.Enqueue("", 10, time.Hour, testNow); err == nil {
		t.Fatal("empty subject must fail")
	}
	if err := s.Lease(c.ID, "amy", 10*time.Minute, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.Lease(c.ID, "bo", 10*time.Minute, testNow); err != shared.ErrLeaseConflict {
		t.Fatalf("conflict: %v", err)
	}
	if err := s.Decide(c.ID, "amy", shared.DecideReject, "mule", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(c.ID, "amy", shared.DecideApprove, "", testNow); err != shared.ErrCaseDecided {
		t.Fatalf("double decide: %v", err)
	}
	trail, err := s.Audit(c.ID)
	if err != nil || len(trail) < 3 {
		t.Fatalf("audit: %+v %v", trail, err)
	}
}

func TestEscalateReassignSweepWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	c, _ := s.Enqueue("txn-2", 50, 5*time.Minute, testNow)
	if err := s.Escalate(c.ID, "amy", "tier2", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.Reassign(c.ID, "lead", "bo", testNow); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if got.Assignee != "bo" {
		t.Fatalf("reassigned: %+v", got)
	}
	leased, _ := s.Enqueue("txn-3", 70, time.Hour, testNow)
	_ = s.Lease(leased.ID, "amy", time.Minute, testNow)
	breaching, _ := s.Enqueue("txn-4", 90, 5*time.Minute, testNow)
	swept := s.SweepTimeouts(testNow.Add(10 * time.Minute))
	if len(swept) != 2 {
		t.Fatalf("lease expiry + SLA breach: %v", swept)
	}
	for _, id := range []string{leased.ID, breaching.ID} {
		found := false
		for _, s := range swept {
			if s == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("swept must include %s: %v", id, swept)
		}
	}
	gotLeased, _ := s.Get(leased.ID)
	if gotLeased.LeaseHolder != "" {
		t.Fatalf("lease must be released: %+v", gotLeased)
	}
	gotBreach, _ := s.Get(breaching.ID)
	if gotBreach.Status != shared.StatusEscalated {
		t.Fatalf("SLA breach auto-escalates: %+v", gotBreach)
	}
	// The already-escalated case is left alone by the sweep.
	got, _ = s.Get(c.ID)
	if got.Status != shared.StatusEscalated {
		t.Fatalf("escalated case untouched: %+v", got)
	}
}

func TestModelRecordsWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	rec, err := s.RecordModel("m-7", "snap-1", "fraud-v3/v12", "BLOCK", 0.9, false, "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.VerifyModel(rec.ID, "snap-1")
	if err != nil || v != shared.VerdictReproduced {
		t.Fatalf("reproduced: %s %v", v, err)
	}
	v, _ = s.VerifyModel(rec.ID, "other")
	if v != shared.VerdictTampered {
		t.Fatalf("tampered: %s", v)
	}
	if _, err := s.RecordModel("", "s", "p", "d", 0.5, false, "", testNow); err == nil {
		t.Fatal("empty model version must fail")
	}
	if _, err := s.GetModel("missing"); err == nil {
		t.Fatal("unknown record must fail")
	}
}
