package decisionops

import (
	"strings"
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func TestExplainTopSignals(t *testing.T) {
	d := Decision{
		Signals: []Signal{
			{Name: "unusual_location", Value: 0.92, Weight: 1.5},
			{Name: "velocity_spike", Value: 0.7, Weight: 1.2},
			{Name: "account_age", Value: 0.1, Weight: 0.2},
			{Name: "cross_border", Value: 0.5, Weight: -0.3},
		},
		PolicyID:      "fraud-v3",
		PolicyVersion: "v12",
		Outcome:       "BLOCK",
	}
	exp := Explain(d)
	if exp.Outcome != "BLOCK" || exp.PolicyID != "fraud-v3" || exp.PolicyVersion != "v12" {
		t.Fatalf("metadata must carry through: %+v", exp)
	}
	if len(exp.TopSignals) != 3 {
		t.Fatalf("top-3 signals expected: %+v", exp.TopSignals)
	}
	// |0.92*1.5|=1.38 first, |0.7*1.2|=0.84 second.
	if exp.TopSignals[0] != "unusual_location" || exp.TopSignals[1] != "velocity_spike" {
		t.Fatalf("ordering by contribution: %+v", exp.TopSignals)
	}
	if !strings.Contains(exp.Summary, "BLOCK") || !strings.Contains(exp.Summary, "fraud-v3") {
		t.Fatalf("summary must name outcome and policy: %q", exp.Summary)
	}
	if !strings.Contains(exp.Summary, "unusual_location") {
		t.Fatalf("summary must name top driver: %q", exp.Summary)
	}
	for _, line := range exp.Lines {
		if !strings.Contains(line, "risk") {
			t.Fatalf("each line must speak of risk in plain language: %q", line)
		}
	}
}

func TestExplainEmpty(t *testing.T) {
	exp := Explain(Decision{Outcome: "ALLOW", PolicyID: "fraud-v3"})
	if !strings.Contains(exp.Summary, "ALLOW") {
		t.Fatalf("empty-signal summary: %q", exp.Summary)
	}
	if len(exp.Lines) != 0 {
		t.Fatalf("no signals → no lines: %+v", exp)
	}
}

func TestQueueEnqueueLeaseDecide(t *testing.T) {
	q := NewQueue()
	c := q.Enqueue("txn-1", 85, time.Hour, tnow)
	if c.Priority != "CRITICAL" {
		t.Fatalf("85 risk must be critical: %+v", c)
	}
	if c.Status != StatusOpen {
		t.Fatalf("new case open: %+v", c)
	}
	if err := q.Lease(c.ID, "amy", 10*time.Minute, tnow); err != nil {
		t.Fatal(err)
	}
	// Second analyst conflicts while the lease is live.
	if err := q.Lease(c.ID, "bo", 10*time.Minute, tnow); err != ErrLeaseConflict {
		t.Fatalf("live lease must conflict: %v", err)
	}
	// Expired lease can be stolen.
	if err := q.Lease(c.ID, "bo", 10*time.Minute, tnow.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := q.Decide(c.ID, "bo", DecideApprove, "looks fine", tnow.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(c.ID)
	if got.Status != StatusDecided || got.Decision != DecideApprove {
		t.Fatalf("decided: %+v", got)
	}
	if err := q.Decide(c.ID, "bo", DecideReject, "", tnow); err != ErrCaseDecided {
		t.Fatalf("double decide must fail: %v", err)
	}
	trail, err := q.Audit(c.ID)
	if err != nil || len(trail) < 4 {
		t.Fatalf("full audit expected (enqueue/lease/lease/decide): %+v %v", trail, err)
	}
	if trail[0].Action != "enqueue" || trail[len(trail)-1].Action != "decide" {
		t.Fatalf("audit order: %+v", trail)
	}
}

func TestQueueDecisionsAndMoves(t *testing.T) {
	q := NewQueue()
	c := q.Enqueue("txn-2", 30, time.Hour, tnow)
	if c.Priority != "LOW" {
		t.Fatalf("30 risk must be low: %+v", c)
	}
	if err := q.Decide(c.ID, "amy", "ban", "", tnow); err != ErrBadDecision {
		t.Fatalf("unknown decision must fail: %v", err)
	}
	for _, d := range []string{DecideReject, DecideRequestEvidence, DecideEscalate} {
		cc := q.Enqueue("s", 50, time.Hour, tnow)
		if err := q.Decide(cc.ID, "amy", d, "", tnow); err != nil {
			t.Fatalf("decision %s: %v", d, err)
		}
	}
	if err := q.Escalate(c.ID, "amy", "tier2", tnow); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(c.ID)
	if got.Status != StatusEscalated || got.Assignee != "tier2" {
		t.Fatalf("escalated: %+v", got)
	}
	if err := q.Reassign(c.ID, "lead", "bo", tnow); err != nil {
		t.Fatal(err)
	}
	got, _ = q.Get(c.ID)
	if got.Assignee != "bo" {
		t.Fatalf("reassigned: %+v", got)
	}
	if _, err := q.Get("missing"); err != ErrCaseNotFound {
		t.Fatalf("unknown case: %v", err)
	}
	if _, err := q.Audit("missing"); err != ErrCaseNotFound {
		t.Fatalf("unknown audit: %v", err)
	}
}

func TestQueueSweepTimeouts(t *testing.T) {
	q := NewQueue()
	leased := q.Enqueue("a", 70, time.Hour, tnow)
	if err := q.Lease(leased.ID, "amy", 5*time.Minute, tnow); err != nil {
		t.Fatal(err)
	}
	sla := q.Enqueue("b", 90, 5*time.Minute, tnow)
	swept := q.SweepTimeouts(tnow.Add(time.Minute))
	if len(swept) != 0 {
		t.Fatalf("nothing due yet: %v", swept)
	}
	swept = q.SweepTimeouts(tnow.Add(6 * time.Minute))
	if len(swept) != 2 {
		t.Fatalf("lease expiry + SLA breach: %v", swept)
	}
	got, _ := q.Get(leased.ID)
	if got.LeaseHolder != "" {
		t.Fatalf("lease must be released: %+v", got)
	}
	got, _ = q.Get(sla.ID)
	if got.Status != StatusEscalated {
		t.Fatalf("SLA breach auto-escalates: %+v", got)
	}
}

func TestModelAuditTrail(t *testing.T) {
	s := NewModelStore()
	r := s.Record("fraud-model-7", "amount=100|geo=uk", "fraud-v3/v12", "BLOCK", 0.93, false, "", tnow)
	if r.FeatureSnapshotHash == "" || r.FeatureSnapshotHash == "amount=100|geo=uk" {
		t.Fatalf("snapshot must be hashed: %+v", r)
	}
	v, err := s.Verify(r.ID, "amount=100|geo=uk")
	if err != nil || v != VerdictReproduced {
		t.Fatalf("same snapshot reproduces: %s %v", v, err)
	}
	v, err = s.Verify(r.ID, "amount=999|geo=uk")
	if err != nil || v != VerdictTampered {
		t.Fatalf("changed snapshot tampers: %s %v", v, err)
	}
	// Human override is part of the immutable record.
	o := s.Record("fraud-model-7", "snap", "fraud-v3/v12", "ALLOW", 0.4, true, "amy", tnow)
	if !o.HumanOverride || o.OverrideBy != "amy" {
		t.Fatalf("override must persist: %+v", o)
	}
	got, err := s.Get(r.ID)
	if err != nil || got.ModelVersion != "fraud-model-7" || got.Decision != "BLOCK" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Get("missing"); err == nil {
		t.Fatal("unknown record must fail")
	}
	if _, err := s.Verify("missing", "x"); err == nil {
		t.Fatal("verify on unknown must fail")
	}
}
