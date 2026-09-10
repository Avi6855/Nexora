package cards

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func TestMerchantLock(t *testing.T) {
	a := &LockedAuthorizer{MerchantGroups: map[string]string{
		"MERCH-NFLX-INTL":            "NETFLIX",
		"NETFLIX INTERNATIONAL B.V.": "NETFLIX",
		"MERCH-AMZN":                 "AMAZON",
	}}
	card := VirtualCard{ID: "v-1", LinkedCardID: "p-1", LockedGroup: "NETFLIX", Active: true}

	// Acquirer-presented odd merchant ID resolves via graph.
	if err := a.Authorize(card, MerchantIdentity{ID: "MERCH-NFLX-INTL", Name: "Netflix International B.V."}, 1549); err != nil {
		t.Fatalf("group merchant must pass: %v", err)
	}
	// Amazon declines with a clear reason.
	err := a.Authorize(card, MerchantIdentity{ID: "MERCH-AMZN", Name: "Amazon UK"}, 2999)
	if !errors.Is(err, ErrMerchantLocked) {
		t.Fatalf("wrong merchant must decline: %v", err)
	}
	// Stolen details used at an unknown merchant decline too.
	if err := a.Authorize(card, MerchantIdentity{Name: "Sketchy Shop"}, 50000); err == nil {
		t.Fatal("unknown merchant must not pass a locked card")
	}
	// Unlocked card pays anywhere.
	unlocked := VirtualCard{ID: "v-2", Active: true}
	if err := a.Authorize(unlocked, MerchantIdentity{Name: "Anywhere"}, 100); err != nil {
		t.Fatalf("unlocked card: %v", err)
	}
	if err := a.Authorize(VirtualCard{ID: "v-3", LockedGroup: "NETFLIX"}, MerchantIdentity{ID: "MERCH-NFLX-INTL"}, 100); !errors.Is(err, ErrCardNotActive) {
		t.Fatal("inactive card must decline")
	}
}

func TestProgrammableRules(t *testing.T) {
	e := NewRuleEngine()
	e.SetRules("c1", []AuthRule{
		{Priority: 10, Action: ActionBlock, BlockGambling: true},
		{Priority: 20, Action: ActionBlock, BlockATM: true},
		{Priority: 30, Action: ActionAllow, Countries: []string{"UK"}, Categories: []string{"TRANSPORT"},
			DailyCapMinor: 10000, TimeFrom: "09:00", TimeTo: "18:00"},
	})

	// Gambling blocked regardless of anything else.
	act, why, _ := e.Evaluate("c1", AuthAttempt{At: t0, Country: "UK", Category: "GAMBLING", AmountMinor: 500})
	if act != ActionBlock || why == "" {
		t.Fatalf("gambling must block: %s %s", act, why)
	}
	// ATM blocked.
	act, _, _ = e.Evaluate("c1", AuthAttempt{At: t0, Country: "UK", Category: "ATM", ATM: true, AmountMinor: 2000})
	if act != ActionBlock {
		t.Fatalf("ATM must block: %s", act)
	}
	// Transport inside window passes.
	act, _, err := e.Evaluate("c1", AuthAttempt{At: t0, Country: "UK", Category: "TRANSPORT", AmountMinor: 300})
	if err != nil || act != ActionAllow {
		t.Fatalf("transport must allow: %s %v", act, err)
	}
	// Outside the time window, no rule matches → default allow.
	act, why, _ = e.Evaluate("c1", AuthAttempt{At: t0.Add(10 * time.Hour), Country: "UK", Category: "TRANSPORT", AmountMinor: 300})
	if act != ActionAllow || why != "no rule matched; default allow" {
		t.Fatalf("outside window: %s %s", act, why)
	}
	// Daily cap accumulates across attempts and then blocks: 600+600 stays
	// within the £10.00 cap, the third 600 would breach it.
	e2 := NewRuleEngine()
	e2.SetRules("c2", []AuthRule{{Priority: 1, Action: ActionAllow, DailyCapMinor: 1000}})
	if act, _, _ = e2.Evaluate("c2", AuthAttempt{At: t0, AmountMinor: 400}); act != ActionAllow {
		t.Fatal("first spend within cap must allow")
	}
	if act, _, _ = e2.Evaluate("c2", AuthAttempt{At: t0, AmountMinor: 400}); act != ActionAllow {
		t.Fatal("second spend within cap must allow")
	}
	if act, why, _ = e2.Evaluate("c2", AuthAttempt{At: t0, AmountMinor: 400}); act != ActionBlock {
		t.Fatalf("cap must block after 800 spent: %s (%s)", act, why)
	}
	// Next day the counter resets.
	if act, _, _ = e2.Evaluate("c2", AuthAttempt{At: t0.Add(24 * time.Hour), AmountMinor: 600}); act != ActionAllow {
		t.Fatal("new day must reset cap")
	}
	// Priority order: block at priority 10 wins over allow at 20.
	e3 := NewRuleEngine()
	e3.SetRules("c3", []AuthRule{
		{Priority: 10, Action: ActionBlock, Countries: []string{"US"}},
		{Priority: 20, Action: ActionAllow, Countries: []string{"US", "UK"}},
	})
	if act, _, _ = e3.Evaluate("c3", AuthAttempt{At: t0, Country: "US"}); act != ActionBlock {
		t.Fatal("lower priority number must decide first")
	}
}

