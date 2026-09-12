// Package distcoord implements distributed-coordination primitives for the
// control plane:
//
//  29. Correlation protocol: {correlation_id, causation_id} per inbound
//     action, propagate helper (child causation = parent id), chain
//     completeness validation.
//  30. Causality tracker: edge store A→B→C; WhyHappened(C) returns the full
//     causal chain; cycle-safe.
//  31. Clock-skew monitor: node clock samples → skew matrix; policy flag
//     whether to delay/disable critical workflows when skew > threshold.
//  32. HLC timestamps: hybrid logical clock (wall ms + logical + node id);
//     Issue/Compare; merge on receive; monotonic across clock regression.
//  33. Lock diagnostics: registry {resource, owner, acquired_at, ttl,
//     waiters}; stuck-lock detection (held > k×TTL with waiters);
//     contention stats per resource.
//  34. Contention advisor: from wait graphs identify hot resource / long
//     critical section / wrong granularity → {shard, partition, shorten,
//     serialize-queue}.
package distcoord

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrActionRequired is returned when an action name is empty.
	ErrActionRequired = errors.New("action is required")
	// ErrUnknownNode is returned when a node has no recorded data.
	ErrUnknownNode = errors.New("unknown node")
	// ErrLockNotFound is returned when a resource has no lock record.
	ErrLockNotFound = errors.New("lock not found")
	// ErrLockNotOwner is returned when release comes from a non-owner.
	ErrLockNotOwner = errors.New("release rejected: not lock owner")
)

// ─── 29. Correlation protocol ────────────────────────────────────────────

// Correlation is one traced action.
type Correlation struct {
	ID            string    `json:"id"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id"`
	Action        string    `json:"action"`
	At            time.Time `json:"at"`
}

// CorrelationStore issues and retains correlations.
type CorrelationStore struct {
	mu      sync.Mutex
	records map[string]Correlation
}

// NewCorrelationStore builds an empty store.
func NewCorrelationStore() *CorrelationStore {
	return &CorrelationStore{records: map[string]Correlation{}}
}

