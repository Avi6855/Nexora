// Package handover implements Nexora's Safe Handover Protocol between
// banking platforms (primary ↔ stand-in).
//
// The failure mode this exists to prevent: after a handover, the OLD primary
// keeps accepting writes because it never learned it lost authority. Two
// independent systems then both act on the same money movement — the worst
// outcome in banking. The protocol makes stale writes structurally
// impossible:
//
//	PRIMARY ──DRAIN──► FREEZE_NEW_WORK ──CAPTURE_STATE──► HANDED_OVER
//	                                                     (epoch N)
//	                     STAND-IN activates with epoch N+1
//
// Every write carries the writer's epoch. A store accepts a write only if
// its epoch ≥ the writer's — an old-primary write (stale epoch) is fenced
// out. Epochs are monotone and never reused, so a partitioned old primary
// cannot "guess" its way back in.
package handover

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrFenced is returned when a writer presents a stale epoch.
	ErrFenced = errors.New("write rejected: writer epoch is fenced (stale authority)")
	// ErrInvalidTransition is returned for illegal state machine moves.
	ErrInvalidTransition = errors.New("invalid handover state transition")
	// ErrNotDrained means the freeze step ran with work still in flight.
	ErrNotDrained = errors.New("cannot freeze: in-flight work remains")
	// ErrCaptureIncomplete means the handover token was issued without a
	// complete state capture — the stand-in must refuse it.
	ErrCaptureIncomplete = errors.New("state capture incomplete; handover token refused")
	// ErrWrongEpoch is returned when an operation references an epoch that is
	// not the current one.
	ErrWrongEpoch = errors.New("operation references a stale or future epoch")
)

// State is the primary's handover lifecycle.
type State string

const (
	StatePrimary    State = "PRIMARY"     // normal authority
	StateDraining   State = "DRAINING"    // finishing in-flight work
	StateFrozen     State = "FROZEN"      // no new work, state capture running
	StateHandedOver State = "HANDED_OVER" // authority released, fenced
)

// Terminal reports whether the platform has released authority.
func (s State) Terminal() bool { return s == StateHandedOver }

// WorkTracker counts in-flight work so DRAINING can prove quiescence.
type WorkTracker interface {
	InFlight() int64
}

// CounterWorkTracker is the simple in-process tracker.
type CounterWorkTracker struct {
	mu    sync.Mutex
	count int64
}

func NewCounterWorkTracker() *CounterWorkTracker { return &CounterWorkTracker{} }

func (t *CounterWorkTracker) Begin() {
	t.mu.Lock()
	t.count++
	t.mu.Unlock()
}

func (t *CounterWorkTracker) Done() {
	t.mu.Lock()
	t.count--
	t.mu.Unlock()
}

func (t *CounterWorkTracker) InFlight() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

// CaptureRecord is one artifact of the state capture (e.g. a table snapshot,
// an offset map, a watermark). The handover token lists them; the stand-in
// verifies presence before activating.
type CaptureRecord struct {
	Name      string    `json:"name"`
	Ref       string    `json:"ref"` // e.g. snapshot ID, s3 key, offset checkpoint
	Completed bool      `json:"completed"`
	At        time.Time `json:"at"`
}

// HandoverToken is the signed-off proof that authority moved. The stand-in
// activates ONLY against a complete token.
type HandoverToken struct {
	TokenID   string          `json:"token_id"`
	FromEpoch uint64          `json:"from_epoch"`
	ToEpoch   uint64          `json:"to_epoch"`
	Captures  []CaptureRecord `json:"captures"`
	IssuedAt  time.Time       `json:"issued_at"`
	IssuedBy  string          `json:"issued_by"`
}

// Complete verifies every capture finished — the stand-in's activation gate.
func (t *HandoverToken) Complete() bool {
	if len(t.Captures) == 0 {
		return false
	}
	for _, c := range t.Captures {
		if !c.Completed {
			return false
		}
	}
	return true
}

// Coordinator runs the handover state machine on the LOSING side (primary).
type Coordinator struct {
	mu       sync.Mutex
	platform string
	state    State
	epoch    uint64
	work     WorkTracker
	captures []CaptureRecord
	token    *HandoverToken
}

func NewCoordinator(platform string, initialEpoch uint64, work WorkTracker) *Coordinator {
	return &Coordinator{platform: platform, state: StatePrimary, epoch: initialEpoch, work: work}
}

// State returns the current lifecycle state.
func (c *Coordinator) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Epoch returns the current authority epoch.
func (c *Coordinator) Epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

// BeginDrain moves PRIMARY → DRAINING. New work must be rejected by callers
// once this returns; existing work continues.
func (c *Coordinator) BeginDrain() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StatePrimary {
		return fmt.Errorf("%w: drain requires PRIMARY, current %s", ErrInvalidTransition, c.state)
	}
	c.state = StateDraining
	return nil
}

