// Package ledgerguard implements Nexora's ledger invariant monitor +
// reservation engine as a tested shared library.
//
//  1. Monitor: every journal posting must satisfy double-entry balance
//     (sum debits == sum credits per journal) AND the accounting equation
//     (Assets == Liabilities + Equity at account-class level). A violation
//     raises an ALERT incident and freezes the journal — further Post calls
//     for that journal are rejected until the freeze is cleared.
//
//  2. Engine: card-style authorisation ledger. Available funds are always
//     ledger balance minus active reservations. Supports incremental
//     authorisation (TopUp), partial/full capture, reversal, expiry sweep,
//     and late presentment (SettleLate) which forces a review flag exactly
//     once and never double-spends.
package ledgerguard

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrJournalFrozen blocks mutation of a journal frozen by the monitor.
	ErrJournalFrozen = errors.New("journal is frozen by the ledger invariant monitor")
	// ErrJournalNotFound is returned for unknown journal IDs.
	ErrJournalNotFound = errors.New("journal not found")
	// ErrIncidentNotFound is returned for unknown incident IDs.
	ErrIncidentNotFound = errors.New("incident not found")
	// ErrUnbalanced marks a posting that breaks double-entry or the
	// accounting equation.
	ErrUnbalanced = errors.New("posting violates ledger invariants")
	// ErrDuplicateReservation rejects reuse of a reservation ID.
	ErrDuplicateReservation = errors.New("duplicate reservation ID")
	// ErrReservationNotFound is returned for unknown reservation IDs.
	ErrReservationNotFound = errors.New("reservation not found")
	// ErrReservationNotActive marks capture/reverse/settle on a terminal
	// reservation.
	ErrReservationNotActive = errors.New("reservation is not active")
	// ErrInsufficientFunds marks an authorisation exceeding available funds.
	ErrInsufficientFunds = errors.New("insufficient available funds")
	// ErrInvalidAmount marks non-positive amounts.
	ErrInvalidAmount = errors.New("amount must be positive")
)

// Class is the account class used for the accounting-equation check.
type Class string

const (
	ClassAsset     Class = "ASSET"
	ClassLiability Class = "LIABILITY"
	ClassEquity    Class = "EQUITY"
)

// Entry is one posting line: exactly one of Debit/Credit must be positive.
type Entry struct {
	Account string `json:"account"`
	Class   Class  `json:"class"`
	Debit   int64  `json:"debit"`
	Credit  int64  `json:"credit"`
}

// Journal is a balanced (or frozen) group of entries.
type Journal struct {
	ID      string  `json:"id"`
	Entries []Entry `json:"entries"`
	Frozen  bool    `json:"frozen"`
}

// CheckStatus is PASS or VIOLATION.
type CheckStatus string

const (
	CheckPass      CheckStatus = "PASS"
	CheckViolation CheckStatus = "VIOLATION"
)

// CheckResult is the outcome of a Post call.
type CheckResult struct {
	JournalID  string      `json:"journal_id"`
	Status     CheckStatus `json:"status"`
	Message    string      `json:"message"`
	IncidentID string      `json:"incident_id,omitempty"`
}

// Incident is the ALERT record filed on violation.
type Incident struct {
	ID        string     `json:"id"`
	JournalID string     `json:"journal_id"`
	Severity  string     `json:"severity"`
	Reason    string     `json:"reason"`
	CreatedAt time.Time  `json:"created_at"`
	Cleared   bool       `json:"cleared"`
	ClearedAt *time.Time `json:"cleared_at,omitempty"`
}

// Monitor enforces ledger invariants across journal postings.
type Monitor struct {
	mu        sync.Mutex
	journals  map[string]*Journal
	incidents map[string]*Incident
	order     []string
}

// NewMonitor returns an empty monitor.
func NewMonitor() *Monitor {
	return &Monitor{
		journals:  make(map[string]*Journal),
		incidents: make(map[string]*Incident),
	}
}

