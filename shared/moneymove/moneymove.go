// Package moneymove implements Nexora's money-correctness primitives as a
// tested shared library, wired into payment-service as internal/moneymove.
//
//  1. Intent ledger: customer statement -> execution plan -> actual legs,
//     flagging DIVERGED when legs differ from the plan.
//  2. Instruction versioning: monotonic v1..vN with optimistic concurrency.
//  3. Precondition engine: formal P(payment,state,time) checks; never
//     executes on FAIL.
//  4. Atomic reservation: single-mutex reserve/release/consume.
//  5. Leases: fencing tokens with renew, stale rejection and expiry takeover.
//  6. Overdraft coordinator: priority-ordered APPROVE/DECLINE/QUEUE.
package moneymove

import (
	"errors"
	"sync"

	"github.com/rs/zerolog"
)

var (
	// ErrIntentNotFound maps to HTTP 404.
	ErrIntentNotFound = errors.New("intent not found")
	// ErrInstructionNotFound maps to HTTP 404.
	ErrInstructionNotFound = errors.New("instruction not found")
	// ErrVersionConflict maps to HTTP 409 (optimistic-concurrency mismatch).
	ErrVersionConflict = errors.New("version conflict: expected_version does not match current version")
	// ErrReservationNotFound maps to HTTP 404.
	ErrReservationNotFound = errors.New("reservation not found")
	// ErrReservationNotActive maps to HTTP 409.
	ErrReservationNotActive = errors.New("reservation is not active")
	// ErrInsufficientFunds maps to HTTP 409.
	ErrInsufficientFunds = errors.New("insufficient available funds")
	// ErrLeaseNotFound maps to HTTP 404.
	ErrLeaseNotFound = errors.New("lease not found")
	// ErrLeaseHeld maps to HTTP 409 (resource already leased).
	ErrLeaseHeld = errors.New("resource is already leased")
	// ErrStaleToken maps to HTTP 409 (fencing token is stale).
	ErrStaleToken = errors.New("stale fencing token")
	// ErrLeaseExpired maps to HTTP 409 (lease window elapsed).
	ErrLeaseExpired = errors.New("lease expired")
)

// Store is the in-memory money-movement state. A single mutex guards every
// map so reserve/release/consume and all other mutations are atomic.
type Store struct {
	mu           sync.Mutex
	logger       zerolog.Logger
	intents      map[string]*IntentRecord
	instructions map[string]*Instruction
	histories    map[string][]Instruction
	balances     map[string]int64
	reservations map[string]*Reservation
	leases       map[string]*Lease
	resourceHeld map[string]string
	fencing      map[string]uint64
}

// NewStore returns an empty store. Mutations emit zerolog Info logs.
func NewStore(logger zerolog.Logger) *Store {
	return &Store{
		logger:       logger,
		intents:      make(map[string]*IntentRecord),
		instructions: make(map[string]*Instruction),
		histories:    make(map[string][]Instruction),
		balances:     make(map[string]int64),
		reservations: make(map[string]*Reservation),
		leases:       make(map[string]*Lease),
		resourceHeld: make(map[string]string),
		fencing:      make(map[string]uint64),
	}
}
