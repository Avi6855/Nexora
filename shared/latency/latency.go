// Package latency implements Nexora's API Latency Budget Manager.
//
// Every banking operation has a wall-clock budget (login 500ms, balance
// 300ms, payment 1s). This package splits a request's total budget across
// downstream calls, propagates the remaining deadline in-band (context +
// headers), and fails fast when a dependency burns more than its share —
// so a slow risk check can never eat the whole payment budget and push the
// customer-facing deadline past the point of usefulness.
package latency

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// HeaderDeadline propagates the absolute deadline downstream.
const HeaderDeadline = "X-Nexora-Deadline-Ms"

// HeaderBudget propagates the caller's total budget (for hop accounting).
const HeaderBudget = "X-Nexora-Budget-Ms"

// ErrExhausted means the request's latency budget is spent. Callers must map
// this to a fast, honest failure (or a fallback) — never queue indefinitely.
var ErrExhausted = errors.New("latency budget exhausted")

// ErrOverBudget means a single allocation exceeded the remaining budget.
var ErrOverBudget = errors.New("allocation exceeds remaining latency budget")

// Budget is an immutable total for one request.
type Budget struct {
	Total time.Duration
}

// Standard budgets per operation class (documented, not magic numbers).
var (
	BudgetLogin    = Budget{Total: 500 * time.Millisecond}
	BudgetBalance  = Budget{Total: 300 * time.Millisecond}
	BudgetPayment  = Budget{Total: 1 * time.Second}
	BudgetSearch   = Budget{Total: 2 * time.Second}
	BudgetInternal = Budget{Total: 5 * time.Second}
)

// Manager tracks one request's budget and hands out bounded allocations.
// It is safe for concurrent use within a request.
type Manager struct {
	mu       sync.Mutex
	total    time.Duration
	deadline time.Time
	spent    time.Duration
	spentBy  map[string]time.Duration
}

// New starts a budget manager now.
func New(b Budget) *Manager {
	return &Manager{total: b.Total, deadline: time.Now().Add(b.Total), spentBy: map[string]time.Duration{}}
}

// FromRequest builds a manager from propagated headers (a downstream service
// receiving an in-flight request continues the SAME budget, it does not start
// a fresh one). Falls back to the default budget when headers are absent.
func FromRequest(r *http.Request, fallback Budget) *Manager {
	dl := r.Header.Get(HeaderDeadline)
	tot := r.Header.Get(HeaderBudget)
	if dl == "" || tot == "" {
		return New(fallback)
	}
	deadlineMs, err1 := strconv.ParseInt(dl, 10, 64)
	totalMs, err2 := strconv.ParseInt(tot, 10, 64)
	if err1 != nil || err2 != nil {
		return New(fallback)
	}
	remaining := time.Duration(deadlineMs-time.Now().UnixMilli()) * time.Millisecond
	if remaining <= 0 {
		remaining = 0
	}
	if remaining > time.Duration(totalMs)*time.Millisecond {
		remaining = time.Duration(totalMs) * time.Millisecond
	}
	return &Manager{total: time.Duration(totalMs) * time.Millisecond, deadline: time.Now().Add(remaining), spentBy: map[string]time.Duration{}}
}

// Inject writes the propagation headers onto an outgoing request.
func (m *Manager) Inject(req *http.Request) {
	m.mu.Lock()
	deadline, total := m.deadline, m.total
	m.mu.Unlock()
	req.Header.Set(HeaderDeadline, strconv.FormatInt(deadline.UnixMilli(), 10))
	req.Header.Set(HeaderBudget, strconv.FormatInt(total.Milliseconds(), 10))
}

// Deadline returns the absolute deadline for this request.
func (m *Manager) Deadline() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deadline
}

// Context derives a context that expires exactly at the budget deadline.
func (m *Manager) Context(parent context.Context) (context.Context, context.CancelFunc) {
	m.mu.Lock()
	dl := m.deadline
	m.mu.Unlock()
	return context.WithDeadline(parent, dl)
}

