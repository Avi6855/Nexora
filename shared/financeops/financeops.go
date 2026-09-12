// Package financeops implements Nexora's finance-operations correctness
// primitives as a tested shared library, wired into ledger-service as
// internal/financeops.
//
//  13. Adjustment workflow: immutable originals, approved compensating
//     entries with balance verification.
//  14. Backdated events: effective/recorded/processed triple with as-of
//     balance recomputation.
//  15. Close control: checklist-gated CLOSE/BLOCK.
//  16. Period locking: locked periods redirect corrections forward.
//  17. Posting rules registry: versioned type -> legs map with rollback.
//  18. Sub-ledgers: isolated per-domain ledgers with consolidation.
package financeops

import (
	"errors"
	"sync"

	"github.com/rs/zerolog"
)

var (
	// ErrEntryNotFound maps to HTTP 404.
	ErrEntryNotFound = errors.New("entry not found")
	// ErrAdjustmentNotFound maps to HTTP 404.
	ErrAdjustmentNotFound = errors.New("adjustment not found")
	// ErrAdjustmentState maps to HTTP 409 (already approved).
	ErrAdjustmentState = errors.New("adjustment already approved")
	// ErrPeriodLocked maps to HTTP 409.
	ErrPeriodLocked = errors.New("period is locked")
	// ErrRuleNotFound maps to HTTP 404.
	ErrRuleNotFound = errors.New("posting rule not found")
	// ErrRuleVersionNotFound maps to HTTP 404.
	ErrRuleVersionNotFound = errors.New("posting rule version not found")
	// ErrNoPriorVersion maps to HTTP 409 (nothing to roll back to).
	ErrNoPriorVersion = errors.New("no prior rule version to roll back to")
)

// Store is the in-memory finance-ops state. A single mutex guards every map
// so all mutations are atomic.
type Store struct {
	mu          sync.Mutex
	logger      zerolog.Logger
	entries     map[string]*Entry
	adjustments map[string]*Adjustment
	backdated   []*BackdatedEvent
	periods     map[string]bool
	rules       map[string][]PostingRule
	subledgers  map[string][]SubledgerEntry
}

// NewStore returns an empty store. Mutations emit zerolog Info logs.
func NewStore(logger zerolog.Logger) *Store {
	return &Store{
		logger:      logger,
		entries:     make(map[string]*Entry),
		adjustments: make(map[string]*Adjustment),
		periods:     make(map[string]bool),
		rules:       make(map[string][]PostingRule),
		subledgers:  make(map[string][]SubledgerEntry),
	}
}
