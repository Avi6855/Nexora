package handover

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestHappyPathHandover(t *testing.T) {
	work := NewCounterWorkTracker()
	c := NewCoordinator("primary-aws", 41, work)

	if c.State() != StatePrimary || c.Epoch() != 41 {
		t.Fatalf("initial state %s epoch %d", c.State(), c.Epoch())
	}
	if err := c.BeginDrain(); err != nil {
		t.Fatalf("drain: %v", err)
	}
	// Freeze must refuse while work is in flight.
	work.Begin()
	if err := c.Freeze(); !errors.Is(err, ErrNotDrained) {
		t.Fatalf("want ErrNotDrained, got %v", err)
	}
	work.Done()
	if err := c.Freeze(); err != nil {
		t.Fatalf("freeze after drain: %v", err)
	}
	if err := c.AddCapture("ledger-snapshot", "snap-123"); err != nil {
		t.Fatalf("capture: %v", err)
	}
	tok, err := c.CompleteHandover("tok-1", "ops")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if tok.FromEpoch != 41 || tok.ToEpoch != 42 {
		t.Fatalf("token epochs = %d→%d, want 41→42", tok.FromEpoch, tok.ToEpoch)
	}
	if c.State() != StateHandedOver || !c.State().Terminal() {
		t.Fatalf("final state = %s", c.State())
	}
	// The old primary self-fenced: its epoch advanced to 42, so writes it
	// stamps at 42 would actually be accepted by a store that also saw 42 —
	// but it can no longer claim 41, which is what matters against a store
	// already told 42 by the stand-in. (See fencing tests below.)
	if c.Epoch() != 42 {
		t.Fatalf("old primary must self-fence to %d, got %d", 42, c.Epoch())
	}
}

func TestInvalidTransitions(t *testing.T) {
	c := NewCoordinator("p", 1, nil)
	if err := c.Freeze(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("freeze from PRIMARY must fail, got %v", err)
	}
	if err := c.BeginDrain(); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := c.BeginDrain(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("double drain must fail, got %v", err)
	}
	if _, err := c.CompleteHandover("t", "ops"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("handover before freeze must fail, got %v", err)
	}
}

func TestHandoverRefusedWithoutCaptures(t *testing.T) {
	c := NewCoordinator("p", 7, nil)
	_ = c.BeginDrain()
	_ = c.Freeze()
	if _, err := c.CompleteHandover("t", "ops"); !errors.Is(err, ErrCaptureIncomplete) {
		t.Fatalf("want ErrCaptureIncomplete, got %v", err)
	}
}

func TestEpochGuardFencesStaleWriters(t *testing.T) {
	g := NewEpochGuard("shared-store", 41)

	// Stand-in writes at epoch 42 → accepted, guard advances.
	if err := g.Check(42); err != nil {
		t.Fatalf("stand-in write must be accepted: %v", err)
	}
	// Partitioned old primary still believes it is epoch 41 → fenced.
	if err := g.Check(41); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale writer must be fenced, got %v", err)
	}
	// Equal epoch → accepted (idempotent re-write within same authority).
	if err := g.Check(42); err != nil {
		t.Fatalf("same-epoch write must be accepted: %v", err)
	}
	if g.Current() != 42 {
		t.Fatalf("guard epoch = %d, want 42", g.Current())
	}
}

func TestEpochGuardAdoptsFutureEpoch(t *testing.T) {
	g := NewEpochGuard("store", 5)
	if err := g.Check(9); err != nil {
		t.Fatalf("future epoch must be adopted: %v", err)
	}
	if g.Current() != 9 {
		t.Fatalf("guard must adopt epoch 9, got %d", g.Current())
	}
	// And immediately fence the intermediate epochs.
	if err := g.Check(6); !errors.Is(err, ErrFenced) {
		t.Fatal("intermediate epochs must be fenced after adoption")
	}
}

func TestStandInRefusesIncompleteToken(t *testing.T) {
	s := NewStandInActivator("standin-gcp")
	incomplete := &HandoverToken{
		TokenID:   "t-bad",
		FromEpoch: 41, ToEpoch: 42,
		Captures: []CaptureRecord{{Name: "ledger", Ref: "x", Completed: false}},
	}
	if err := s.Activate(incomplete); !errors.Is(err, ErrCaptureIncomplete) {
		t.Fatalf("want ErrCaptureIncomplete, got %v", err)
	}
	if s.IsActive() {
		t.Fatal("stand-in must not be active after refused token")
	}
	if _, err := s.WriteEpoch(); err == nil {
		t.Fatal("inactive stand-in must have no write epoch")
	}
}

func TestStandInActivationFlow(t *testing.T) {
	s := NewStandInActivator("standin-gcp")
	tok := &HandoverToken{
		TokenID:   "t-good",
		FromEpoch: 41, ToEpoch: 42,
		Captures: []CaptureRecord{
			{Name: "ledger-snapshot", Ref: "snap-1", Completed: true, At: time.Now()},
			{Name: "kafka-offsets", Ref: "off-1", Completed: true, At: time.Now()},
		},
	}
	if !tok.Complete() {
		t.Fatal("token must verify complete")
	}
	if err := s.Activate(tok); err != nil {
		t.Fatalf("activate: %v", err)
	}
	epoch, err := s.WriteEpoch()
	if err != nil || epoch != 42 {
		t.Fatalf("write epoch = %d, %v; want 42", epoch, err)
	}
	// Double activation refused.
	if err := s.Activate(tok); err == nil {
		t.Fatal("double activation must be refused")
	}
}

func TestFencingTokenFormat(t *testing.T) {
	g := NewEpochGuard("payments", 41)
	tok := g.FencingToken()
	if tok != "epoch=41;platform=payments" {
		t.Fatalf("fencing token = %q", tok)
	}
}

func TestConcurrentFencingChecks(t *testing.T) {
	g := NewEpochGuard("store", 1)
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := g.Check(2); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent same-epoch checks must all pass: %v", e)
	}
	if g.Current() != 2 {
		t.Fatalf("epoch = %d, want 2", g.Current())
	}
}
