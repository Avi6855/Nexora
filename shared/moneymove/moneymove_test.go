package moneymove

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func testStore() *Store {
	return NewStore(zerolog.Nop())
}

func TestIntentLedgerDivergence(t *testing.T) {
	s := testStore()
	rec, err := s.RecordIntent("pay landlord £500", 50000, "landlord", []string{"debit:acct-1", "credit:landlord"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Intent.Amount != 50000 || rec.Intent.Payee != "landlord" {
		t.Fatalf("bad intent: %+v", rec.Intent)
	}
	if len(rec.Plan.Steps) != 2 {
		t.Fatalf("bad plan: %+v", rec.Plan)
	}

	// Matching legs: no divergence.
	ok, err := s.ExecuteIntent(rec.Intent.ID, []string{"debit:acct-1", "credit:landlord"})
	if err != nil {
		t.Fatal(err)
	}
	if ok.Diverged || ok.Reason != "" {
		t.Fatalf("matching execution must not diverge: %+v", ok)
	}
	diverged, reason, err := s.Divergence(rec.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diverged || reason != "" {
		t.Fatalf("divergence must be false: %v %q", diverged, reason)
	}

	// Diverging legs: flag with reason.
	bad, err := s.ExecuteIntent(rec.Intent.ID, []string{"debit:acct-1", "credit:stranger"})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.Diverged || bad.Reason == "" {
		t.Fatalf("mismatched legs must diverge: %+v", bad)
	}
	diverged, reason, err = s.Divergence(rec.Intent.ID)
	if err != nil || !diverged || reason == "" {
		t.Fatalf("divergence must report reason: %v %q %v", diverged, reason, err)
	}

	// Length mismatch also diverges.
	short, err := s.ExecuteIntent(rec.Intent.ID, []string{"debit:acct-1"})
	if err != nil || !short.Diverged {
		t.Fatalf("short legs must diverge: %+v %v", short, err)
	}

	if _, err := s.GetIntent("missing"); !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("unknown intent must 404, got %v", err)
	}
	if _, _, err := s.Divergence("missing"); !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("unknown divergence must 404, got %v", err)
	}
	if _, err := s.RecordIntent("", 10, "p", []string{"s"}); err == nil {
		t.Fatal("empty statement must fail")
	}
	if _, err := s.ExecuteIntent(rec.Intent.ID, nil); err == nil {
		t.Fatal("empty legs must fail")
	}
}

func TestInstructionVersioning(t *testing.T) {
	s := testStore()
	ins, err := s.CreateInstruction(1000, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ins.Version != 1 {
		t.Fatalf("new instruction must be v1, got v%d", ins.Version)
	}
	if v, err := s.GetVersion(ins.ID); err != nil || v != 1 {
		t.Fatalf("GetVersion must be 1: %d %v", v, err)
	}

	u2, err := s.UpdateInstruction(ins.ID, 1, 2000, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if u2.Version != 2 || u2.Amount != 2000 || u2.Payee != "bob" {
		t.Fatalf("bad v2: %+v", u2)
	}
	u3, err := s.UpdateInstruction(ins.ID, 2, 3000, "carol")
	if err != nil || u3.Version != 3 {
		t.Fatalf("bad v3: %+v %v", u3, err)
	}

	h, err := s.History(ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 3 || h[0].Version != 1 || h[1].Version != 2 || h[2].Version != 3 {
		t.Fatalf("history must be v1..v3 in order: %+v", h)
	}
	// History must be a copy: mutating it must not affect the store.
	h[0].Amount = 999999
	again, _ := s.History(ins.ID)
	if again[0].Amount == 999999 {
		t.Fatal("history must be immutable copies")
	}
	// Originals accumulate: current version stays v3.
	cur, _ := s.GetInstruction(ins.ID)
	if cur.Version != 3 {
		t.Fatalf("current must stay v3: %+v", cur)
	}

	// Stale expected_version conflicts with 409-style error.
	if _, err := s.UpdateInstruction(ins.ID, 1, 4000, "dave"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update must conflict, got %v", err)
	}
	if v, _ := s.GetVersion(ins.ID); v != 3 {
		t.Fatalf("failed update must not bump version: v%d", v)
	}
	if _, err := s.GetInstruction("missing"); !errors.Is(err, ErrInstructionNotFound) {
		t.Fatalf("unknown instruction must 404, got %v", err)
	}
	if _, err := s.History("missing"); !errors.Is(err, ErrInstructionNotFound) {
		t.Fatalf("unknown history must 404, got %v", err)
	}
}

func TestPreconditionEngine(t *testing.T) {
	s := testStore()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cutoff := now.Add(time.Hour).Format(time.RFC3339)
	good := PreconditionPayment{Amount: 1000, AccountID: "a1", Recipient: "bob", Currency: "GBP"}
	goodState := PreconditionState{
		Balance:             5000,
		AccountActive:       true,
		RecipientValid:      true,
		LegalBlocked:        false,
		SupportedCurrencies: []string{"GBP", "USD"},
		Cutoff:              cutoff,
	}
	res := s.Evaluate(good, goodState, now)
	if !res.Passed || res.Verdict != "PASS" || len(res.Failed) != 0 {
		t.Fatalf("good payment must PASS: %+v", res)
	}

	bad := PreconditionPayment{Amount: 9000, AccountID: "a1", Recipient: "", Currency: "XXX"}
	badState := PreconditionState{
		Balance:             100,
		AccountActive:       false,
		RecipientValid:      false,
		LegalBlocked:        true,
		SupportedCurrencies: []string{"GBP"},
		Cutoff:              now.Add(-time.Hour).Format(time.RFC3339),
	}
	res = s.Evaluate(bad, badState, now)
	if res.Passed || res.Verdict != "FAIL" {
		t.Fatalf("bad payment must FAIL: %+v", res)
	}
	want := map[string]bool{
		PredicateBalanceSufficient: true,
		PredicateAccountActive:     true,
		PredicateRecipientValid:    true,
		PredicateNoLegalBlock:      true,
		PredicateCurrencySupported: true,
		PredicateBeforeCutoff:      true,
	}
	if len(res.Failed) != len(want) {
		t.Fatalf("all six predicates must fail: %v", res.Failed)
	}
	for _, f := range res.Failed {
		if !want[f] {
			t.Fatalf("unexpected failed predicate %q", f)
		}
	}

	// Single failures name the exact predicate.
	single := s.Evaluate(good, PreconditionState{
		Balance: 5000, AccountActive: true, RecipientValid: true,
		SupportedCurrencies: []string{"GBP"},
	}, now)
	if !single.Passed {
		t.Fatalf("empty cutoff must pass: %+v", single)
	}
	poor := goodState
	poor.Balance = 10
	if r := s.Evaluate(good, poor, now); r.Passed || len(r.Failed) != 1 || r.Failed[0] != PredicateBalanceSufficient {
		t.Fatalf("short balance must fail balance-sufficient only: %+v", r)
	}
}

func TestAtomicReservationConcurrency(t *testing.T) {
	s := testStore()
	s.Fund("acct-1", 1000)
	const racers = 16
	var wg sync.WaitGroup
	wins := make(chan string, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Reserve("acct-1", 1000)
			if err == nil {
				wins <- r.ID
			}
		}()
	}
	wg.Wait()
	close(wins)
	var ids []string
	for id := range wins {
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		t.Fatalf("exactly one winner expected, got %d", len(ids))
	}
	if avail := s.Available("acct-1"); avail != 0 {
		t.Fatalf("available must be 0 after winning hold, got %d", avail)
	}
	// No negative availability: further reserves fail.
	if _, err := s.Reserve("acct-1", 1); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("oversubscription must fail, got %v", err)
	}
	// Release restores availability; consume settles the ledger.
	if _, err := s.Release(ids[0]); err != nil {
		t.Fatal(err)
	}
	if avail := s.Available("acct-1"); avail != 1000 {
		t.Fatalf("release must restore 1000, got %d", avail)
	}
	r2, err := s.Reserve("acct-1", 400)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(r2.ID); err != nil {
		t.Fatal(err)
	}
	if avail := s.Available("acct-1"); avail != 600 {
		t.Fatalf("consume must leave 600 available, got %d", avail)
	}
	if _, err := s.Release(r2.ID); !errors.Is(err, ErrReservationNotActive) {
		t.Fatalf("double-settle must fail, got %v", err)
	}
	if _, err := s.Release("missing"); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("unknown release must 404, got %v", err)
	}
}

func TestLeasesFencing(t *testing.T) {
	s := testStore()
	base := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	l1, err := s.AcquireAt("payroll", time.Minute, base)
	if err != nil {
		t.Fatal(err)
	}
	if l1.Token != 1 {
		t.Fatalf("first token must be 1, got %d", l1.Token)
	}
	// Live holder blocks takeover.
	if _, err := s.AcquireAt("payroll", time.Minute, base.Add(time.Second)); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("double acquire must fail held, got %v", err)
	}
	// Execute under the right token succeeds.
	if _, err := s.ExecuteUnderLeaseAt(l1.ID, l1.Token, base.Add(time.Second)); err != nil {
		t.Fatalf("execute under lease must succeed: %v", err)
	}
	// Stale token rejected.
	if _, err := s.ExecuteUnderLeaseAt(l1.ID, 999, base.Add(time.Second)); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("stale token must fail, got %v", err)
	}
	// Renew extends the window.
	renewed, err := s.RenewAt(l1.ID, l1.Token, 10*time.Minute, base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.ExpiresAt.After(l1.ExpiresAt) {
		t.Fatal("renew must extend expiry")
	}
	// Renew with stale token rejected.
	if _, err := s.RenewAt(l1.ID, 999, time.Minute, base.Add(time.Second)); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("stale renew must fail, got %v", err)
	}
	// Expiry allows takeover with a higher fencing token.
	after := base.Add(11 * time.Minute)
	l2, err := s.AcquireAt("payroll", time.Minute, after)
	if err != nil {
		t.Fatalf("takeover after expiry must succeed: %v", err)
	}
	if l2.Token != 2 {
		t.Fatalf("takeover token must fence to 2, got %d", l2.Token)
	}
	// Old lease is now stale even with its old token.
	if _, err := s.ExecuteUnderLeaseAt(l1.ID, l1.Token, after.Add(time.Second)); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("superseded lease must be stale, got %v", err)
	}
	// Expired lease cannot execute.
	l3, err := s.AcquireAt("solo", time.Minute, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecuteUnderLeaseAt(l3.ID, l3.Token, base.Add(2*time.Minute)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired execute must fail, got %v", err)
	}
	if _, err := s.RenewAt(l3.ID, l3.Token, time.Minute, base.Add(2*time.Minute)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired renew must fail, got %v", err)
	}
	if _, err := s.ExecuteUnderLeaseAt("missing", 1, base); !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("unknown lease must 404, got %v", err)
	}
}