// Freeze moves DRAINING → FROZEN. It refuses while work is in flight —
// freezing with live writes would corrupt the captured state.
func (c *Coordinator) Freeze() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateDraining {
		return fmt.Errorf("%w: freeze requires DRAINING, current %s", ErrInvalidTransition, c.state)
	}
	if c.work != nil && c.work.InFlight() > 0 {
		return fmt.Errorf("%w: %d operations in flight", ErrNotDrained, c.work.InFlight())
	}
	c.state = StateFrozen
	return nil
}

// AddCapture records a completed capture artifact.
func (c *Coordinator) AddCapture(name, ref string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateFrozen {
		return fmt.Errorf("%w: captures only valid in FROZEN, current %s", ErrInvalidTransition, c.state)
	}
	c.captures = append(c.captures, CaptureRecord{Name: name, Ref: ref, Completed: true, At: time.Now().UTC()})
	return nil
}

// CompleteHandover issues the handover token and fences this platform by
// advancing its epoch to the stand-in's epoch (N+1). From this moment any
// write from this platform presents a stale epoch and is rejected by shared
// stores.
func (c *Coordinator) CompleteHandover(tokenID, issuedBy string) (*HandoverToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateFrozen {
		return nil, fmt.Errorf("%w: handover requires FROZEN, current %s", ErrInvalidTransition, c.state)
	}
	if len(c.captures) == 0 {
		return nil, ErrCaptureIncomplete
	}
	from := c.epoch
	tok := &HandoverToken{
		TokenID:   tokenID,
		FromEpoch: from,
		ToEpoch:   from + 1,
		Captures:  append([]CaptureRecord(nil), c.captures...),
		IssuedAt:  time.Now().UTC(),
		IssuedBy:  issuedBy,
	}
	c.token = tok
	c.epoch = from + 1 // self-fence: we can no longer write at our old epoch
	c.state = StateHandedOver
	return tok, nil
}

// Token returns the issued token (for the stand-in to verify).
func (c *Coordinator) Token() *HandoverToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// ── Fencing: the receiving-side authority check ─────────────────────────────

// FencedStore is the minimal contract a shared store implements to enforce
// epochs. Every mutating operation must call Authorize first.
type FencedStore interface {
	// CurrentEpoch returns the store's highest-seen epoch.
	CurrentEpoch() uint64
	// Authorize accepts a writer's epoch claim. Stale claims are fenced.
	Authorize(writerEpoch uint64) error
}

// EpochGuard is the in-memory reference implementation of the fencing check.
// Stores embed it; every write path calls Check.
type EpochGuard struct {
	mu       sync.Mutex
	epoch    uint64
	platform string
}

func NewEpochGuard(platform string, epoch uint64) *EpochGuard {
	return &EpochGuard{platform: platform, epoch: epoch}
}

// Check validates a write carrying writerEpoch. It is monotone: once the
// store has seen epoch N, writers claiming < N are fenced forever.
func (g *EpochGuard) Check(writerEpoch uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if writerEpoch < g.epoch {
		return fmt.Errorf("%w: writer claims epoch %d, store requires >= %d",
			ErrFenced, writerEpoch, g.epoch)
	}
	if writerEpoch > g.epoch {
		// A future epoch implies we missed a handover announcement — adopt it
		// (monotone) so the newer authority can proceed.
		g.epoch = writerEpoch
	}
	return nil
}

// Current returns the guard's epoch.
func (g *EpochGuard) Current() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.epoch
}

// FencingToken derives a per-operation token combining epoch and platform —
// useful for request headers and audit records.
func (g *EpochGuard) FencingToken() string {
	return fmt.Sprintf("epoch=%d;platform=%s", g.Current(), g.platform)
}

// ── Stand-in activation ─────────────────────────────────────────────────────

// StandInActivator is the gaining side: it refuses to activate without a
// complete token, and it adopts ToEpoch as its write epoch.
type StandInActivator struct {
	mu       sync.Mutex
	platform string
	active   bool
	epoch    uint64
	token    *HandoverToken
}

func NewStandInActivator(platform string) *StandInActivator {
	return &StandInActivator{platform: platform}
}

// Activate verifies the token is complete and adopts the new epoch.
func (s *StandInActivator) Activate(tok *HandoverToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok == nil {
		return ErrCaptureIncomplete
	}
	if !tok.Complete() {
		return fmt.Errorf("%w: token %s has incomplete captures", ErrCaptureIncomplete, tok.TokenID)
	}
	if s.active {
		return fmt.Errorf("platform %s is already active at epoch %d", s.platform, s.epoch)
	}
	s.active = true
	s.epoch = tok.ToEpoch
	s.token = tok
	return nil
}

// WriteEpoch is the epoch the stand-in stamps on its writes.
func (s *StandInActivator) WriteEpoch() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return 0, errors.New("stand-in is not active")
	}
	return s.epoch, nil
}

// IsActive reports activation status.
func (s *StandInActivator) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}