// Post validates entries, appends them to the journal on success, or records
// a VIOLATION (ALERT incident + journal freeze) on invariant breach.
// Posting to a frozen journal is blocked with ErrJournalFrozen.
func (m *Monitor) Post(journalID string, entries []Entry) (*CheckResult, error) {
	if journalID == "" {
		return nil, fmt.Errorf("journal_id is required")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("at least one entry is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.journals[journalID]
	if !ok {
		j = &Journal{ID: journalID}
		m.journals[journalID] = j
	}
	if j.Frozen {
		return &CheckResult{JournalID: journalID, Status: CheckViolation, Message: "journal is frozen"}, ErrJournalFrozen
	}

	if err := validateEntries(entries); err != nil {
		return m.violateLocked(j, err.Error()), fmt.Errorf("%w: %s", ErrUnbalanced, err.Error())
	}
	var debits, credits int64
	for _, e := range entries {
		debits += e.Debit
		credits += e.Credit
	}
	if debits != credits {
		msg := fmt.Sprintf("sum(debits)=%d != sum(credits)=%d", debits, credits)
		return m.violateLocked(j, msg), fmt.Errorf("%w: %s", ErrUnbalanced, msg)
	}
	if msg := checkEquationLocked(m.journals, entries); msg != "" {
		return m.violateLocked(j, msg), fmt.Errorf("%w: %s", ErrUnbalanced, msg)
	}

	j.Entries = append(j.Entries, entries...)
	return &CheckResult{JournalID: journalID, Status: CheckPass, Message: "balanced"}, nil
}

func validateEntries(entries []Entry) error {
	for i, e := range entries {
		if e.Account == "" {
			return fmt.Errorf("entry %d: account is required", i)
		}
		switch e.Class {
		case ClassAsset, ClassLiability, ClassEquity:
		default:
			return fmt.Errorf("entry %d: unknown class %q", i, e.Class)
		}
		if e.Debit < 0 || e.Credit < 0 {
			return fmt.Errorf("entry %d: amounts must be non-negative", i)
		}
		if (e.Debit > 0) == (e.Credit > 0) {
			return fmt.Errorf("entry %d: exactly one of debit/credit must be positive", i)
		}
	}
	return nil
}

// checkEquationLocked verifies Assets == Liabilities + Equity at class level
// over all committed state plus the candidate entries. Asset nets use normal
// debit balances (debit-credit); liability/equity nets use normal credit
// balances (credit-debit). Balanced postings preserve a balanced ledger, so
// any breach here is reported distinctly from the per-journal sums check.
func checkEquationLocked(journals map[string]*Journal, candidate []Entry) string {
	var aD, aC, lD, lC, eD, eC int64
	acc := func(e Entry) {
		switch e.Class {
		case ClassAsset:
			aD += e.Debit
			aC += e.Credit
		case ClassLiability:
			lD += e.Debit
			lC += e.Credit
		case ClassEquity:
			eD += e.Debit
			eC += e.Credit
		}
	}
	for _, j := range journals {
		for _, e := range j.Entries {
			acc(e)
		}
	}
	for _, e := range candidate {
		acc(e)
	}
	assets := aD - aC
	liabEquity := (lC - lD) + (eC - eD)
	if assets != liabEquity {
		return fmt.Sprintf("accounting equation breached: Assets=%d != Liabilities+Equity=%d", assets, liabEquity)
	}
	return ""
}

func (m *Monitor) violateLocked(j *Journal, reason string) *CheckResult {
	j.Frozen = true
	inc := &Incident{
		ID:        uuid.NewString(),
		JournalID: j.ID,
		Severity:  "ALERT",
		Reason:    reason,
		CreatedAt: time.Now().UTC(),
	}
	m.incidents[inc.ID] = inc
	m.order = append(m.order, inc.ID)
	return &CheckResult{JournalID: j.ID, Status: CheckViolation, Message: reason, IncidentID: inc.ID}
}

// Freeze locks a journal (idempotent). Unknown journals are created frozen
// so downstream mutation is blocked even before the first posting.
func (m *Monitor) Freeze(journalID string) error {
	if journalID == "" {
		return fmt.Errorf("journal_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.journals[journalID]
	if !ok {
		j = &Journal{ID: journalID}
		m.journals[journalID] = j
	}
	j.Frozen = true
	return nil
}

// Unfreeze clears the freeze flag so the journal accepts postings again.
// Incident records are untouched; use ClearIncident to close them.
func (m *Monitor) Unfreeze(journalID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.journals[journalID]
	if !ok {
		return ErrJournalNotFound
	}
	j.Frozen = false
	return nil
}

// IsFrozen reports the freeze flag (false for unknown journals).
func (m *Monitor) IsFrozen(journalID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.journals[journalID]
	return ok && j.Frozen
}

// GetJournal returns a copy of a journal.
func (m *Monitor) GetJournal(journalID string) (*Journal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.journals[journalID]
	if !ok {
		return nil, ErrJournalNotFound
	}
	cp := *j
	cp.Entries = append([]Entry(nil), j.Entries...)
	return &cp, nil
}

// Incidents returns all incidents in filing order.
func (m *Monitor) Incidents() []Incident {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Incident, 0, len(m.order))
	for _, id := range m.order {
		if inc, ok := m.incidents[id]; ok {
			out = append(out, *inc)
		}
	}
	return out
}

// ClearIncident marks an incident cleared. When no open incidents remain for
// its journal, the journal freeze is lifted automatically.
func (m *Monitor) ClearIncident(incidentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inc, ok := m.incidents[incidentID]
	if !ok {
		return ErrIncidentNotFound
	}
	if !inc.Cleared {
		now := time.Now().UTC()
		inc.Cleared = true
		inc.ClearedAt = &now
	}
	for _, other := range m.incidents {
		if other.JournalID == inc.JournalID && !other.Cleared {
			return nil
		}
	}
	if j, ok := m.journals[inc.JournalID]; ok {
		j.Frozen = false
	}
	return nil
}

// ── Reservation engine ──────────────────────────────────────────────────────

// ReservationStatus is the lifecycle state of a hold.
type ReservationStatus string

const (
	ReservationActive   ReservationStatus = "ACTIVE"
	ReservationCaptured ReservationStatus = "CAPTURED"
	ReservationReversed ReservationStatus = "REVERSED"
	ReservationExpired  ReservationStatus = "EXPIRED"
)

// Reservation is one authorisation hold.
type Reservation struct {
	ID         string            `json:"id"`
	Account    string            `json:"account"`
	Authorized int64             `json:"authorized"`
	Captured   int64             `json:"captured"`
	Remaining  int64             `json:"remaining"`
	ExpiresAt  time.Time         `json:"expires_at"`
	Status     ReservationStatus `json:"status"`
	Review     bool              `json:"review"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Balance reports ledger vs reserved vs available funds.
type Balance struct {
	Account   string `json:"account"`
	Ledger    int64  `json:"ledger"`
	Reserved  int64  `json:"reserved"`
	Available int64  `json:"available"`
}

// Engine tracks ledger balances and authorisation holds.
// Available funds are always ledger balance minus active reservations.
type Engine struct {
	mu           sync.Mutex
	balances     map[string]int64
	reservations map[string]*Reservation
}

// NewEngine returns an empty engine.
func NewEngine() *Engine {
	return &Engine{
		balances:     make(map[string]int64),
		reservations: make(map[string]*Reservation),
	}
}

// SetBalance funds an account in the engine ledger.
func (e *Engine) SetBalance(account string, balance int64) {
	if account == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.balances[account] = balance
}

func (e *Engine) reservedLocked(account string) int64 {
	var total int64
	for _, r := range e.reservations {
		if r.Account == account && r.Status == ReservationActive {
			total += r.Authorized - r.Captured
		}
	}
	return total
}

// Balances returns ledger, reserved and available funds for an account.
func (e *Engine) Balances(account string) Balance {
	e.mu.Lock()
	defer e.mu.Unlock()
	ledger := e.balances[account]
	reserved := e.reservedLocked(account)
	return Balance{Account: account, Ledger: ledger, Reserved: reserved, Available: ledger - reserved}
}

// Get returns a copy of a reservation.
func (e *Engine) Get(id string) (*Reservation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	cp := *r
	return &cp, nil
}

// Authorize places a hold. Duplicate IDs are rejected with
// ErrDuplicateReservation; holds exceeding available funds fail with
// ErrInsufficientFunds.
func (e *Engine) Authorize(id, account string, amount int64, expiresAt time.Time) (*Reservation, error) {
	if id == "" || account == "" {
		return nil, fmt.Errorf("id and account are required")
	}
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(24 * time.Hour)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.reservations[id]; exists {
		return nil, ErrDuplicateReservation
	}
	available := e.balances[account] - e.reservedLocked(account)
	if available < amount {
		return nil, ErrInsufficientFunds
	}
	now := time.Now().UTC()
	r := &Reservation{
		ID: id, Account: account, Authorized: amount,
		Remaining: amount, ExpiresAt: expiresAt,
		Status: ReservationActive, CreatedAt: now, UpdatedAt: now,
	}
	e.reservations[id] = r
	cp := *r
	return &cp, nil
}

// TopUp incrementally authorises more funds on an active reservation.
func (e *Engine) TopUp(id string, additional int64) (*Reservation, error) {
	if additional <= 0 {
		return nil, ErrInvalidAmount
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	if r.Status != ReservationActive {
		return nil, ErrReservationNotActive
	}
	if !time.Now().UTC().Before(r.ExpiresAt) {
		r.Status = ReservationExpired
		r.UpdatedAt = time.Now().UTC()
		return nil, ErrReservationNotActive
	}
	available := e.balances[r.Account] - e.reservedLocked(r.Account)
	if available < additional {
		return nil, ErrInsufficientFunds
	}
	r.Authorized += additional
	r.Remaining = r.Authorized - r.Captured
	r.UpdatedAt = time.Now().UTC()
	cp := *r
	return &cp, nil
}

// Capture settles part or all of an authorisation. A full capture closes the
// reservation (CAPTURED); a partial capture leaves it ACTIVE with reduced
// remaining hold. Captured funds leave the ledger balance immediately.
func (e *Engine) Capture(id string, amount int64) (*Reservation, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	if r.Status != ReservationActive {
		return nil, ErrReservationNotActive
	}
	if !time.Now().UTC().Before(r.ExpiresAt) {
		r.Status = ReservationExpired
		r.UpdatedAt = time.Now().UTC()
		return nil, ErrReservationNotActive
	}
	remaining := r.Authorized - r.Captured
	if amount > remaining {
		return nil, fmt.Errorf("capture amount %d exceeds remaining hold %d", amount, remaining)
	}
	if e.balances[r.Account] < amount {
		return nil, ErrInsufficientFunds
	}
	e.balances[r.Account] -= amount
	r.Captured += amount
	r.Remaining = r.Authorized - r.Captured
	if r.Remaining == 0 {
		r.Status = ReservationCaptured
	}
	r.UpdatedAt = time.Now().UTC()
	cp := *r
	return &cp, nil
}

// Reverse releases the remaining hold without moving funds.
func (e *Engine) Reverse(id string) (*Reservation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	if r.Status != ReservationActive {
		return nil, ErrReservationNotActive
	}
	r.Status = ReservationReversed
	r.Remaining = 0
	r.UpdatedAt = time.Now().UTC()
	cp := *r
	return &cp, nil
}

// SweepExpired marks every active reservation past its expiry as EXPIRED,
// releasing its hold. It returns the number of reservations expired.
func (e *Engine) SweepExpired(now time.Time) int {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var count int
	for _, r := range e.reservations {
		if r.Status == ReservationActive && !now.Before(r.ExpiresAt) {
			r.Status = ReservationExpired
			r.Remaining = r.Authorized - r.Captured
			r.UpdatedAt = now
			count++
		}
	}
	return count
}

// SettleLate handles late presentment after expiry: the funds settle once and
// the reservation is flagged for manual review. Repeating the call is a
// no-op (flagged once, never double-spends). Only EXPIRED reservations may
// settle late.
func (e *Engine) SettleLate(id string, amount int64) (*Reservation, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}
	if r.Status != ReservationExpired {
		return nil, ErrReservationNotActive
	}
	if r.Review {
		cp := *r
		return &cp, nil
	}
	remaining := r.Authorized - r.Captured
	if amount > remaining {
		return nil, fmt.Errorf("late settlement %d exceeds remaining hold %d", amount, remaining)
	}
	if e.balances[r.Account] < amount {
		return nil, ErrInsufficientFunds
	}
	e.balances[r.Account] -= amount
	r.Captured += amount
	r.Remaining = r.Authorized - r.Captured
	r.Review = true
	r.UpdatedAt = time.Now().UTC()
	cp := *r
	return &cp, nil
}
