package ledgerguard

import (
	"errors"
	"testing"
	"time"
)

func balancedEntries() []Entry {
	return []Entry{
		{Account: "cash", Class: ClassAsset, Debit: 10000},
		{Account: "deposits", Class: ClassLiability, Credit: 10000},
	}
}

func TestBalancedPostPasses(t *testing.T) {
	m := NewMonitor()
	res, err := m.Post("j1", balancedEntries())
	if err != nil {
		t.Fatalf("balanced post failed: %v", err)
	}
	if res.Status != CheckPass {
		t.Fatalf("expected PASS, got %s (%s)", res.Status, res.Message)
	}
	if m.IsFrozen("j1") {
		t.Fatal("balanced journal must not freeze")
	}
	if len(m.Incidents()) != 0 {
		t.Fatalf("expected no incidents, got %d", len(m.Incidents()))
	}
}

func TestUnbalancedPostViolatesAndFreezes(t *testing.T) {
	m := NewMonitor()
	entries := []Entry{
		{Account: "cash", Class: ClassAsset, Debit: 10000},
		{Account: "deposits", Class: ClassLiability, Credit: 9000},
	}
	res, err := m.Post("j-bad", entries)
	if !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("expected ErrUnbalanced, got %v", err)
	}
	if res == nil || res.Status != CheckViolation {
		t.Fatalf("expected VIOLATION result, got %+v", res)
	}
	if res.IncidentID == "" {
		t.Fatal("violation must carry an incident ID")
	}
	if !m.IsFrozen("j-bad") {
		t.Fatal("violating journal must freeze")
	}
	incidents := m.Incidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	if incidents[0].Severity != "ALERT" {
		t.Fatalf("incident severity must be ALERT, got %q", incidents[0].Severity)
	}
	if incidents[0].JournalID != "j-bad" {
		t.Fatalf("incident journal mismatch: %q", incidents[0].JournalID)
	}
}

func TestMutationBlockedWhileFrozen(t *testing.T) {
	m := NewMonitor()
	bad := []Entry{
		{Account: "cash", Class: ClassAsset, Debit: 500},
		{Account: "deposits", Class: ClassLiability, Credit: 400},
	}
	if _, err := m.Post("j2", bad); err == nil {
		t.Fatal("expected violation on bad post")
	}
	// Any further mutation of the frozen journal is blocked.
	if _, err := m.Post("j2", balancedEntries()); !errors.Is(err, ErrJournalFrozen) {
		t.Fatalf("expected ErrJournalFrozen, got %v", err)
	}
	// Explicit freeze also blocks.
	if err := m.Freeze("j3"); err != nil {
		t.Fatalf("freeze failed: %v", err)
	}
	if _, err := m.Post("j3", balancedEntries()); !errors.Is(err, ErrJournalFrozen) {
		t.Fatalf("expected frozen block, got %v", err)
	}
	// Unfreeze re-opens the journal.
	if err := m.Unfreeze("j3"); err != nil {
		t.Fatalf("unfreeze failed: %v", err)
	}
	if _, err := m.Post("j3", balancedEntries()); err != nil {
		t.Fatalf("post after unfreeze failed: %v", err)
	}
	// Clearing the incident also lifts the freeze.
	incidents := m.Incidents()
	if len(incidents) == 0 {
		t.Fatal("expected incidents to clear")
	}
	if err := m.ClearIncident(incidents[0].ID); err != nil {
		t.Fatalf("clear incident failed: %v", err)
	}
	if m.IsFrozen("j2") {
		t.Fatal("clearing the only incident must unfreeze the journal")
	}
	if _, err := m.Post("j2", balancedEntries()); err != nil {
		t.Fatalf("post after clear failed: %v", err)
	}
}