// Allocate reserves up to `want` for one downstream call. Two ceilings bind:
// unspent reservation (total − spent) and physical wall-clock remaining
// before the deadline. The allocation is charged immediately — reserve what
// you will actually use, release leftovers with Release.
func (m *Manager) Allocate(component string, want time.Duration) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	allocatable := m.total - m.spent
	if wall := time.Until(m.deadline); wall < allocatable {
		allocatable = wall
	}
	if allocatable <= 0 {
		return 0, ErrExhausted
	}
	if want > allocatable {
		want = allocatable
	}
	m.spent += want
	m.spentBy[component] += want
	return want, nil
}

// Release returns unused allocation to the budget for the next caller.
func (m *Manager) Release(component string, d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spent -= d
	if m.spent < 0 {
		m.spent = 0
	}
	m.spentBy[component] -= d
	if m.spentBy[component] < 0 {
		m.spentBy[component] = 0
	}
}

// ExtendDeadline moves the absolute deadline earlier only — a downstream call
// that returns early can pull the deadline in, but nothing may extend a
// customer-facing deadline beyond its original budget.
func (m *Manager) ExtendDeadline(newDeadline time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if newDeadline.After(m.deadline.Add(m.total)) {
		return fmt.Errorf("deadline may not exceed original budget")
	}
	if newDeadline.Before(m.deadline) {
		m.deadline = newDeadline
	}
	return nil
}

// Remaining reports the unspent, allocatable budget: the lesser of unspent
// reservation and wall-clock remaining.
func (m *Manager) Remaining() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.total - m.spent
	if wall := time.Until(m.deadline); wall < r {
		r = wall
	}
	if r < 0 {
		r = 0
	}
	return r
}

// Report summarises where the budget went (for tracing and the tail-latency
// protection loop).
type Report struct {
	Total    time.Duration
	Spent    time.Duration
	Overhead map[string]time.Duration
}

// Summary returns the accounting report.
func (m *Manager) Summary() Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	by := make(map[string]time.Duration, len(m.spentBy))
	for k, v := range m.spentBy {
		by[k] = v
	}
	return Report{Total: m.total, Spent: m.spent, Overhead: by}
}

// Split evenly divides the remaining budget across named components — the
// quick way to provision parallel fan-out calls.
func (m *Manager) Split(names ...string) (map[string]time.Duration, error) {
	if len(names) == 0 {
		return nil, errors.New("split requires at least one component")
	}
	m.mu.Lock()
	remaining := time.Until(m.deadline)
	m.mu.Unlock()
	if remaining <= 0 {
		return nil, ErrExhausted
	}
	share := remaining / time.Duration(len(names))
	out := make(map[string]time.Duration, len(names))
	for _, n := range names {
		d, err := m.Allocate(n, share)
		if err != nil {
			return nil, err
		}
		out[n] = d
	}
	return out, nil
}

// SortedComponents returns component names by spend, descending (stable for
// deterministic reports).
func (r Report) SortedComponents() []string {
	names := make([]string, 0, len(r.Overhead))
	for k := range r.Overhead {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if r.Overhead[names[i]] != r.Overhead[names[j]] {
			return r.Overhead[names[i]] > r.Overhead[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// Middleware attaches a budget manager to every inbound request, continuing
// propagated budgets where present.
func Middleware(defaults map[string]Budget, fallback Budget) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := fallback
			if specific, ok := defaults[r.URL.Path]; ok {
				b = specific
			}
			m := FromRequest(r, b)
			ctx := context.WithValue(r.Context(), managerKey{}, m)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type managerKey struct{}

// FromContext retrieves the request's budget manager.
func FromContext(ctx context.Context) (*Manager, bool) {
	m, ok := ctx.Value(managerKey{}).(*Manager)
	return m, ok
}
