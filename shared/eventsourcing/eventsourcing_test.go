package eventsourcing

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func mustAppend(t *testing.T, s *Store, agg string, seq int, typ string, payload string, at time.Time) {
	t.Helper()
	if _, err := s.Append(agg, seq, typ, json.RawMessage(payload), at); err != nil {
		t.Fatalf("Append %s seq %d: %v", typ, seq, err)
	}
}

func TestConcurrencyConflict(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	mustAppend(t, s, "cust-1", 0, EventCustomerCreated, `{"initial_balance":10000}`, base)
	mustAppend(t, s, "cust-1", 1, EventPaymentMade, `{"amount":1000}`, base.Add(time.Minute))
	if _, err := s.Append("cust-1", 1, EventPaymentMade, json.RawMessage(`{"amount":500}`), base.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale expected_seq = %v, want ErrConflict", err)
	}
	stream, err := s.Load("cust-1")
	if err != nil || len(stream) != 2 || stream[1].Seq != 2 {
		t.Fatalf("stream = %+v %v", stream, err)
	}
	if _, err := s.Load("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing load = %v, want ErrNotFound", err)
	}
}

func TestRebuildToSeq(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	mustAppend(t, s, "cust-2", 0, EventCustomerCreated, `{"initial_balance":10000}`, base)
	mustAppend(t, s, "cust-2", 1, EventCardIssued, `{"card_id":"card-1","last4":"1234"}`, base.Add(time.Minute))
	mustAppend(t, s, "cust-2", 2, EventPotCreated, `{"pot_id":"holiday","name":"Holiday"}`, base.Add(2*time.Minute))
	mustAppend(t, s, "cust-2", 3, EventMoneyTransferred, `{"from_pot":"","to_pot":"holiday","amount":2000}`, base.Add(3*time.Minute))
	mustAppend(t, s, "cust-2", 4, EventPaymentMade, `{"amount":500}`, base.Add(4*time.Minute))

	at2, err := s.Project("cust-2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if at2.Balance != 10000 || len(at2.Cards) != 1 || len(at2.Pots) != 0 {
		t.Fatalf("seq2 = %+v", at2)
	}
	full, err := s.Project("cust-2", 0)
	if err != nil {
		t.Fatal(err)
	}
	if full.Balance != 10000-2000-500 {
		t.Fatalf("balance = %d, want 7500", full.Balance)
	}
	if full.Pots["holiday"] != 2000 {
		t.Fatalf("pot = %+v", full.Pots)
	}
	snap, err := s.Snapshot("cust-2", 4)
	if err != nil || snap.CursorSeq != 4 {
		t.Fatalf("snapshot = %+v %v", snap, err)
	}
	if err := s.RegisterProjector("balance", []string{EventPaymentMade}, applyBalance); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate projector = %v, want ErrConflict", err)
	}
}

func TestBlockedAccountGating(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	mustAppend(t, s, "cust-3", 0, EventCustomerCreated, `{"initial_balance":5000}`, base)
	mustAppend(t, s, "cust-3", 1, EventAccountBlocked, `{}`, base.Add(time.Minute))
	mustAppend(t, s, "cust-3", 2, EventPaymentMade, `{"amount":100}`, base.Add(2*time.Minute))
	if _, err := s.Project("cust-3", 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked payment = %v, want ErrBlocked", err)
	}
	mustAppend(t, s, "cust-3", 3, EventAccountUnblocked, `{}`, base.Add(3*time.Minute))
	mustAppend(t, s, "cust-3", 4, EventRefundReceived, `{"amount":200}`, base.Add(4*time.Minute))
	// Replay only to the unblock still fails (blocked payment at seq 2 folds
	// before the unblock), but a stream that unblocks first then pays works.
	state, err := s.Project("cust-3", 2)
	if err != nil || !state.Blocked {
		t.Fatalf("blocked state = %+v %v", state, err)
	}
	s2 := NewStore()
	mustAppend(t, s2, "cust-4", 0, EventCustomerCreated, `{"initial_balance":5000}`, base)
	mustAppend(t, s2, "cust-4", 1, EventAccountBlocked, `{}`, base.Add(time.Minute))
	mustAppend(t, s2, "cust-4", 2, EventAccountUnblocked, `{}`, base.Add(2*time.Minute))
	mustAppend(t, s2, "cust-4", 3, EventPaymentMade, `{"amount":100}`, base.Add(3*time.Minute))
	ok, err := s2.Project("cust-4", 0)
	if err != nil || ok.Balance != 4900 || ok.Blocked {
		t.Fatalf("unblocked payment = %+v %v", ok, err)
	}
}

func TestDebugReplay(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	mustAppend(t, s, "cust-9", 0, EventCustomerCreated, `{"initial_balance":10000}`, base)
	mustAppend(t, s, "cust-9", 1, EventPaymentMade, `{"amount":1000}`, base.Add(time.Hour))
	mustAppend(t, s, "cust-9", 2, EventPaymentMade, `{"amount":2000}`, base.Add(2*time.Hour))
	rules := map[string]string{"pricing": "v1", "fraud": "v2"}

	// Exact-timestamp cutoff: pin exactly at the second event's instant.
	cutoff := base.Add(time.Hour)
	res, err := s.ReplayAt("cust-9", cutoff, rules)
	if err != nil {
		t.Fatal(err)
	}
	if res.EventsReplayed != 2 || res.CursorSeq != 2 {
		t.Fatalf("replay = %+v, want 2 events cursor 2", res)
	}
	if res.State.Balance != 9000 {
		t.Fatalf("balance = %d, want 9000", res.State.Balance)
	}
	// Just before the second event: only the first folds.
	res2, err := s.ReplayAt("cust-9", cutoff.Add(-time.Nanosecond), rules)
	if err != nil {
		t.Fatal(err)
	}
	if res2.EventsReplayed != 1 || res2.State.Balance != 10000 {
		t.Fatalf("cutoff-1ns = %+v", res2)
	}
	// Unknown rule version errors.
	if _, err := s.ReplayAt("cust-9", cutoff, map[string]string{"pricing": "v9"}); !errors.Is(err, ErrUnknownRule) {
		t.Fatalf("bad version = %v, want ErrUnknownRule", err)
	}
	if _, err := s.ReplayAt("cust-9", cutoff, map[string]string{"nope": "v1"}); !errors.Is(err, ErrUnknownRule) {
		t.Fatalf("bad rule = %v, want ErrUnknownRule", err)
	}
	// Rules-hash stability: same input hashes identically, different pins differ.
	a, _ := s.ReplayAt("cust-9", cutoff, rules)
	b, _ := s.ReplayAt("cust-9", cutoff.Add(time.Hour), rules)
	if a.RulesHash == "" || a.RulesHash != b.RulesHash {
		t.Fatalf("hash unstable: %q vs %q", a.RulesHash, b.RulesHash)
	}
	c, _ := s.ReplayAt("cust-9", cutoff, map[string]string{"pricing": "v2", "fraud": "v2"})
	if c.RulesHash == a.RulesHash {
		t.Fatal("different rule pins must hash differently")
	}
}
