package financeops

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func testStore() *Store {
	return NewStore(zerolog.Nop())
}

func TestAdjustmentWorkflow(t *testing.T) {
	s := testStore()
	orig, err := s.CreateEntry("expense:ops", "cash", 5000, "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	adj, err := s.RequestAdjustment(orig.ID, "wrong cost centre")
	if err != nil {
		t.Fatal(err)
	}
	if adj.Status != AdjustmentPending {
		t.Fatalf("new adjustment must be PENDING: %+v", adj)
	}
	// Not balanced before approval.
	if ok, _, err := s.VerifyBalanced(adj.ID); err != nil || ok {
		t.Fatalf("pending adjustment must not verify: %v %v", ok, err)
	}
	approved, comp, err := s.ApproveAdjustment(adj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != AdjustmentApproved || approved.CompensatingID != comp.ID {
		t.Fatalf("approve must link compensating: %+v %+v", approved, comp)
	}
	// Compensating legs reverse the original and reference it.
	if comp.DebitAccount != orig.CreditAccount || comp.CreditAccount != orig.DebitAccount {
		t.Fatalf("legs must reverse: orig=%+v comp=%+v", orig, comp)
	}
	if comp.Amount != orig.Amount || comp.Reference != orig.ID {
		t.Fatalf("compensating must match amount and reference: %+v", comp)
	}
	// Original never mutated.
	again, err := s.GetEntry(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.DebitAccount != "expense:ops" || again.Amount != 5000 {
		t.Fatalf("original must be immutable: %+v", again)
	}
	ok, msg, err := s.VerifyBalanced(adj.ID)
	if err != nil || !ok || msg == "" {
		t.Fatalf("approved pair must verify balanced: %v %q %v", ok, msg, err)
	}
	// Double approve conflicts.
	if _, _, err := s.ApproveAdjustment(adj.ID); !errors.Is(err, ErrAdjustmentState) {
		t.Fatalf("double approve must conflict, got %v", err)
	}
	if _, err := s.GetAdjustment("missing"); !errors.Is(err, ErrAdjustmentNotFound) {
		t.Fatalf("unknown adjustment must 404, got %v", err)
	}
	if _, err := s.RequestAdjustment("missing", "x"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("unknown original must 404, got %v", err)
	}
}

func TestBackdatedEvents(t *testing.T) {
	s := testStore()
	t1 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	ev, err := s.PostBackdatedEventAt("acct-1", 1000, t1, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.EffectiveAt.Equal(t1) || !ev.RecordedAt.Equal(now) || !ev.ProcessedAt.Equal(now) {
		t.Fatalf("triple must be preserved: %+v", ev)
	}
	if _, err := s.PostBackdatedEventAt("acct-1", 500, t2, now); err != nil {
		t.Fatal(err)
	}
	// As-of recomputation uses effective time, not arrival.
	if bal := s.BalanceAsOf("acct-1", time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)); bal != 0 {
		t.Fatalf("balance before events must be 0, got %d", bal)
	}
	if bal := s.BalanceAsOf("acct-1", time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)); bal != 1000 {
		t.Fatalf("as-of Sep 5 must be 1000, got %d", bal)
	}
	if bal := s.BalanceAsOf("acct-1", now); bal != 1500 {
		t.Fatalf("as-of now must be 1500, got %d", bal)
	}
	// Other accounts are isolated.
	if bal := s.BalanceAsOf("acct-2", now); bal != 0 {
		t.Fatalf("other account must be 0, got %d", bal)
	}
}

func TestCloseControl(t *testing.T) {
	s := testStore()
	res := s.CheckClose(Checklist{true, true, true, true})
	if res.Verdict != "CLOSE" || res.Blocked || len(res.Reasons) != 0 {
		t.Fatalf("full checklist must CLOSE: %+v", res)
	}
	blocked := s.CheckClose(Checklist{TransactionsComplete: true})
	if blocked.Verdict != "BLOCK" || !blocked.Blocked {
		t.Fatalf("partial checklist must BLOCK: %+v", blocked)
	}
	if len(blocked.Reasons) != 3 {
		t.Fatalf("three failed checks must yield three reasons: %+v", blocked)
	}
	none := s.CheckClose(Checklist{})
	if len(none.Reasons) != 4 {
		t.Fatalf("empty checklist must list four reasons: %+v", none)
	}
}

func TestPeriodLockingRedirect(t *testing.T) {
	s := testStore()
	if err := s.Lock("2026-09"); err != nil {
		t.Fatal(err)
	}
	if !s.IsLocked("2026-09") {
		t.Fatal("2026-09 must be locked")
	}
	c, err := s.PostCorrection("2026-09", "expense:ops", "cash", 250)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Redirected || c.ActualPeriod != "2026-10" {
		t.Fatalf("locked correction must redirect to 2026-10: %+v", c)
	}
	if c.RequestedPeriod != "2026-09" {
		t.Fatalf("requested period must be preserved: %+v", c)
	}
	// Open periods are never redirected.
	open, err := s.PostCorrection("2026-10", "expense:ops", "cash", 100)
	if err != nil {
		t.Fatal(err)
	}
	if open.Redirected || open.ActualPeriod != "2026-10" {
		t.Fatalf("open correction must not redirect: %+v", open)
	}
	// Chained locks skip forward.
	if err := s.Lock("2026-10"); err != nil {
		t.Fatal(err)
	}
	c2, err := s.PostCorrection("2026-09", "expense:ops", "cash", 50)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ActualPeriod != "2026-11" {
		t.Fatalf("double-locked correction must reach 2026-11: %+v", c2)
	}
	// Locked-period entries are untouched: the correction lands as a new entry
	// in the actual period.
	ent, err := s.GetEntry(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ent.Period != "2026-10" {
		t.Fatalf("correction entry must live in 2026-10: %+v", ent)
	}
}

func TestPostingRulesRollback(t *testing.T) {
	s := testStore()
	r1, err := s.PutRule("card_settlement", "clearing:card", "cash")
	_ = r1
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != 1 {
		t.Fatalf("first version must be 1: %+v", r1)
	}
	r2, err := s.PutRule("card_settlement", "clearing:card-v2", "cash")
	if err != nil || r2.Version != 2 {
		t.Fatalf("second version must be 2: %+v %v", r2, err)
	}
	latest, err := s.Resolve("card_settlement", 0)
	if err != nil || latest.Version != 2 || latest.DebitAccount != "clearing:card-v2" {
		t.Fatalf("resolve latest must be v2: %+v %v", latest, err)
	}
	v1, err := s.Resolve("card_settlement", 1)
	if err != nil || v1.DebitAccount != "clearing:card" {
		t.Fatalf("resolve v1 must be original: %+v %v", v1, err)
	}
	restored, err := s.Rollback("card_settlement")
	if err != nil || restored.Version != 1 {
		t.Fatalf("rollback must restore v1: %+v %v", restored, err)
	}
	if _, err := s.Rollback("card_settlement"); !errors.Is(err, ErrNoPriorVersion) {
		t.Fatalf("double rollback must fail, got %v", err)
	}
	if _, err := s.Resolve("card_settlement", 2); !errors.Is(err, ErrRuleVersionNotFound) {
		t.Fatalf("rolled-back version must 404, got %v", err)
	}
	if _, err := s.Resolve("missing", 0); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("unknown type must 404, got %v", err)
	}
	if _, err := s.Rollback("missing"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("unknown rollback must 404, got %v", err)
	}
}

func TestSubledgersConsolidation(t *testing.T) {
	s := testStore()
	if _, err := s.PostSubledger("card", "clearing:card", "cash", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostSubledger("card", "clearing:card", "cash", 500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostSubledger("loans", "loans:receivable", "cash", 2000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostSubledger("fees", "fees:receivable", "cash", 100); err != nil {
		t.Fatal(err)
	}
	card, err := s.TrialBalance("card")
	if err != nil {
		t.Fatal(err)
	}
	if card.Debits != 1500 || card.Credits != 1500 || !card.Balanced || card.Count != 2 {
		t.Fatalf("bad card trial: %+v", card)
	}
	loans, _ := s.TrialBalance("loans")
	if loans.Debits != 2000 || loans.Count != 1 {
		t.Fatalf("bad loans trial: %+v", loans)
	}
	// Untouched domains are empty but balanced.
	savings, _ := s.TrialBalance("savings")
	if savings.Debits != 0 || !savings.Balanced || savings.Count != 0 {
		t.Fatalf("empty domain must be zero-balanced: %+v", savings)
	}
	view := s.Consolidated()
	if view.Debits != 3600 || view.Credits != 3600 || !view.Balanced {
		t.Fatalf("consolidated must be 3600 balanced: %+v", view)
	}
	if view.PerDomain["card"].Debits != 1500 || view.PerDomain["loans"].Debits != 2000 {
		t.Fatalf("per-domain consolidation wrong: %+v", view.PerDomain)
	}
	if _, err := s.PostSubledger("nope", "a", "b", 10); err == nil {
		t.Fatal("unknown domain must fail")
	}
	if _, err := s.TrialBalance("nope"); err == nil {
		t.Fatal("unknown trial must fail")
	}
}