func TestCredentialContinuity(t *testing.T) {
	v := NewTokenVault()
	v.Register("tok-netflix", "card-1", "NETFLIX", t0)
	v.Register("tok-amazon", "card-1", "AMAZON", t0)

	// Card stolen → replaced. Tokens re-map; merchants keep charging tokens.
	n := v.ReplaceCard("card-1", "card-2")
	if n != 2 {
		t.Fatalf("%d tokens re-mapped, want 2", n)
	}
	cardID, err := v.ChargeThroughToken("tok-netflix")
	if err != nil || cardID != "card-2" {
		t.Fatalf("token must charge the NEW card: %s %v", cardID, err)
	}
	// Old card no longer reachable via any token.
	for _, id := range []string{"tok-netflix", "tok-amazon"} {
		if got, _ := v.ChargeThroughToken(id); got == "card-1" {
			t.Fatalf("token %s still points at the stolen card", id)
		}
	}
	// Suspend + revoke gate charges.
	_ = v.Suspend("tok-amazon")
	if _, err := v.ChargeThroughToken("tok-amazon"); err == nil {
		t.Fatal("suspended token must decline")
	}
	_ = v.Revoke("tok-netflix")
	if _, err := v.ChargeThroughToken("tok-netflix"); err == nil {
		t.Fatal("revoked token must decline")
	}
	if _, err := v.Get("nope"); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestCardLifecycleSaga(t *testing.T) {
	s := NewCardSaga("card-9", t0)
	// Walk to ACTIVATED.
	for _, want := range []Stage{StagePersonalised, StageManufactured, StageShipped, StageDelivered, StageActivated} {
		if err := s.Advance(t0); err != nil {
			t.Fatal(err)
		}
		if s.Stage != want {
			t.Fatalf("stage %s, want %s", s.Stage, want)
		}
	}
	// Narrations are customer-safe.
	if s.History[len(s.History)-1].Customer == "" {
		t.Fatal("each stage needs a customer message")
	}
	// Partner failure mid-manufacture compensates to the previous stable stage.
	s2 := NewCardSaga("card-10", t0)
	_ = s2.Advance(t0) // PERSONALISED
	_ = s2.Advance(t0) // MANUFACTURED
	fb := s2.PartnerFailure(t0, "personalisation bureau outage")
	if fb != StagePersonalised {
		t.Fatalf("fallback %s, want PERSONALISED", fb)
	}
	if s2.FailReason == "" {
		t.Fatal("failure reason must be recorded for retry")
	}
	// Failure at the FIRST stage has nothing to fall back to.
	s3 := NewCardSaga("card-11", t0)
	if fb := s3.PartnerFailure(t0, "bureau unreachable"); fb != StageFailed {
		t.Fatalf("first-stage failure must FAIL the saga, got %s", fb)
	}
	// Reactivation is event-driven (ACTIVATED→SUSPENDED→ACTIVATED), then
	// suspend → replace → expire.
	_ = s.Advance(t0) // SUSPENDED
	if err := s.Transition(StageActivated, t0); err != nil {
		t.Fatal(err)
	}
	_ = s.Advance(t0) // SUSPENDED again
	_ = s.Advance(t0) // REPLACED
	if err := s.Advance(t0); err != nil {
		t.Fatal(err)
	}
	if s.Stage != StageExpired {
		t.Fatalf("replaced cards expire, got %s", s.Stage)
	}
}

func TestDeliveryExceptions(t *testing.T) {
	// Lost courier item → new PAN, no address check.
	wf, err := ClassifyDeliveryFailure("lost")
	if err != nil {
		t.Fatal(err)
	}
	if !wf.NewCardNeeded || !wf.ReDispatch || wf.AddressCheck {
		t.Fatalf("lost card workflow wrong: %+v", wf)
	}
	if wf.CustomerAction == "" || len(wf.Automated) == 0 {
		t.Fatalf("workflow must be actionable: %+v", wf)
	}
	// Wrong address → verify then re-dispatch the SAME card.
	wf, err = ClassifyDeliveryFailure("wrong address")
	if err != nil {
		t.Fatal(err)
	}
	if wf.NewCardNeeded || !wf.AddressCheck {
		t.Fatalf("wrong-address workflow wrong: %+v", wf)
	}
	// Delayed → trace, no reissue.
	wf, _ = ClassifyDeliveryFailure("delayed")
	if wf.NewCardNeeded || wf.ReDispatch {
		t.Fatalf("delayed must not reissue: %+v", wf)
	}
	if _, err := ClassifyDeliveryFailure("mystery"); err == nil {
		t.Fatal("unknown courier reason must error")
	}
}
