// Package multicurrency implements multi-currency balances, FX rate
// snapshots, spread-aware conversion with an audit trail, and
// snapshot-pinned cross-currency valuation as a tested shared library.
//
// Valuation is pinned to a snapshot id (consistency token): once a snapshot
// is taken, ValuateTotal always uses that snapshot's rates even as newer
// snapshots arrive, so aggregates stay stable across rate moves.
package multicurrency

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInsufficientFunds marks debits/conversions exceeding the balance.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrUnknownSnapshot marks valuation/conversion against a missing snapshot.
	ErrUnknownSnapshot = errors.New("unknown fx snapshot")
	// ErrMissingRate marks a snapshot without the needed pair rate.
	ErrMissingRate = errors.New("snapshot has no rate for currency pair")
)

// Snapshot is one FX rate capture. Rates map "FROMTO" pairs (e.g.
// "EURGBP") to units of TO per 1 FROM. Base-to-base pairs are 1.
type Snapshot struct {
	ID    string             `json:"snapshot_id"`
	Rates map[string]float64 `json:"rates"`
	At    time.Time          `json:"at"`
}

// AuditEntry records one conversion.
type AuditEntry struct {
	ID         string    `json:"id"`
	Account    string    `json:"account"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Amount     int64     `json:"amount_minor"`
	Converted  int64     `json:"converted_minor"`
	Rate       float64   `json:"rate"`
	SpreadBps  int64     `json:"spread_bps"`
	SnapshotID string    `json:"snapshot_id"`
	At         time.Time `json:"at"`
}

// Ledger holds per-account per-currency balances plus snapshots + audit.
type Ledger struct {
	mu        sync.Mutex
	balances  map[string]map[string]int64
	snapshots map[string]*Snapshot
	order     []string
	audit     []AuditEntry
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{
		balances:  make(map[string]map[string]int64),
		snapshots: make(map[string]*Snapshot),
	}
}

func normCurrency(c string) string {
	out := ""
	for _, r := range c {
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out += string(r)
		}
	}
	return out
}

// Credit adds funds in one currency.
func (l *Ledger) Credit(account, currency string, amount int64) error {
	if account == "" || currency == "" {
		return fmt.Errorf("account and currency are required")
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	currency = normCurrency(currency)
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.balances[account]
	if !ok {
		m = make(map[string]int64)
		l.balances[account] = m
	}
	m[currency] += amount
	return nil
}

// Debit removes funds in one currency.
func (l *Ledger) Debit(account, currency string, amount int64) error {
	if account == "" || currency == "" {
		return fmt.Errorf("account and currency are required")
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	currency = normCurrency(currency)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.balances[account][currency] < amount {
		return ErrInsufficientFunds
	}
	l.balances[account][currency] -= amount
	return nil
}

// Balance returns one currency balance.
func (l *Ledger) Balance(account, currency string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balances[account][normCurrency(currency)]
}

// Balances returns a copy of all currency balances for an account.
func (l *Ledger) Balances(account string) map[string]int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int64, len(l.balances[account]))
	for k, v := range l.balances[account] {
		out[k] = v
	}
	return out
}

// SnapshotRates captures an FX rate snapshot and returns its id
// (the consistency token for later valuation/conversion).
func (l *Ledger) SnapshotRates(rates map[string]float64, at time.Time) (string, error) {
	if len(rates) == 0 {
		return "", fmt.Errorf("at least one rate is required")
	}
	for pair, r := range rates {
		if r <= 0 {
			return "", fmt.Errorf("rate for %s must be positive", pair)
		}
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	id := "fx-" + uuid.NewString()[:8]
	cp := make(map[string]float64, len(rates))
	for k, v := range rates {
		cp[k] = v
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.snapshots[id] = &Snapshot{ID: id, Rates: cp, At: at}
	l.order = append(l.order, id)
	return id, nil
}

// GetSnapshot returns one snapshot copy.
func (l *Ledger) GetSnapshot(id string) (*Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.snapshots[id]
	if !ok {
		return nil, ErrUnknownSnapshot
	}
	cp := &Snapshot{ID: s.ID, At: s.At, Rates: make(map[string]float64, len(s.Rates))}
	for k, v := range s.Rates {
		cp.Rates[k] = v
	}
	return cp, nil
}

// Convert moves funds across currencies using a pinned snapshot rate minus
// spread (basis points). The effective rate is rate*(1-spreadBps/10000) and
// the converted amount floors to minor units. Every conversion appends an
// audit entry.
func (l *Ledger) Convert(account, from, to string, amount int64, snapshotID string, spreadBps int64, at time.Time) (int64, error) {
	if account == "" || from == "" || to == "" {
		return 0, fmt.Errorf("account, from and to are required")
	}
	if amount <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	if spreadBps < 0 || spreadBps > 10000 {
		return 0, fmt.Errorf("spread_bps must be between 0 and 10000")
	}
	from = normCurrency(from)
	to = normCurrency(to)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, ok := l.snapshots[snapshotID]
	if !ok {
		return 0, ErrUnknownSnapshot
	}
	var rate float64
	if from == to {
		rate = 1
	} else {
		var ok bool
		rate, ok = snap.Rates[from+to]
		if !ok {
			return 0, fmt.Errorf("%w: %s%s in snapshot %s", ErrMissingRate, from, to, snapshotID)
		}
	}
	if l.balances[account][from] < amount {
		return 0, ErrInsufficientFunds
	}
	effective := rate * (1 - float64(spreadBps)/10000)
	converted := int64(float64(amount) * effective)
	if _, ok := l.balances[account]; !ok {
		l.balances[account] = make(map[string]int64)
	}
	l.balances[account][from] -= amount
	l.balances[account][to] += converted
	l.audit = append(l.audit, AuditEntry{
		ID: uuid.NewString(), Account: account, From: from, To: to,
		Amount: amount, Converted: converted, Rate: rate,
		SpreadBps: spreadBps, SnapshotID: snapshotID, At: at,
	})
	return converted, nil
}

// ValuateTotal aggregates every currency balance into the base currency
// using the pinned snapshot's rates. The snapshot id is required so the
// valuation is stable even as rates move.
func (l *Ledger) ValuateTotal(account, base, snapshotID string) (int64, error) {
	if account == "" || base == "" || snapshotID == "" {
		return 0, fmt.Errorf("account, base and snapshot_id are required")
	}
	base = normCurrency(base)
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, ok := l.snapshots[snapshotID]
	if !ok {
		return 0, ErrUnknownSnapshot
	}
	var total int64
	for ccy, bal := range l.balances[account] {
		if bal == 0 {
			continue
		}
		if ccy == base {
			total += bal
			continue
		}
		rate, ok := snap.Rates[ccy+base]
		if !ok {
			return 0, fmt.Errorf("%w: %s%s in snapshot %s", ErrMissingRate, ccy, base, snapshotID)
		}
		total += int64(float64(bal) * rate)
	}
	return total, nil
}

// Audit returns conversion audit entries for an account (all when empty)
// in insertion order.
func (l *Ledger) Audit(account string) []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []AuditEntry
	for _, e := range l.audit {
		if account == "" || e.Account == account {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []AuditEntry{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