func TestReservationMathPartialCaptureReversalExpiry(t *testing.T) {
	e := NewEngine()
	e.SetBalance("acct-1", 10000)

	r, err := e.Authorize("res-1", "acct-1", 4000, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	if r.Status != ReservationActive {
		t.Fatalf("expected ACTIVE, got %s", r.Status)
	}
	b := e.Balances("acct-1")
	if b.Ledger != 10000 || b.Reserved != 4000 || b.Available != 6000 {
		t.Fatalf("bad balances after authorize: %+v", b)
	}

	// Incremental authorisation.
	if _, err := e.TopUp("res-1", 1000); err != nil {
		t.Fatalf("topup failed: %v", err)
	}
	if b := e.Balances("acct-1"); b.Available != 5000 {
		t.Fatalf("available after topup must be 5000, got %d", b.Available)
	}

	// Partial capture settles funds but keeps the hold open.
	c, err := e.Capture("res-1", 2000)
	if err != nil {
		t.Fatalf("partial capture failed: %v", err)
	}
	if c.Captured != 2000 || c.Status != ReservationActive {
		t.Fatalf("partial capture must stay ACTIVE with captured=2000, got %+v", c)
	}
	b = e.Balances("acct-1")
	// Ledger drops by captured amount; reserved drops equally; available flat.
	if b.Ledger != 8000 || b.Reserved != 3000 || b.Available != 5000 {
		t.Fatalf("bad balances after partial capture: %+v", b)
	}

	// Reversal releases the remainder without moving more funds.
	rev, err := e.Reverse("res-1")
	if err != nil {
		t.Fatalf("reverse failed: %v", err)
	}
	if rev.Status != ReservationReversed {
		t.Fatalf("expected REVERSED, got %s", rev.Status)
	}
	b = e.Balances("acct-1")
	if b.Reserved != 0 || b.Available != 8000 {
		t.Fatalf("bad balances after reversal: %+v", b)
	}

	// Expiry sweep releases holds whose TTL passed.
	if _, err := e.Authorize("res-exp", "acct-1", 1000, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("authorize expiring hold failed: %v", err)
	}
	// Authorize with past expiry still books (available checked at call time);
	// the sweep expires it.
	if n := e.SweepExpired(time.Now().UTC()); n != 1 {
		t.Fatalf("expected 1 expiry, got %d", n)
	}
	got, err := e.Get("res-exp")
	if err != nil {
		t.Fatalf("get expired failed: %v", err)
	}
	if got.Status != ReservationExpired {
		t.Fatalf("expected EXPIRED, got %s", got.Status)
	}
	if b := e.Balances("acct-1"); b.Reserved != 0 {
		t.Fatalf("expired hold must release reserved, got %+v", b)
	}
	// Capturing an expired hold is rejected.
	if _, err := e.Capture("res-exp", 100); !errors.Is(err, ErrReservationNotActive) {
		t.Fatalf("expected not-active on expired capture, got %v", err)
	}
}

func TestLatePresentmentFlaggedOnce(t *testing.T) {
	e := NewEngine()
	e.SetBalance("acct-late", 5000)
	if _, err := e.Authorize("res-late", "acct-late", 1000, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	if n := e.SweepExpired(time.Now().UTC().Add(2 * time.Hour)); n != 1 {
		t.Fatalf("expected sweep to expire, got %d", n)
	}
	before := e.Balances("acct-late")
	first, err := e.SettleLate("res-late", 1000)
	if err != nil {
		t.Fatalf("settle-late failed: %v", err)
	}
	if !first.Review {
		t.Fatal("late presentment must force review flag")
	}
	after := e.Balances("acct-late")
	if after.Ledger != before.Ledger-1000 {
		t.Fatalf("late settlement must move funds once: before=%d after=%d", before.Ledger, after.Ledger)
	}
	// Repeating never double-spends and stays flagged.
	second, err := e.SettleLate("res-late", 1000)
	if err != nil {
		t.Fatalf("repeat settle-late failed: %v", err)
	}
	if !second.Review {
		t.Fatal("review flag must persist")
	}
	final := e.Balances("acct-late")
	if final.Ledger != after.Ledger {
		t.Fatalf("repeat settle-late double-spent: %d -> %d", after.Ledger, final.Ledger)
	}
}

func TestDuplicateReservationIDRejected(t *testing.T) {
	e := NewEngine()
	e.SetBalance("acct-dup", 5000)
	if _, err := e.Authorize("dup-1", "acct-dup", 100, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("first authorize failed: %v", err)
	}
	if _, err := e.Authorize("dup-1", "acct-dup", 100, time.Now().UTC().Add(time.Hour)); !errors.Is(err, ErrDuplicateReservation) {
		t.Fatalf("expected ErrDuplicateReservation, got %v", err)
	}
}

func TestFullCaptureClosesReservation(t *testing.T) {
	e := NewEngine()
	e.SetBalance("acct-full", 2000)
	if _, err := e.Authorize("res-full", "acct-full", 1500, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	r, err := e.Capture("res-full", 1500)
	if err != nil {
		t.Fatalf("full capture failed: %v", err)
	}
	if r.Status != ReservationCaptured {
		t.Fatalf("expected CAPTURED, got %s", r.Status)
	}
	if b := e.Balances("acct-full"); b.Ledger != 500 || b.Reserved != 0 || b.Available != 500 {
		t.Fatalf("bad balances after full capture: %+v", b)
	}
}