// Issue creates a root correlation for one inbound action.
func (s *CorrelationStore) Issue(action string) (Correlation, error) {
	if strings.TrimSpace(action) == "" {
		return Correlation{}, ErrActionRequired
	}
	id := uuid.NewString()
	c := Correlation{
		ID:            id,
		CorrelationID: id,
		CausationID:   "",
		Action:        action,
		At:            time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[c.ID] = c
	return c, nil
}

// Propagate derives a child span: it keeps the parent's correlation id and
// sets the child causation id to the parent id.
func (s *CorrelationStore) Propagate(parent Correlation, action string) (Correlation, error) {
	if strings.TrimSpace(action) == "" {
		return Correlation{}, ErrActionRequired
	}
	if strings.TrimSpace(parent.ID) == "" || strings.TrimSpace(parent.CorrelationID) == "" {
		return Correlation{}, fmt.Errorf("parent correlation is invalid")
	}
	c := Correlation{
		ID:            uuid.NewString(),
		CorrelationID: parent.CorrelationID,
		CausationID:   parent.ID,
		Action:        action,
		At:            time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[c.ID] = c
	return c, nil
}

// ValidateChain checks chain completeness: non-empty, one shared
// correlation id, exactly one root, every causation id resolves, no
// duplicate ids.
func ValidateChain(chain []Correlation) error {
	if len(chain) == 0 {
		return fmt.Errorf("chain is empty")
	}
	ids := map[string]bool{}
	corr := chain[0].CorrelationID
	if corr == "" {
		return fmt.Errorf("correlation_id is required")
	}
	roots := 0
	for _, c := range chain {
		if c.ID == "" {
			return fmt.Errorf("correlation id is required")
		}
		if ids[c.ID] {
			return fmt.Errorf("duplicate correlation id %q", c.ID)
		}
		ids[c.ID] = true
		if c.CorrelationID != corr {
			return fmt.Errorf("mixed correlation_id: %q vs %q", c.CorrelationID, corr)
		}
		if c.CausationID == "" {
			roots++
		}
	}
	if roots != 1 {
		return fmt.Errorf("chain must have exactly one root, got %d", roots)
	}
	for _, c := range chain {
		if c.CausationID == "" {
			continue
		}
		if !ids[c.CausationID] {
			return fmt.Errorf("dangling causation_id %q for %q", c.CausationID, c.ID)
		}
	}
	return nil
}

// ─── 30. Causality tracker ───────────────────────────────────────────────

// CausalEdge is one happened-before edge From → To.
type CausalEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// CausalityTracker stores causal edges and answers "why did X happen?".
type CausalityTracker struct {
	mu      sync.Mutex
	forward map[string]map[string]bool
	reverse map[string]map[string]bool
	known   map[string]bool
}

// NewCausalityTracker builds an empty tracker.
func NewCausalityTracker() *CausalityTracker {
	return &CausalityTracker{
		forward: map[string]map[string]bool{},
		reverse: map[string]map[string]bool{},
		known:   map[string]bool{},
	}
}

// AddEdge records A → B (A caused B).
func (t *CausalityTracker) AddEdge(from, to string) error {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return fmt.Errorf("from and to are required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.forward[from] == nil {
		t.forward[from] = map[string]bool{}
	}
	if t.reverse[to] == nil {
		t.reverse[to] = map[string]bool{}
	}
	t.forward[from][to] = true
	t.reverse[to][from] = true
	t.known[from] = true
	t.known[to] = true
	return nil
}

// Edges returns a sorted snapshot of edges.
func (t *CausalityTracker) Edges() []CausalEdge {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []CausalEdge{}
	for from, tos := range t.forward {
		for to := range tos {
			out = append(out, CausalEdge{From: from, To: to})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// WhyHappened returns the full causal chain for node (all transitive
// causes, roots first), cycle-safe. Unknown nodes yield ErrUnknownNode.
func (t *CausalityTracker) WhyHappened(node string) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.known[node] {
		return nil, fmt.Errorf("%w: %s", ErrUnknownNode, node)
	}
	// BFS backwards from node over reverse edges; visited guards cycles.
	visited := map[string]bool{node: true}
	queue := []string{node}
	order := []string{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		parents := []string{}
		for p := range t.reverse[cur] {
			parents = append(parents, p)
		}
		sort.Strings(parents)
		for _, p := range parents {
			if visited[p] {
				continue
			}
			visited[p] = true
			order = append(order, p)
			queue = append(queue, p)
		}
	}
	// order is nearest-cause-first; reverse so roots come first.
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	if order == nil {
		order = []string{}
	}
	return order, nil
}

// ─── 31. Clock-skew monitor ──────────────────────────────────────────────

// ClockSample is one node's wall-clock reading (unix millis).
type ClockSample struct {
	Node   string `json:"node"`
	WallMs int64  `json:"wall_ms"`
}

// SkewPolicy is the monitor's workflow-safety answer.
type SkewPolicy struct {
	MaxSkewMs       int64  `json:"max_skew_ms"`
	ThresholdMs     int64  `json:"threshold_ms"`
	DisableCritical bool   `json:"disable_critical"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
}

// SkewMonitor tracks latest wall readings per node.
type SkewMonitor struct {
	mu        sync.Mutex
	walls     map[string]int64
	threshold int64
}

// NewSkewMonitor builds a monitor; non-positive thresholds get 500ms.
func NewSkewMonitor(thresholdMs int64) *SkewMonitor {
	if thresholdMs <= 0 {
		thresholdMs = 500
	}
	return &SkewMonitor{walls: map[string]int64{}, threshold: thresholdMs}
}

// RecordSample stores one node's wall reading.
func (m *SkewMonitor) RecordSample(node string, wallMs int64) error {
	if strings.TrimSpace(node) == "" {
		return fmt.Errorf("node is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.walls[node] = wallMs
	return nil
}

// SkewMatrix returns pairwise absolute skews in ms.
func (m *SkewMonitor) SkewMatrix() map[string]map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]map[string]int64{}
	for a, wa := range m.walls {
		out[a] = map[string]int64{}
		for b, wb := range m.walls {
			d := wa - wb
			if d < 0 {
				d = -d
			}
			out[a][b] = d
		}
	}
	return out
}

// MaxSkew returns the largest pairwise skew (0 with <2 nodes).
func (m *SkewMonitor) MaxSkew() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxSkewLocked()
}

func (m *SkewMonitor) maxSkewLocked() int64 {
	var (
		haveMin, haveMax bool
		mn, mx           int64
	)
	for _, w := range m.walls {
		if !haveMin || w < mn {
			mn, haveMin = w, true
		}
		if !haveMax || w > mx {
			mx, haveMax = w, true
		}
	}
	if !haveMin {
		return 0
	}
	return mx - mn
}

// Policy flags whether critical workflows must pause when skew exceeds
// the threshold.
func (m *SkewMonitor) Policy() SkewPolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	maxSkew := m.maxSkewLocked()
	if maxSkew > m.threshold {
		return SkewPolicy{
			MaxSkewMs: maxSkew, ThresholdMs: m.threshold,
			DisableCritical: true, Action: "DISABLE_CRITICAL",
			Reason: fmt.Sprintf("max skew %dms exceeds threshold %dms; critical workflows disabled", maxSkew, m.threshold),
		}
	}
	return SkewPolicy{
		MaxSkewMs: maxSkew, ThresholdMs: m.threshold,
		DisableCritical: false, Action: "ALLOW",
		Reason: fmt.Sprintf("max skew %dms within threshold %dms", maxSkew, m.threshold),
	}
}

// ─── 32. HLC timestamps ──────────────────────────────────────────────────

// Timestamp is a hybrid-logical-clock reading.
type Timestamp struct {
	WallMs  int64  `json:"wall_ms"`
	Logical uint64 `json:"logical"`
	Node    string `json:"node"`
}

// Clock is a per-node HLC.
type Clock struct {
	mu   sync.Mutex
	node string
	last Timestamp
}

// NewClock builds a clock for node.
func NewClock(node string) *Clock {
	return &Clock{node: node}
}

// Issue returns the next timestamp for wallMs (unix millis), monotonic
// even when the wall clock regresses.
func (c *Clock) Issue(wallMs int64) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	var next Timestamp
	switch {
	case wallMs > c.last.WallMs:
		next = Timestamp{WallMs: wallMs, Logical: 0, Node: c.node}
	default:
		next = Timestamp{WallMs: c.last.WallMs, Logical: c.last.Logical + 1, Node: c.node}
	}
	c.last = next
	return next
}

// Receive merges a remote timestamp on message receipt and returns the
// next local timestamp, monotonic across sender/receiver skew.
func (c *Clock) Receive(remote Timestamp, wallMs int64) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	wall := wallMs
	if remote.WallMs > wall {
		wall = remote.WallMs
	}
	if c.last.WallMs > wall {
		wall = c.last.WallMs
	}
	var logical uint64
	switch {
	case wall == c.last.WallMs && wall == remote.WallMs:
		logical = c.last.Logical
		if remote.Logical > logical {
			logical = remote.Logical
		}
		logical++
	case wall == c.last.WallMs:
		logical = c.last.Logical + 1
	case wall == remote.WallMs:
		logical = remote.Logical + 1
	default:
		logical = 0
	}
	next := Timestamp{WallMs: wall, Logical: logical, Node: c.node}
	c.last = next
	return next
}

// Compare orders timestamps: wall, then logical, then node id.
// Returns -1, 0 or +1.
func Compare(a, b Timestamp) int {
	if a.WallMs != b.WallMs {
		if a.WallMs < b.WallMs {
			return -1
		}
		return 1
	}
	if a.Logical != b.Logical {
		if a.Logical < b.Logical {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Node, b.Node)
}

// ─── 33. Lock diagnostics ────────────────────────────────────────────────

// LockInfo is one held lock.
type LockInfo struct {
	Resource   string    `json:"resource"`
	Owner      string    `json:"owner"`
	AcquiredAt time.Time `json:"acquired_at"`
	TTLMs      int64     `json:"ttl_ms"`
	Waiters    []string  `json:"waiters"`
}

// ResourceStats aggregates contention per resource.
type ResourceStats struct {
	Resource     string `json:"resource"`
	Held         bool   `json:"held"`
	Owner        string `json:"owner,omitempty"`
	Waiters      int    `json:"waiters"`
	Acquisitions int    `json:"acquisitions"`
	Contentions  int    `json:"contentions"`
}

// LockRegistry tracks held locks, waiters and contention counters.
type LockRegistry struct {
	mu           sync.Mutex
	locks        map[string]*LockInfo
	acquisitions map[string]int
	contentions  map[string]int
}

// NewLockRegistry builds an empty registry.
func NewLockRegistry() *LockRegistry {
	return &LockRegistry{locks: map[string]*LockInfo{}, acquisitions: map[string]int{}, contentions: map[string]int{}}
}

// Acquire takes the lock when free (or TTL-expired, treating the old owner
// as gone); otherwise the owner queues as a waiter and acquired=false.
func (r *LockRegistry) Acquire(resource, owner string, ttlMs int64, now time.Time) (bool, error) {
	if strings.TrimSpace(resource) == "" || strings.TrimSpace(owner) == "" {
		return false, fmt.Errorf("resource and owner are required")
	}
	if ttlMs <= 0 {
		return false, fmt.Errorf("ttl_ms must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.locks[resource]; ok {
		if now.Sub(cur.AcquiredAt) < time.Duration(ttlMs)*time.Millisecond {
			// Still held: queue waiter (idempotent per owner).
			for _, w := range cur.Waiters {
				if w == owner {
					r.contentions[resource]++
					return false, nil
				}
			}
			cur.Waiters = append(cur.Waiters, owner)
			r.contentions[resource]++
			return false, nil
		}
		// TTL expired: previous owner is considered gone.
	}
	r.locks[resource] = &LockInfo{Resource: resource, Owner: owner, AcquiredAt: now, TTLMs: ttlMs, Waiters: []string{}}
	r.acquisitions[resource]++
	return true, nil
}

// Release frees a held lock; only the owner may release.
func (r *LockRegistry) Release(resource, owner string) error {
	if strings.TrimSpace(resource) == "" || strings.TrimSpace(owner) == "" {
		return fmt.Errorf("resource and owner are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.locks[resource]
	if !ok {
		return fmt.Errorf("%w: %s", ErrLockNotFound, resource)
	}
	if cur.Owner != owner {
		return fmt.Errorf("%w: held by %s", ErrLockNotOwner, cur.Owner)
	}
	delete(r.locks, resource)
	return nil
}

// StuckLocks returns held locks older than k×TTL that still have waiters.
func (r *LockRegistry) StuckLocks(k float64, now time.Time) []LockInfo {
	if k <= 0 {
		k = 2
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []LockInfo{}
	for _, l := range r.locks {
		limit := time.Duration(float64(l.TTLMs)*k) * time.Millisecond
		if len(l.Waiters) > 0 && now.Sub(l.AcquiredAt) > limit {
			cp := *l
			cp.Waiters = append([]string{}, l.Waiters...)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

// Stats returns per-resource contention counters.
func (r *LockRegistry) Stats() []ResourceStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := map[string]bool{}
	for n := range r.acquisitions {
		names[n] = true
	}
	for n := range r.contentions {
		names[n] = true
	}
	for n := range r.locks {
		names[n] = true
	}
	out := []ResourceStats{}
	for n := range names {
		st := ResourceStats{Resource: n, Acquisitions: r.acquisitions[n], Contentions: r.contentions[n]}
		if l, ok := r.locks[n]; ok {
			st.Held = true
			st.Owner = l.Owner
			st.Waiters = len(l.Waiters)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

// ─── 34. Contention advisor ──────────────────────────────────────────────

// WaitEdge is one waiter → holder wait on a resource.
type WaitEdge struct {
	Waiter   string `json:"waiter"`
	Holder   string `json:"holder"`
	Resource string `json:"resource"`
	HoldMs   int64  `json:"hold_ms"`
}

// ContentionAdvice is one remediation recommendation.
type ContentionAdvice struct {
	Resource       string `json:"resource"`
	Pattern        string `json:"pattern"`
	Recommendation string `json:"recommendation"`
	Reason         string `json:"reason"`
}

const (
	// HotResourceWaiters triggers sharding: many waiters, one resource.
	HotResourceWaiters = 5
	// LongCriticalSectionMs triggers shortening the critical section.
	LongCriticalSectionMs = 500
	// WideGranularityHolders triggers re-partitioning: one holder guards
	// many resources (lock scope too coarse).
	WideGranularityHolders = 3
)

// AdviseContention maps a wait graph to remediation recommendations, one
// per resource, ranked by waiter count desc.
func AdviseContention(edges []WaitEdge) []ContentionAdvice {
	byResource := map[string][]WaitEdge{}
	holderResources := map[string]map[string]bool{}
	for _, e := range edges {
		if e.Resource == "" {
			continue
		}
		byResource[e.Resource] = append(byResource[e.Resource], e)
		if holderResources[e.Holder] == nil {
			holderResources[e.Holder] = map[string]bool{}
		}
		holderResources[e.Holder][e.Resource] = true
	}
	out := []ContentionAdvice{}
	for res, list := range byResource {
		waiters := map[string]bool{}
		var totalHold int64
		for _, e := range list {
			waiters[e.Waiter] = true
			totalHold += e.HoldMs
		}
		avgHold := int64(0)
		if len(list) > 0 {
			avgHold = totalHold / int64(len(list))
		}
		wide := false
		for _, resSet := range holderResources {
			if len(resSet) >= WideGranularityHolders && resSet[res] {
				wide = true
				break
			}
		}
		switch {
		case len(waiters) >= HotResourceWaiters:
			out = append(out, ContentionAdvice{Resource: res, Pattern: "hot_resource", Recommendation: "shard",
				Reason: fmt.Sprintf("%d distinct waiters on %q; split hot key across shards", len(waiters), res)})
		case wide:
			out = append(out, ContentionAdvice{Resource: res, Pattern: "wrong_granularity", Recommendation: "partition",
				Reason: fmt.Sprintf("holder guards %d+ resources including %q; narrow lock scope per partition", WideGranularityHolders, res)})
		case avgHold > LongCriticalSectionMs:
			out = append(out, ContentionAdvice{Resource: res, Pattern: "long_critical_section", Recommendation: "shorten",
				Reason: fmt.Sprintf("avg hold %dms on %q exceeds %dms; move work outside the critical section", avgHold, res, LongCriticalSectionMs)})
		default:
			out = append(out, ContentionAdvice{Resource: res, Pattern: "queued_contention", Recommendation: "serialize-queue",
				Reason: fmt.Sprintf("%d waiters on %q with avg hold %dms; drain via a serialized queue", len(waiters), res, avgHold)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	if out == nil {
		out = []ContentionAdvice{}
	}
	return out
}
