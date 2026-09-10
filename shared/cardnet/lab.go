package cardnet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Replay / Simulation Lab ─────────────────────────────────────────────────

// LabResult summarises a replay run.
type LabResult struct {
	Total      int           `json:"total"`
	Approved   int           `json:"approved"`
	Declined   int           `json:"declined"`
	Malformed  int           `json:"malformed"`
	Duration   time.Duration `json:"duration"`
	Throughput float64       `json:"throughput_per_sec"`
	// ByType counts per canonical message type.
	ByType map[MessageType]int `json:"by_type"`
}

// Lab replays recorded network messages through the gateway at scale — the
// card-network protocol laboratory: AUTH/REVERSAL/CAPTURE/REFUND/ADVICE
// sequences, 100K→10M messages, fully offline.
type Lab struct {
	gateway *Gateway
}

func NewLab(gateway *Gateway) *Lab { return &Lab{gateway: gateway} }

// Replay processes raw (version, fields) pairs and reports aggregate results.
// Malformed messages are COUNTED, never fatal — the lab exists to find them.
func (l *Lab) Replay(messages []RawMessage) LabResult {
	start := time.Now()
	res := LabResult{ByType: map[MessageType]int{}}
	for _, raw := range messages {
		msg, err := l.gateway.Parse(raw.FormatVersion, raw.Fields)
		if err != nil {
			res.Malformed++
			continue
		}
		res.Total++
		res.ByType[msg.Type]++
		if msg.Approved() {
			res.Approved++
		} else {
			res.Declined++
		}
	}
	res.Duration = time.Since(start)
	if secs := res.Duration.Seconds(); secs > 0 {
		res.Throughput = float64(res.Total) / secs
	}
	return res
}

// RawMessage is one recorded network message.
type RawMessage struct {
	FormatVersion string            `json:"format_version"`
	Fields        map[string]string `json:"fields"`
}

// Generate produces synthetic message sequences for load testing: n
// transactions each with AUTH → CAPTURE, plus a configurable reversal rate.
func Generate(n int, reversalRatePct int, seed func(i int) string) []RawMessage {
	out := make([]RawMessage, 0, n*2)
	for i := 0; i < n; i++ {
		stan := seed(i)
		amount := int64(100 + i%9900) // £1.00–£100.00
		out = append(out, RawMessage{FormatVersion: "v2", Fields: map[string]string{
			"mti": "0100", "amount": fmt.Sprintf("%d.%02d", amount/100, amount%100),
			"currency": "GBP", "stan": stan, "rrn": "RRN" + stan,
			"response_code": "00", "acquirer": "ACQ1", "terminal": "T" + stan,
			"merchant": "M" + stan, "mcc": "5411",
		}})
		out = append(out, RawMessage{FormatVersion: "v2", Fields: map[string]string{
			"mti": "0200", "amount": fmt.Sprintf("%d.%02d", amount/100, amount%100),
			"currency": "GBP", "stan": stan, "rrn": "RRN" + stan,
			"response_code": "00", "acquirer": "ACQ1", "terminal": "T" + stan,
			"merchant": "M" + stan, "mcc": "5411",
		}})
		if i%100 < reversalRatePct {
			out = append(out, RawMessage{FormatVersion: "v2", Fields: map[string]string{
				"mti": "0400", "amount": fmt.Sprintf("%d.%02d", amount/100, amount%100),
				"currency": "GBP", "stan": stan, "rrn": "RRN" + stan,
				"response_code": "00", "acquirer": "ACQ1", "terminal": "T" + stan,
				"merchant": "M" + stan, "mcc": "5411",
			}})
		}
	}
	return out
}

// ── Correlation Engine ──────────────────────────────────────────────────────

// CorrelationKey is one hop in the transaction identity graph.
type CorrelationKey struct {
	Kind  string `json:"kind"` // "app_payment","internal_payment","network_trace","processor_ref","settlement_ref"
	Value string `json:"value"`
}

// CorrelationRecord links one key to another with provenance.
type CorrelationRecord struct {
	From CorrelationKey `json:"from"`
	To   CorrelationKey `json:"to"`
	At   time.Time      `json:"at"`
}

// CorrelationEngine maintains the app→payment→network→processor→settlement
// graph so support can answer "show me the complete lifecycle of this card
// transaction" from any single identifier.
type CorrelationEngine struct {
	mu     sync.RWMutex
	links  map[string][]CorrelationKey // key string → linked keys
	record map[string][]CorrelationRecord
}

func NewCorrelationEngine() *CorrelationEngine {
	return &CorrelationEngine{links: map[string][]CorrelationKey{}, record: map[string][]CorrelationRecord{}}
}

func keyString(k CorrelationKey) string { return k.Kind + ":" + k.Value }

