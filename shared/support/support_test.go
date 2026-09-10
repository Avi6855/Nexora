package support

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)

func TestPurposeFiltering(t *testing.T) {
	src := SourceData{
		CustomerID: "c1", AccountState: "OPEN", CardState: "ACTIVE",
		PaymentState: "SETTLED", RecentActivity: []string{"paid Tesco £48.20"},
		OpenCases: []string{"case-1"}, Comms: []string{"email 3 Sep"},
		Vulnerable: true, InternalNotes: []string{"never exported"},
	}
	card := BuildContext(src, PurposeCardSupport)
	if card.CardState == "" || card.PaymentState != "" {
		t.Fatalf("card purpose sees cards, not payments: %+v", card)
	}
	if card.Vulnerable {
		t.Fatal("card purpose is operational — vulnerability flag is fraud/general only")
	}
	pay := BuildContext(src, PurposePaymentSupport)
	if pay.PaymentState == "" || pay.CardState != "" {
		t.Fatalf("payment purpose sees payments, not cards: %+v", pay)
	}
	fraud := BuildContext(src, PurposeFraudSupport)
	if fraud.CardState == "" || fraud.PaymentState == "" || !fraud.Vulnerable {
		t.Fatal("fraud purpose sees everything relevant")
	}
	for _, v := range []ContextView{card, pay, fraud} {
		found := false
		for _, omitted := range v.Omitted {
			if omitted == "internal_notes" {
				found = true
			}
		}
		if !found {
			t.Fatal("internal notes must be declared omitted, never exposed")
		}
		if len(v.Omitted) == 0 {
			t.Fatal("omitted fields must be declared for transparency")
		}
	}
}

func TestCaseLifecycle(t *testing.T) {
	c := &Case{ID: "case-1", State: CaseOpen, Priority: PriorityNormal, CreatedAt: t0,
		Deadline: t0.Add(SLATarget(PriorityNormal))}
	if err := c.Move(CaseWaitingCustomer, t0); err == nil {
		t.Fatal("OPEN → WAITING_CUSTOMER is illegal")
	}
	if err := c.Move(CaseAssigned, t0); err != nil {
		t.Fatal(err)
	}
	if err := c.Move(CaseWaitingExternal, t0); err != nil {
		t.Fatal(err)
	}
	if err := c.Move(CaseResolved, t0); err != nil {
		t.Fatal(err)
	}
	// From RESOLVED the only exit is REOPENED.
	if err := c.Move(CaseAssigned, t0); err == nil {
		t.Fatal("RESOLVED → ASSIGNED must go through REOPENED")
	}
	if err := c.Move(CaseReopened, t0); err != nil {
		t.Fatal(err)
	}
	if !c.Deadline.After(t0) {
		t.Fatal("reopen re-arms the SLA clock")
	}
}

func TestSLABreachAndEscalation(t *testing.T) {
	c := &Case{ID: "case-2", State: CaseAssigned, Priority: PriorityHigh, CreatedAt: t0,
		Deadline: t0.Add(SLATarget(PriorityHigh))}
	breachAt := t0.Add(2 * time.Hour)
	if !c.SLABreached(breachAt) {
		t.Fatal("2h old high-priority case has breached its 1h SLA")
	}
	if c.BreachCount != 1 {
		t.Fatalf("breach count %d, want 1", c.BreachCount)
	}
	// Deadline re-armed: not breached again immediately.
	if c.SLABreached(breachAt.Add(time.Minute)) {
		t.Fatal("re-armed deadline must not double-count")
	}
	// Escalation halves the SLA.
	if err := c.Escalate(breachAt); err != nil {
		t.Fatal(err)
	}
	if c.State != CaseEscalated {
		t.Fatalf("state %s, want ESCALATED", c.State)
	}
	if want := breachAt.Add(SLATarget(PriorityHigh) / 2); !c.Deadline.Equal(want) {
		t.Fatalf("escalated deadline %v, want %v", c.Deadline, want)
	}
}

func TestFairnessRanking(t *testing.T) {
	now := t0.Add(30 * time.Minute)
	cases := []QueueCase{
		{Case: Case{ID: "a", Priority: PriorityNormal, CreatedAt: now.Add(-time.Minute),
			Deadline: now.Add(8 * time.Hour)}, CustomerImpact: 0.1},
		{Case: Case{ID: "b", Priority: PriorityCritical, CreatedAt: now.Add(-14 * time.Minute),
			Deadline: now.Add(time.Minute)}, CustomerImpact: 0.9, FinancialExposure: 200000, VulnerableCustomer: true},
		{Case: Case{ID: "c", Priority: PriorityNormal, CreatedAt: now.Add(-5 * time.Minute),
			Deadline: now.Add(4 * time.Hour)}, CustomerImpact: 0.5},
	}
	ranked := Rank(cases, now)
	if ranked[0].ID != "b" {
		t.Fatalf("critical+vulnerable+blocked must rank first, got %s", ranked[0].ID)
	}
	// Vulnerability outranks exposure alone: make d richer but not vulnerable.
	d := QueueCase{Case: Case{ID: "d", Priority: PriorityHigh, CreatedAt: now.Add(-time.Minute),
		Deadline: now.Add(time.Hour)}, CustomerImpact: 0.5, FinancialExposure: 500000}
	e := QueueCase{Case: Case{ID: "e", Priority: PriorityHigh, CreatedAt: now.Add(-time.Minute),
		Deadline: now.Add(time.Hour)}, CustomerImpact: 0.5, FinancialExposure: 100000, VulnerableCustomer: true}
	ranked = Rank([]QueueCase{d, e}, now)
	if ranked[0].ID != "e" {
		t.Fatalf("vulnerability must outrank raw exposure: got %s", ranked[0].ID)
	}
}

func TestDeduplication(t *testing.T) {
	existing := Case{ID: "case-9", Subject: "Card declined at Tesco", Channel: "CHAT",
		CreatedAt: t0, LinkedTxnID: "tx-77"}
	base := DedupInput{Existing: existing, NewSubject: "my card was declined at tesco",
		NewChannel: "EMAIL", SameCustomer: true, WindowHours: 2}

	// Same transaction, similar text, inside window → MERGE.
	in := base
	in.SameTransaction = true
	if got := Dedup(in); got != DedupMerge {
		t.Fatalf("want MERGE, got %s", got)
	}
	// High text similarity alone also merges.
	in = base
	if got := Dedup(in); got != DedupMerge {
		t.Fatalf("similar text must MERGE, got %s", got)
	}
	// Same transaction but a week later → LINK (related, not the same report).
	in = base
	in.WindowHours = 200
	if got := Dedup(in); got != DedupLink {
		t.Fatalf("stale same-txn report should LINK, got %s", got)
	}
	// Different problem entirely.
	in = base
	in.NewSubject = "how do I close my savings pot"
	in.SameTransaction = false
	if got := Dedup(in); got != DedupKeepSeparate {
		t.Fatalf("unrelated subject must KEEP_SEPARATE, got %s", got)
	}
	// Different customer is never merged, however similar.
	in = base
	in.SameCustomer = false
	if got := Dedup(in); got != DedupKeepSeparate {
		t.Fatalf("cross-customer must KEEP_SEPARATE, got %s", got)
	}
}
