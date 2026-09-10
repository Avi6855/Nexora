// PIN try counter integrity service.
//
// The PIN try counter is security-critical state: it decides when a card
// locks. Distributed authorization requests for the same card can race —
// three terminals presenting PINs at once must produce EXACTLY three
// attempts, never fewer (an attacker wins) and never more (a customer is
// locked out by phantom attempts). Correctness comes from compare-and-swap
// on a monotonically increasing version — the same discipline Cassandra LWTs
// (IF version = ?) give us in production.
package cardnet

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrPINLocked is returned once the counter reaches the limit.
	ErrPINLocked = errors.New("PIN try counter exhausted: card locked")
	// ErrCASConflict means another writer advanced the version first; the
	// caller re-reads and retries with the fresh version.
	ErrCASConflict = errors.New("compare-and-swap conflict on PIN counter version")
)

// PINCounterState is the atomically-updated counter record.
type PINCounterState struct {
	CardRef  string `json:"card_ref"`
	Attempts int    `json:"attempts"`
	MaxTries int    `json:"max_tries"`
	Locked   bool   `json:"locked"`
	// Version increases on every successful CAS. Writers must present the
	// version they read; a stale version loses the race.
	Version int64 `json:"version"`
}

// PINStore is the atomic-storage contract (a Cassandra table with
// `UPDATE ... SET attempts=?, version=? WHERE card=? IF version=?`).
type PINStore interface {
	Load(cardRef string) (PINCounterState, error)
	// CompareAndSwap applies next only if stored.Version == expected.
	CompareAndSwap(cardRef string, expected int64, next PINCounterState) error
}

// MemoryPINStore is the reference implementation; the CAS is a single locked
// section, mirroring an LWT's linearizable point.
type MemoryPINStore struct {
	mu     sync.Mutex
	states map[string]PINCounterState
}

func NewMemoryPINStore() *MemoryPINStore {
	return &MemoryPINStore{states: map[string]PINCounterState{}}
}

func (s *MemoryPINStore) Load(cardRef string) (PINCounterState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[cardRef]
	if !ok {
		return PINCounterState{CardRef: cardRef, MaxTries: 3}, nil
	}
	return st, nil
}

func (s *MemoryPINStore) CompareAndSwap(cardRef string, expected int64, next PINCounterState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.states[cardRef]
	if ok && cur.Version != expected {
		return ErrCASConflict
	}
	if !ok && expected != 0 {
		return ErrCASConflict
	}
	s.states[cardRef] = next
	return nil
}

// PINTryService processes PIN attempts against the CAS store.
type PINTryService struct {
	store    PINStore
	maxTries int
}

func NewPINTryService(store PINStore, maxTries int) *PINTryService {
	if maxTries <= 0 {
		maxTries = 3
	}
	return &PINTryService{store: store, maxTries: maxTries}
}

// Attempt records one PIN verification try. Returns the remaining attempts
// or ErrPINLocked. Under concurrency, exactly maxTries attempts succeed
// before locking — the invariant the integrity service exists to guarantee.
func (s *PINTryService) Attempt(cardRef string) (remaining int, err error) {
	const maxCASRetries = 5
	for i := 0; i < maxCASRetries; i++ {
		cur, err := s.store.Load(cardRef)
		if err != nil {
			return 0, err
		}
		if cur.Locked {
			return 0, ErrPINLocked
		}
		attempts := cur.Attempts + 1
		if attempts > s.maxTries {
			// Already at limit (should have been locked) — lock now.
			next := cur
			next.Attempts = s.maxTries
			next.Locked = true
			next.Version++
			if err := s.store.CompareAndSwap(cardRef, cur.Version, next); err != nil {
				continue // lost race; retry with fresh state
			}
			return 0, ErrPINLocked
		}
		next := PINCounterState{
			CardRef:  cardRef,
			Attempts: attempts,
			MaxTries: s.maxTries,
			Locked:   attempts == s.maxTries,
			Version:  cur.Version + 1,
		}
		if err := s.store.CompareAndSwap(cardRef, cur.Version, next); err != nil {
			continue // lost race; retry with fresh state
		}
		if next.Locked {
			return 0, ErrPINLocked
		}
		return s.maxTries - attempts, nil
	}
	return 0, fmt.Errorf("PIN counter CAS did not converge after %d retries", maxCASRetries)
}

// Reset clears the counter after a successful PIN (version-checked).
func (s *PINTryService) Reset(cardRef string) error {
	const maxCASRetries = 5
	for i := 0; i < maxCASRetries; i++ {
		cur, err := s.store.Load(cardRef)
		if err != nil {
			return err
		}
		next := PINCounterState{CardRef: cardRef, MaxTries: s.maxTries, Version: cur.Version + 1}
		if err := s.store.CompareAndSwap(cardRef, cur.Version, next); err != nil {
			continue
		}
		return nil
	}
	return ErrCASConflict
}

// State exposes the counter for observability.
func (s *PINTryService) State(cardRef string) (PINCounterState, error) {
	return s.store.Load(cardRef)
}