// Link records a bidirectional edge between two identifiers.
func (e *CorrelationEngine) Link(from, to CorrelationKey, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ks, ts := keyString(from), keyString(to)
	e.links[ks] = appendUnique(e.links[ks], to)
	e.links[ts] = appendUnique(e.links[ts], from)
	e.record[ks] = append(e.record[ks], CorrelationRecord{From: from, To: to, At: at})
	e.record[ts] = append(e.record[ts], CorrelationRecord{From: to, To: from, At: at})
}

// Trace returns every identifier reachable from the given key (BFS across the
// graph), deterministically ordered.
func (e *CorrelationEngine) Trace(from CorrelationKey) []CorrelationKey {
	e.mu.RLock()
	defer e.mu.RUnlock()
	seen := map[string]bool{keyString(from): true}
	queue := []CorrelationKey{from}
	var out []CorrelationKey
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for _, next := range e.links[keyString(cur)] {
			if !seen[keyString(next)] {
				seen[keyString(next)] = true
				queue = append(queue, next)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return keyString(out[i]) < keyString(out[j]) })
	return out
}

// Lifecycle renders the ordered timeline of identity links involving a key.
func (e *CorrelationEngine) Lifecycle(from CorrelationKey) []CorrelationRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	recs := append([]CorrelationRecord(nil), e.record[keyString(from)]...)
	sort.Slice(recs, func(i, j int) bool { return recs[i].At.Before(recs[j].At) })
	return recs
}

func appendUnique(list []CorrelationKey, k CorrelationKey) []CorrelationKey {
	ks := keyString(k)
	for _, existing := range list {
		if keyString(existing) == ks {
			return list
		}
	}
	return append(list, k)
}

// ── Advice Processor: out-of-order network message sequencing ───────────────

var ErrStaleMessage = errors.New("stale network message superseded by a newer one")

// AdviceOutcome describes what happened to an out-of-band message.
type AdviceOutcome string

const (
	AdviceApplied AdviceOutcome = "APPLIED"
	AdviceStale   AdviceOutcome = "STALE_SUPERSEDED"
	AdviceGap     AdviceOutcome = "SEQUENCE_GAP_HELD"
)

// AdviceResult reports sequencing state after applying a message.
type AdviceResult struct {
	Outcome       AdviceOutcome `json:"outcome"`
	Sequence      int64         `json:"sequence"`
	AppliedSeq    int64         `json:"applied_sequence"`
	ReversalPct   int           `json:"reversal_pct,omitempty"`
	ReversalMinor int64         `json:"reversal_minor,omitempty"`
	FinalState    string        `json:"final_state"`
}

// AdviceTracker sequences asynchronous advices (reversals, updates) per
// transaction. Networks may deliver 0400/0620 messages out of order and more
// than once; correctness comes from monotone sequence numbers + network
// timestamps, NOT arrival order.
type AdviceTracker struct {
	mu           sync.Mutex
	lastSeq      map[string]int64
	state        map[string]string
	held         map[string][]heldAdvice
	lastReversal map[string]reversalState
}

type heldAdvice struct {
	seq   int64
	apply func() error
}

type reversalState struct {
	pct   int
	minor int64
}

func NewAdviceTracker() *AdviceTracker {
	return &AdviceTracker{
		lastSeq: map[string]int64{}, state: map[string]string{},
		held: map[string][]heldAdvice{}, lastReversal: map[string]reversalState{},
	}
}

// Apply processes an advice with its network sequence number. Rules:
//   - seq <= lastSeq: STALE — superseded, must not mutate state twice.
//   - seq == lastSeq+1: apply now.
//   - seq > lastSeq+1: hold until the gap fills (gap = missing message).
func (t *AdviceTracker) Apply(txnRef string, seq int64, apply func() error) (AdviceResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	last := t.lastSeq[txnRef]

	if seq <= last {
		return AdviceResult{Outcome: AdviceStale, Sequence: seq, AppliedSeq: last,
			FinalState: t.state[txnRef]}, nil
	}
	if seq > last+1 {
		t.held[txnRef] = append(t.held[txnRef], heldAdvice{seq: seq, apply: apply})
		return AdviceResult{Outcome: AdviceGap, Sequence: seq, AppliedSeq: last,
			FinalState: t.state[txnRef]}, nil
	}

	// In-order: apply.
	if err := safeApply(apply); err != nil {
		return AdviceResult{}, err
	}
	t.lastSeq[txnRef] = seq
	// Drain any held messages that are now contiguous.
	for {
		drained := false
		kept := t.held[txnRef][:0]
		for _, h := range t.held[txnRef] {
			if h.seq == t.lastSeq[txnRef]+1 {
				if err := safeApply(h.apply); err != nil {
					t.held[txnRef] = kept
					return AdviceResult{}, err
				}
				t.lastSeq[txnRef] = h.seq
				drained = true
			} else {
				kept = append(kept, h)
			}
		}
		t.held[txnRef] = kept
		if !drained {
			break
		}
	}
	return AdviceResult{Outcome: AdviceApplied, Sequence: seq, AppliedSeq: t.lastSeq[txnRef],
		FinalState: t.state[txnRef]}, nil
}

