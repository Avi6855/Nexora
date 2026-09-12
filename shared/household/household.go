// Package household implements bills, direct-debit mandates and account
// closure safety scans as a tested shared library.
//
// Bills: a plan catalog {provider, amount, cadence} with
// cheaper-comparable detection against market plans; actions
// cancel/switch/renew/ignore run as async tasks (PENDING → DONE).
//
// Mandates: a direct-debit detection list with merchant mapping; each
// mandate migrates via a task that requires verify-first-collection.
//
// Closure: a pre-close safety scan over {direct debits, recurring, pending
// refunds, pending transfers, subscriptions, balance} producing SAFE/UNSAFE
// plus a blockers list.
package household

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── Bills ─────────────────────────────────────────────────────────────────

const (
	BillActionCancel = "cancel"
	BillActionSwitch = "switch"
	BillActionRenew  = "renew"
	BillActionIgnore = "ignore"
)

const (
	TaskPending = "PENDING"
	TaskRunning = "RUNNING"
	TaskDone    = "DONE"
	TaskFailed  = "FAILED"
)

// Bill is one catalogued plan.
type Bill struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Amount    int64     `json:"amount_minor"`
	Cadence   string    `json:"cadence"`
	Category  string    `json:"category,omitempty"`
	Action    string    `json:"action,omitempty"`
	TaskState string    `json:"task_state,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MarketPlan is a comparable market offer.
type MarketPlan struct {
	Provider string `json:"provider"`
	Amount   int64  `json:"amount_minor"`
	Cadence  string `json:"cadence"`
}

// ── Mandates ──────────────────────────────────────────────────────────────

// Mandate is one detected direct debit.
type Mandate struct {
	ID                      string    `json:"id"`
	Merchant                string    `json:"merchant"`
	Reference               string    `json:"reference"`
	AmountMinor             int64     `json:"amount_minor,omitempty"`
	MappedMerchant          string    `json:"mapped_merchant,omitempty"`
	MigrationState          string    `json:"migration_state,omitempty"`
	MigrationTarget         string    `json:"migration_target,omitempty"`
	FirstCollectionVerified bool      `json:"first_collection_verified"`
	CreatedAt               time.Time `json:"created_at"`
}

// ── Closure ───────────────────────────────────────────────────────────────

// ClosureInput is the pre-close safety scan input.
type ClosureInput struct {
	AccountID        string `json:"account_id"`
	DirectDebits     int    `json:"direct_debits"`
	Recurring        int    `json:"recurring"`
	PendingRefunds   int    `json:"pending_refunds"`
	PendingTransfers int    `json:"pending_transfers"`
	Subscriptions    int    `json:"subscriptions"`
	BalanceMinor     int64  `json:"balance_minor"`
}

// ClosureResult is the scan verdict.
type ClosureResult struct {
	Verdict  string   `json:"verdict"` // SAFE | UNSAFE
	Blockers []string `json:"blockers"`
}

var (
	// ErrBillNotFound marks unknown bill IDs.
	ErrBillNotFound = errors.New("bill not found")
	// ErrMandateNotFound marks unknown mandate IDs.
	ErrMandateNotFound = errors.New("mandate not found")
)

// Store holds bills + mandates.
type Store struct {
	mu       sync.Mutex
	bills    map[string]*Bill
	mandates map[string]*Mandate
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{bills: make(map[string]*Bill), mandates: make(map[string]*Mandate)}
}

// AddBill catalogues a plan.
func (s *Store) AddBill(provider string, amountMinor int64, cadence string) (*Bill, error) {
	if provider == "" || cadence == "" {
		return nil, fmt.Errorf("provider and cadence are required")
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	b := &Bill{ID: "bill-" + uuid.NewString()[:8], Provider: provider, Amount: amountMinor, Cadence: cadence, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bills[b.ID] = b
	return copyBill(b), nil
}

// GetBill returns one bill.
func (s *Store) GetBill(id string) (*Bill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bills[id]
	if !ok {
		return nil, ErrBillNotFound
	}
	return copyBill(b), nil
}

// CompareBills detects cheaper-comparable market plans: same cadence and a
// lower amount than the catalogued bill, sorted cheapest first.
func (s *Store) CompareBills(id string, market []MarketPlan) ([]MarketPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bills[id]
	if !ok {
		return nil, ErrBillNotFound
	}
	var out []MarketPlan
	for _, m := range market {
		if m.Cadence == "" || m.Provider == "" {
			continue
		}
		if m.Cadence != b.Cadence {
			continue
		}
		if m.Amount < b.Amount {
			out = append(out, m)
		}
	}
	if out == nil {
		out = []MarketPlan{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount < out[j].Amount })
	return out, nil
}

// BillAction records cancel/switch/renew/ignore and starts the async task
// in PENDING. CompleteBillTask drives it to DONE.
func (s *Store) BillAction(id, action string) (*Bill, error) {
	switch action {
	case BillActionCancel, BillActionSwitch, BillActionRenew, BillActionIgnore:
	default:
		return nil, fmt.Errorf("unknown action %q (use cancel, switch, renew, ignore)", action)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bills[id]
	if !ok {
		return nil, ErrBillNotFound
	}
	b.Action = action
	b.TaskState = TaskPending
	return copyBill(b), nil
}

// CompleteBillTask marks the bill's async task done.
func (s *Store) CompleteBillTask(id string) (*Bill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bills[id]
	if !ok {
		return nil, ErrBillNotFound
	}
	if b.TaskState == "" {
		return nil, fmt.Errorf("bill %s has no task to complete", id)
	}
	b.TaskState = TaskDone
	return copyBill(b), nil
}

// AddMandate records one detected direct debit with merchant mapping.
func (s *Store) AddMandate(merchant, reference string, amountMinor int64) (*Mandate, error) {
	if merchant == "" || reference == "" {
		return nil, fmt.Errorf("merchant and reference are required")
	}
	m := &Mandate{ID: "mnd-" + uuid.NewString()[:8], Merchant: merchant, Reference: reference, AmountMinor: amountMinor, MappedMerchant: merchant, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mandates[m.ID] = m
	return copyMandate(m), nil
}

// GetMandate returns one mandate.
func (s *Store) GetMandate(id string) (*Mandate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mandates[id]
	if !ok {
		return nil, ErrMandateNotFound
	}
	return copyMandate(m), nil
}

// ListMandates returns the direct-debit detection list.
func (s *Store) ListMandates() []*Mandate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Mandate, 0, len(s.mandates))
	for _, m := range s.mandates {
		out = append(out, copyMandate(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// MapMerchant remaps a mandate to its canonical merchant.
func (s *Store) MapMerchant(id, canonical string) (*Mandate, error) {
	if canonical == "" {
		return nil, fmt.Errorf("canonical merchant is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mandates[id]
	if !ok {
		return nil, ErrMandateNotFound
	}
	m.MappedMerchant = canonical
	return copyMandate(m), nil
}

// MigrateMandate starts a migration task per mandate. The task requires
// verify-first-collection before it can complete.
func (s *Store) MigrateMandate(id, targetAccount string) (*Mandate, error) {
	if targetAccount == "" {
		return nil, fmt.Errorf("target account is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mandates[id]
	if !ok {
		return nil, ErrMandateNotFound
	}
	m.MigrationTarget = targetAccount
	m.MigrationState = TaskPending
	m.FirstCollectionVerified = false
	return copyMandate(m), nil
}

// VerifyFirstCollection verifies the first collection on the migrated
// mandate, completing the migration task.
func (s *Store) VerifyFirstCollection(id string) (*Mandate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mandates[id]
	if !ok {
		return nil, ErrMandateNotFound
	}
	if m.MigrationState == "" {
		return nil, fmt.Errorf("mandate %s has no migration to verify", id)
	}
	m.FirstCollectionVerified = true
	m.MigrationState = TaskDone
	return copyMandate(m), nil
}

// ScanClosure runs the pre-close safety scan. Any live commitment or
// non-zero balance blocks closure with an explicit blockers list.
func ScanClosure(in ClosureInput) ClosureResult {
	var blockers []string
	if in.DirectDebits > 0 {
		blockers = append(blockers, fmt.Sprintf("%d active direct debit(s)", in.DirectDebits))
	}
	if in.Recurring > 0 {
		blockers = append(blockers, fmt.Sprintf("%d recurring payment(s)", in.Recurring))
	}
	if in.PendingRefunds > 0 {
		blockers = append(blockers, fmt.Sprintf("%d pending refund(s)", in.PendingRefunds))
	}
	if in.PendingTransfers > 0 {
		blockers = append(blockers, fmt.Sprintf("%d pending transfer(s)", in.PendingTransfers))
	}
	if in.Subscriptions > 0 {
		blockers = append(blockers, fmt.Sprintf("%d subscription(s)", in.Subscriptions))
	}
	if in.BalanceMinor != 0 {
		blockers = append(blockers, fmt.Sprintf("non-zero balance %d minor units", in.BalanceMinor))
	}
	if blockers == nil {
		blockers = []string{}
	}
	if len(blockers) > 0 {
		return ClosureResult{Verdict: "UNSAFE", Blockers: blockers}
	}
	return ClosureResult{Verdict: "SAFE", Blockers: blockers}
}

func copyBill(b *Bill) *Bill {
	cp := *b
	return &cp
}

func copyMandate(m *Mandate) *Mandate {
	cp := *m
	return &cp
}