func TestOverdraftCoordinator(t *testing.T) {
	s := testStore()
	decisions, err := s.Decide(1000, []Claim{
		{ID: "c-fee", Channel: ChannelFee, Amount: 100},
		{ID: "c-card", Channel: ChannelCard, Amount: 600},
		{ID: "c-dd", Channel: ChannelDirectDebit, Amount: 500},
		{ID: "c-atm", Channel: ChannelATM, Amount: 300},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Priority order: card, atm, direct_debit, fee.
	if len(decisions) != 4 {
		t.Fatalf("want 4 decisions, got %+v", decisions)
	}
	byID := map[string]Decision{}
	for _, d := range decisions {
		byID[d.ClaimID] = d
	}
	// 1000 funds: card 600 APPROVE (400 left), atm 300 APPROVE (100 left),
	// direct_debit 500 cannot fit -> QUEUE, fee 100 APPROVE... but fee is last
	// and only 100 remains, so fee APPROVEs. Check ordering effect.
	if byID["c-card"].Verdict != VerdictApprove {
		t.Fatalf("card must approve: %+v", byID["c-card"])
	}
	if byID["c-atm"].Verdict != VerdictApprove {
		t.Fatalf("atm must approve: %+v", byID["c-atm"])
	}
	if byID["c-dd"].Verdict != VerdictQueue {
		t.Fatalf("short direct_debit must queue: %+v", byID["c-dd"])
	}
	if byID["c-fee"].Verdict != VerdictApprove {
		t.Fatalf("fee must approve with remainder: %+v", byID["c-fee"])
	}

	// Immediate channels decline when broke; deferrable queue.
	broke, err := s.Decide(50, []Claim{
		{ID: "q1", Channel: ChannelTransfer, Amount: 500},
		{ID: "d1", Channel: ChannelCard, Amount: 500},
		{ID: "d2", Channel: ChannelFee, Amount: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, d := range broke {
		m[d.ClaimID] = d.Verdict
	}
	if m["q1"] != VerdictQueue || m["d1"] != VerdictDecline || m["d2"] != VerdictDecline {
		t.Fatalf("broke verdicts wrong: %v", m)
	}

	if _, err := s.Decide(100, []Claim{{ID: "x", Channel: "nope", Amount: 10}}); err == nil {
		t.Fatal("unknown channel must fail")
	}
	if _, err := s.Decide(-1, []Claim{{ID: "x", Channel: ChannelCard, Amount: 10}}); err == nil {
		t.Fatal("negative liquidity must fail")
	}
}