func safeApply(f func() error) error {
	if f == nil {
		return nil
	}
	return f()
}

// RecordReversal notes a reversal's magnitude (full or partial) against the
// original authorization amount.
func (t *AdviceTracker) RecordReversal(txnRef string, originalMinor, reversalMinor int64) error {
	if originalMinor <= 0 {
		return errors.New("original amount must be positive")
	}
	if reversalMinor < 0 || reversalMinor > originalMinor {
		return fmt.Errorf("reversal %d out of range (0, %d]", reversalMinor, originalMinor)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	pct := int(reversalMinor * 100 / originalMinor)
	t.lastReversal[txnRef] = reversalState{pct: pct, minor: reversalMinor}
	if pct >= 100 {
		t.state[txnRef] = "REVERSED"
	} else {
		t.state[txnRef] = "PARTIALLY_REVERSED"
	}
	return nil
}

// State returns the tracked lifecycle state for a transaction.
func (t *AdviceTracker) State(txnRef string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state[txnRef]
}

// PendingGaps reports transactions with held messages (observability).
func (t *AdviceTracker) PendingGaps() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for k, held := range t.held {
		if len(held) > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ── Offline terminal transaction handling ───────────────────────────────────

// OfflineDecision is the outcome of late-presentment validation.
type OfflineDecision string

const (
	OfflineAccept      OfflineDecision = "ACCEPT"
	OfflineDuplicate   OfflineDecision = "DUPLICATE_SUPPRESSED"
	OfflineOverCeiling OfflineDecision = "REFUSE_OVER_CEILING"
	OfflineStale       OfflineDecision = "REFUSE_STALE_PRESENTMENT"
)

// OfflineHandler validates transactions presented after the fact (terminal
// was offline at T0, network reconnected at T+20m, message arrives T+25m).
type OfflineHandler struct {
	mu           sync.Mutex
	seenSTAN     map[string]bool
	ceilingMinor int64
	maxAge       time.Duration
}

func NewOfflineHandler(ceilingMinor int64, maxAge time.Duration) *OfflineHandler {
	return &OfflineHandler{seenSTAN: map[string]bool{}, ceilingMinor: ceilingMinor, maxAge: maxAge}
}

// Present validates a late-arriving transaction. Order matters: duplicate
// detection by STAN first (networks resend), then amount ceiling, then
// presentment age.
func (h *OfflineHandler) Present(msg *CanonicalMessage, now time.Time) OfflineDecision {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seenSTAN[msg.STAN] {
		return OfflineDuplicate
	}
	if msg.AmountMinor > h.ceilingMinor {
		return OfflineOverCeiling
	}
	if now.Sub(msg.TransmissionTime) > h.maxAge {
		return OfflineStale
	}
	h.seenSTAN[msg.STAN] = true
	return OfflineAccept
}

// SeenCount reports how many distinct offline transactions were accepted.
func (h *OfflineHandler) SeenCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seenSTAN)
}

// ── Contactless risk counters ───────────────────────────────────────────────

// ContactlessCounter tracks cumulative contactless usage per card with
// reconciliation for delayed network updates.
type ContactlessCounter struct {
	mu         sync.Mutex
	count      int
	amount     int64
	maxCount   int
	maxAmount  int64
	lastUpdate time.Time
}

func NewContactlessCounter(maxCount int, maxAmount int64) *ContactlessCounter {
	return &ContactlessCounter{maxCount: maxCount, maxAmount: maxAmount}
}

// CanSpend checks whether a tap of `amount` is permitted under cumulative
// count/amount limits.
func (c *ContactlessCounter) CanSpend(amount int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.count+1 > c.maxCount {
		return false
	}
	return c.amount+amount <= c.maxAmount
}

// Record applies a tap. Delayed network updates may arrive out of order; the
// monotone timestamp rule prevents double-counting older state.
func (c *ContactlessCounter) Record(amount int64, at time.Time) error {
	if !at.After(c.lastUpdate) && !c.lastUpdate.IsZero() {
		return fmt.Errorf("stale contactless update at %s (last %s)", at, c.lastUpdate)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	c.amount += amount
	c.lastUpdate = at
	return nil
}

// Reconcile corrects the counter against an authoritative snapshot (e.g. the
// issuer's own counter after a network partition) — takes the HIGHER values,
// never reduces below what we have already observed locally.
func (c *ContactlessCounter) Reconcile(count int, amount int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if count > c.count {
		c.count = count
	}
	if amount > c.amount {
		c.amount = amount
	}
}

// Snapshot exposes current counter state.
func (c *ContactlessCounter) Snapshot() (count int, amount int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count, c.amount
}

var _ = strings.TrimSpace // reserved for future field trimming
