// Package recon implements the real-time banking reconciliation network.
//
// Sources reconciled per settlement batch: processor report,
// payment-service records, internal ledger entries and settlement file
// lines. Each settlement reference is evaluated to one of
// MATCHED / MISMATCH / PARTIAL / UNKNOWN subject to reconciliation
// windows (only items inside the window are eligible) and tolerance
// rules (amount + currency aware minor-unit epsilon per rail).
//
// The Engine is mutex-guarded and keeps an append-only hash-chained
// audit trail: every entry stores PrevHash + Hash where
// Hash = sha256hex(prevHash + "|" + payload).
package recon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Amount is an integer minor-units value with an ISO currency code.
// Never use floats for money.
type Amount struct {
	MinorUnits int64  `json:"minor_units"`
	Currency   string `json:"currency"`
}

// Source identifies one of the four legs reconciled per batch.
type Source string

const (
	SourceProcessorReport Source = "processor_report"
	SourcePaymentRecords  Source = "payment_service"
	SourceLedger          Source = "ledger"
	SourceSettlementFile  Source = "settlement_file"
)

// AllSources lists every leg expected for a full match.
var AllSources = []Source{
	SourceProcessorReport,
	SourcePaymentRecords,
	SourceLedger,
	SourceSettlementFile,
}

// Validate rejects unknown sources.
func (s Source) Validate() error {
	switch s {
	case SourceProcessorReport, SourcePaymentRecords, SourceLedger, SourceSettlementFile:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidInput, string(s))
	}
}

// ReconItem is a single leg observation for a settlement reference.
type ReconItem struct {
	Reference string    `json:"reference"`
	Amount    Amount    `json:"amount"`
	Timestamp time.Time `json:"timestamp"`
	Rail      string    `json:"rail,omitempty"`
}

// Outcome is the per-reference reconciliation verdict.
type Outcome string

const (
	OutcomeMatched  Outcome = "MATCHED"
	OutcomeMismatch Outcome = "MISMATCH"
	OutcomePartial  Outcome = "PARTIAL"
	OutcomeUnknown  Outcome = "UNKNOWN"
)

// ToleranceRule allows an absolute minor-unit epsilon for a currency,
// optionally scoped to a single rail (empty Rail matches any rail).
type ToleranceRule struct {
	Currency          string `json:"currency"`
	Rail              string `json:"rail,omitempty"`
	EpsilonMinorUnits int64  `json:"epsilon_minor_units"`
}

// Window bounds eligibility: only items whose timestamp falls inside
// the window are eligible. Zero From/To means unbounded on that side.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Contains reports whether t is inside the window (inclusive).
func (w Window) Contains(t time.Time) bool {
	if !w.From.IsZero() && t.Before(w.From) {
		return false
	}
	if !w.To.IsZero() && t.After(w.To) {
		return false
	}
	return true
}

// Validate rejects inverted windows.
func (w Window) Validate() error {
	if !w.From.IsZero() && !w.To.IsZero() && w.From.After(w.To) {
		return fmt.Errorf("%w: window from %s after to %s", ErrInvalidInput, w.From, w.To)
	}
	return nil
}

// Batch groups one settlement reconciliation run.
type Batch struct {
	ID         string          `json:"id"`
	Window     Window          `json:"window"`
	Tolerances []ToleranceRule `json:"tolerances"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ExceptionStatus tracks the exception lifecycle.
type ExceptionStatus string

const (
	ExceptionOpen     ExceptionStatus = "OPEN"
	ExceptionAcked    ExceptionStatus = "ACKNOWLEDGED"
	ExceptionResolved ExceptionStatus = "RESOLVED"
)

// ExceptionCase is one non-matched reference awaiting operations.
type ExceptionCase struct {
	ID         string          `json:"id"`
	BatchID    string          `json:"batch_id"`
	Reference  string          `json:"reference"`
	Outcome    Outcome         `json:"outcome"`
	Reason     string          `json:"reason"`
	Status     ExceptionStatus `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Resolution string          `json:"resolution,omitempty"`
}

// Correction is an idempotent adjustment posting. The ID is the
// idempotency key: re-posting the same ID returns the original entry.
type Correction struct {
	ID        string    `json:"id"`
	BatchID   string    `json:"batch_id"`
	Reference string    `json:"reference"`
	Amount    Amount    `json:"amount"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// RepairProposal is an automatic fix suggested for a sub-tolerance
// break (non-zero diff still inside the epsilon).
type RepairProposal struct {
	BatchID            string     `json:"batch_id"`
	Reference          string     `json:"reference"`
	ExpectedMinorUnits int64      `json:"expected_minor_units"`
	ActualMinorUnits   int64      `json:"actual_minor_units"`
	DiffMinorUnits     int64      `json:"diff_minor_units"`
	Currency           string     `json:"currency"`
	Suggested          Correction `json:"suggested"`
	Reason             string     `json:"reason"`
}

// AuditEntry is one append-only hash-chained audit record.
type AuditEntry struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	Action    string    `json:"action"`
	BatchID   string    `json:"batch_id"`
	Reference string    `json:"reference"`
	Payload   string    `json:"payload"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

// BatchSummary counts outcomes for a batch.
type BatchSummary struct {
	BatchID        string `json:"batch_id"`
	Total          int    `json:"total"`
	Matched        int    `json:"matched"`
	Mismatch       int    `json:"mismatch"`
	Partial        int    `json:"partial"`
	Unknown        int    `json:"unknown"`
	OpenExceptions int    `json:"open_exceptions"`
	Corrections    int    `json:"corrections"`
	Ran            bool   `json:"ran"`
}

var (
	ErrBatchExists        = errors.New("batch already exists")
	ErrBatchNotFound      = errors.New("batch not found")
	ErrInvalidInput       = errors.New("invalid input")
	ErrExceptionNotFound  = errors.New("exception not found")
	ErrExceptionResolved  = errors.New("exception already resolved")
	ErrCorrectionNotFound = errors.New("correction not found")
)

// Engine is the mutex-guarded reconciliation core.
type Engine struct {
	mu            sync.RWMutex
	batches       map[string]*Batch
	items         map[string]map[Source][]ReconItem
	outcomes      map[string]map[string]Outcome
	outcomeDetail map[string]map[string]string
	diffs         map[string]map[string]int64
	ran           map[string]bool
	exceptions    map[string]*ExceptionCase
	excByBatchRef map[string]string
	corrections   map[string]*Correction
	audit         []AuditEntry
	seq           int64
	lastHash      string
}

// NewEngine returns an empty in-memory engine.
func NewEngine() *Engine {
	return &Engine{
		batches:       make(map[string]*Batch),
		items:         make(map[string]map[Source][]ReconItem),
		outcomes:      make(map[string]map[string]Outcome),
		outcomeDetail: make(map[string]map[string]string),
		diffs:         make(map[string]map[string]int64),
		ran:           make(map[string]bool),
		exceptions:    make(map[string]*ExceptionCase),
		excByBatchRef: make(map[string]string),
		corrections:   make(map[string]*Correction),
		audit:         make([]AuditEntry, 0),
	}
}

func computeHash(prev, payload string) string {
	sum := sha256.Sum256([]byte(prev + "|" + payload))
	return hex.EncodeToString(sum[:])
}

func excKey(batchID, reference string) string {
	return batchID + "\x00" + reference
}

// appendAuditLocked appends one hash-chained entry. Caller must hold the write lock.
func (e *Engine) appendAuditLocked(action, batchID, reference, detail string) AuditEntry {
	e.seq++
	at := time.Now().UTC()
	payload := fmt.Sprintf("%d|%s|%s|%s|%s|%s", e.seq, action, batchID, reference, detail, at.Format(time.RFC3339Nano))
	hash := computeHash(e.lastHash, payload)
	entry := AuditEntry{
		Seq:       e.seq,
		At:        at,
		Action:    action,
		BatchID:   batchID,
		Reference: reference,
		Payload:   payload,
		PrevHash:  e.lastHash,
		Hash:      hash,
	}
	e.audit = append(e.audit, entry)
	e.lastHash = hash
	return entry
}

// OpenBatch creates a settlement batch with its window and tolerances.
func (e *Engine) OpenBatch(id string, window Window, tolerances []ToleranceRule) (*Batch, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: batch id is required", ErrInvalidInput)
	}
	if err := window.Validate(); err != nil {
		return nil, err
	}
	for i, t := range tolerances {
		if t.Currency == "" {
			return nil, fmt.Errorf("%w: tolerance[%d] currency is required", ErrInvalidInput, i)
		}
		if t.EpsilonMinorUnits < 0 {
			return nil, fmt.Errorf("%w: tolerance[%d] epsilon must be >= 0", ErrInvalidInput, i)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.batches[id]; ok {
		return nil, ErrBatchExists
	}
	cp := make([]ToleranceRule, len(tolerances))
	copy(cp, tolerances)
	b := &Batch{ID: id, Window: window, Tolerances: cp, CreatedAt: time.Now().UTC()}
	e.batches[id] = b
	e.items[id] = make(map[Source][]ReconItem)
	e.outcomes[id] = make(map[string]Outcome)
	e.outcomeDetail[id] = make(map[string]string)
	e.diffs[id] = make(map[string]int64)
	e.ran[id] = false
	e.appendAuditLocked("open_batch", id, "", fmt.Sprintf("tolerances=%d", len(cp)))
	out := *b
	out.Tolerances = append([]ToleranceRule(nil), cp...)
	return &out, nil
}

// Ingest appends legs for one source into a batch.
func (e *Engine) Ingest(batchID string, source Source, items []ReconItem) error {
	if err := source.Validate(); err != nil {
		return err
	}
	for i, it := range items {
		if it.Reference == "" {
			return fmt.Errorf("%w: item[%d] reference is required", ErrInvalidInput, i)
		}
		if it.Amount.Currency == "" {
			return fmt.Errorf("%w: item[%d] currency is required", ErrInvalidInput, i)
		}
		if it.Timestamp.IsZero() {
			return fmt.Errorf("%w: item[%d] timestamp is required", ErrInvalidInput, i)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.batches[batchID]; !ok {
		return ErrBatchNotFound
	}
	cp := make([]ReconItem, len(items))
	copy(cp, items)
	e.items[batchID][source] = append(e.items[batchID][source], cp...)
	e.appendAuditLocked("ingest", batchID, "", fmt.Sprintf("source=%s count=%d", string(source), len(items)))
	return nil
}

// toleranceFor returns the applicable epsilon for currency+rail: exact
// rail match first, then currency-only, else zero.
func toleranceFor(rules []ToleranceRule, currency, rail string) int64 {
	best := int64(-1)
	for _, r := range rules {
		if r.Currency != currency {
			continue
		}
		if r.Rail != "" && r.Rail == rail {
			if r.EpsilonMinorUnits > best {
				best = r.EpsilonMinorUnits
			}
		}
	}
	if best >= 0 {
		return best
	}
	for _, r := range rules {
		if r.Currency == currency && r.Rail == "" {
			if r.EpsilonMinorUnits > best {
				best = r.EpsilonMinorUnits
			}
		}
	}
	if best >= 0 {
		return best
	}
	return 0
}

// Run evaluates every reference in the batch and refreshes the exception queue.
func (e *Engine) Run(batchID string) (map[string]Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.batches[batchID]
	if !ok {
		return nil, ErrBatchNotFound
	}
	// Collect legs per reference: first item per source wins for evaluation.
	byRef := make(map[string]map[Source]ReconItem)
	for src, list := range e.items[batchID] {
		for _, it := range list {
			m, ok := byRef[it.Reference]
			if !ok {
				m = make(map[Source]ReconItem)
				byRef[it.Reference] = m
			}
			if _, seen := m[src]; !seen {
				m[src] = it
			}
		}
	}
	out := make(map[string]Outcome, len(byRef))
	detail := make(map[string]string, len(byRef))
	diffs := make(map[string]int64, len(byRef))
	matched, mismatch, partial, unknown := 0, 0, 0, 0
	for ref, legs := range byRef {
		oc, det, diff := evaluateReference(b, legs)
		out[ref] = oc
		detail[ref] = det
		diffs[ref] = diff
		switch oc {
		case OutcomeMatched:
			matched++
		case OutcomeMismatch:
			mismatch++
		case OutcomePartial:
			partial++
		default:
			unknown++
		}
		_ = ref
	}
	e.outcomes[batchID] = out
	e.outcomeDetail[batchID] = detail
	e.diffs[batchID] = diffs
	e.ran[batchID] = true
	e.upsertExceptionsLocked(batchID, out, detail)
	e.appendAuditLocked("run", batchID, "", fmt.Sprintf("total=%d matched=%d mismatch=%d partial=%d unknown=%d", len(out), matched, mismatch, partial, unknown))
	cp := make(map[string]Outcome, len(out))
	for k, v := range out {
		cp[k] = v
	}
	return cp, nil
}

func evaluateReference(b *Batch, legs map[Source]ReconItem) (Outcome, string, int64) {
	// Window eligibility: at least one leg must fall inside the window.
	eligible := false
	for _, it := range legs {
		if b.Window.Contains(it.Timestamp) {
			eligible = true
			break
		}
	}
	if !eligible {
		return OutcomeUnknown, "window_excluded", 0
	}
	if len(legs) == 1 {
		return OutcomeUnknown, "single_source", 0
	}
	if _, ok := legs[SourceSettlementFile]; !ok {
		return OutcomePartial, "missing_settlement_leg", 0
	}
	// Currency must agree across all legs.
	currency := ""
	for _, it := range legs {
		if currency == "" {
			currency = it.Amount.Currency
		} else if it.Amount.Currency != currency {
			return OutcomeMismatch, "currency_mismatch", 0
		}
	}
	minV, maxV := int64(0), int64(0)
	first := true
	maxEps := int64(0)
	for _, it := range legs {
		v := it.Amount.MinorUnits
		if first {
			minV, maxV, first = v, v, false
		} else {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		eps := toleranceFor(b.Tolerances, it.Amount.Currency, it.Rail)
		if eps > maxEps {
			maxEps = eps
		}
	}
	diff := maxV - minV
	if diff < 0 {
		diff = -diff
	}
	if diff <= maxEps {
		if diff == 0 {
			return OutcomeMatched, "exact_match", 0
		}
		return OutcomeMatched, fmt.Sprintf("within_tolerance diff=%d eps=%d", diff, maxEps), diff
	}
	return OutcomeMismatch, fmt.Sprintf("amount_break diff=%d eps=%d", diff, maxEps), diff
}

func (e *Engine) upsertExceptionsLocked(batchID string, outcomes map[string]Outcome, details map[string]string) {
	now := time.Now().UTC()
	for ref, oc := range outcomes {
		key := excKey(batchID, ref)
		if oc == OutcomeMatched {
			if excID, ok := e.excByBatchRef[key]; ok {
				if ex, ok := e.exceptions[excID]; ok && ex.Status != ExceptionResolved {
					ex.Status = ExceptionResolved
					ex.Resolution = "auto-cleared: matched on re-run"
					ex.UpdatedAt = now
					e.appendAuditLocked("exception_auto_resolve", batchID, ref, fmt.Sprintf("exception=%s", ex.ID))
				}
			}
			continue
		}
		reason := details[ref]
		if excID, ok := e.excByBatchRef[key]; ok {
			if ex, ok := e.exceptions[excID]; ok {
				if ex.Status != ExceptionResolved {
					ex.Outcome = oc
					ex.Reason = reason
					ex.UpdatedAt = now
					continue
				}
				// Previously resolved but broke again: reopen a new case.
			}
		}
		id := uuid.NewString()
		ex := &ExceptionCase{
			ID:        id,
			BatchID:   batchID,
			Reference: ref,
			Outcome:   oc,
			Reason:    reason,
			Status:    ExceptionOpen,
			CreatedAt: now,
			UpdatedAt: now,
		}
		e.exceptions[id] = ex
		e.excByBatchRef[key] = id
		e.appendAuditLocked("exception_open", batchID, ref, fmt.Sprintf("exception=%s outcome=%s reason=%s", id, string(oc), reason))
	}
}

// ProposeRepairs returns automatic fix proposals for sub-tolerance
// breaks: references whose diff is non-zero but still within epsilon.
func (e *Engine) ProposeRepairs(batchID string) ([]RepairProposal, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	b, ok := e.batches[batchID]
	if !ok {
		return nil, ErrBatchNotFound
	}
	out, ok := e.outcomes[batchID]
	if !ok || !e.ran[batchID] {
		return []RepairProposal{}, nil
	}
	// Rebuild legs to compute expected vs settlement amounts.
	byRef := make(map[string]map[Source]ReconItem)
	for src, list := range e.items[batchID] {
		for _, it := range list {
			m, ok := byRef[it.Reference]
			if !ok {
				m = make(map[Source]ReconItem)
				byRef[it.Reference] = m
			}
			if _, seen := m[src]; !seen {
				m[src] = it
			}
		}
	}
	var out2 []RepairProposal
	for ref, oc := range out {
		if oc != OutcomeMatched {
			continue
		}
		diff := e.diffs[batchID][ref]
		if diff == 0 {
			continue
		}
		legs := byRef[ref]
		expected := expectedAmount(legs)
		settled := legs[SourceSettlementFile].Amount
		adjustment := expected.MinorUnits - settled.MinorUnits
		if adjustment == 0 {
			continue
		}
		// Sanity: only propose when still within tolerance.
		eps := int64(0)
		for _, it := range legs {
			if v := toleranceFor(b.Tolerances, it.Amount.Currency, it.Rail); v > eps {
				eps = v
			}
		}
		if diff > eps {
			continue
		}
		out2 = append(out2, RepairProposal{
			BatchID:            batchID,
			Reference:          ref,
			ExpectedMinorUnits: expected.MinorUnits,
			ActualMinorUnits:   settled.MinorUnits,
			DiffMinorUnits:     diff,
			Currency:           expected.Currency,
			Suggested: Correction{
				ID:        fmt.Sprintf("%s:%s:repair", batchID, ref),
				BatchID:   batchID,
				Reference: ref,
				Amount:    Amount{MinorUnits: adjustment, Currency: expected.Currency},
				Reason:    fmt.Sprintf("sub-tolerance auto-repair diff=%d eps=%d", diff, eps),
			},
			Reason: fmt.Sprintf("sub-tolerance break diff=%d eps=%d", diff, eps),
		})
	}
	sort.Slice(out2, func(i, j int) bool { return out2[i].Reference < out2[j].Reference })
	if out2 == nil {
		out2 = []RepairProposal{}
	}
	return out2, nil
}

// expectedAmount picks the canonical amount: payment-service leg first,
// then processor, ledger, settlement.
func expectedAmount(legs map[Source]ReconItem) Amount {
	for _, s := range []Source{SourcePaymentRecords, SourceProcessorReport, SourceLedger, SourceSettlementFile} {
		if it, ok := legs[s]; ok {
			return it.Amount
		}
	}
	return Amount{}
}

// PostCorrection stores a correction idempotently by correction ID.
func (e *Engine) PostCorrection(c Correction) (*Correction, error) {
	if c.ID == "" {
		return nil, fmt.Errorf("%w: correction id is required", ErrInvalidInput)
	}
	if c.BatchID == "" || c.Reference == "" {
		return nil, fmt.Errorf("%w: correction batch_id and reference are required", ErrInvalidInput)
	}
	if c.Amount.Currency == "" {
		return nil, fmt.Errorf("%w: correction currency is required", ErrInvalidInput)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.batches[c.BatchID]; !ok {
		return nil, ErrBatchNotFound
	}
	if existing, ok := e.corrections[c.ID]; ok {
		cp := *existing
		return &cp, nil
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	cp := c
	e.corrections[c.ID] = &cp
	e.appendAuditLocked("post_correction", c.BatchID, c.Reference, fmt.Sprintf("correction=%s amount=%d %s", c.ID, c.Amount.MinorUnits, c.Amount.Currency))
	out := cp
	return &out, nil
}

// AcknowledgeException moves an OPEN exception to ACKNOWLEDGED (idempotent).
func (e *Engine) AcknowledgeException(id string) (*ExceptionCase, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: exception id is required", ErrInvalidInput)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ex, ok := e.exceptions[id]
	if !ok {
		return nil, ErrExceptionNotFound
	}
	if ex.Status == ExceptionResolved {
		return nil, ErrExceptionResolved
	}
	if ex.Status == ExceptionAcked {
		cp := *ex
		return &cp, nil
	}
	ex.Status = ExceptionAcked
	ex.UpdatedAt = time.Now().UTC()
	e.appendAuditLocked("exception_ack", ex.BatchID, ex.Reference, fmt.Sprintf("exception=%s", ex.ID))
	cp := *ex
	return &cp, nil
}

// ResolveException moves an exception to RESOLVED (idempotent).
func (e *Engine) ResolveException(id string, resolution string) (*ExceptionCase, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: exception id is required", ErrInvalidInput)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ex, ok := e.exceptions[id]
	if !ok {
		return nil, ErrExceptionNotFound
	}
	if ex.Status == ExceptionResolved {
		cp := *ex
		return &cp, nil
	}
	if resolution == "" {
		resolution = "resolved"
	}
	ex.Status = ExceptionResolved
	ex.Resolution = resolution
	ex.UpdatedAt = time.Now().UTC()
	e.appendAuditLocked("exception_resolve", ex.BatchID, ex.Reference, fmt.Sprintf("exception=%s resolution=%s", ex.ID, resolution))
	cp := *ex
	return &cp, nil
}

// ExceptionQueue returns all non-resolved exceptions sorted by ID.
func (e *Engine) ExceptionQueue() []*ExceptionCase {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var q []*ExceptionCase
	for _, ex := range e.exceptions {
		if ex.Status == ExceptionResolved {
			continue
		}
		cp := *ex
		q = append(q, &cp)
	}
	sort.Slice(q, func(i, j int) bool { return q[i].ID < q[j].ID })
	if q == nil {
		q = []*ExceptionCase{}
	}
	return q
}

// ExceptionsForBatch returns non-resolved exceptions for one batch.
func (e *Engine) ExceptionsForBatch(batchID string) []*ExceptionCase {
	all := e.ExceptionQueue()
	var filtered []*ExceptionCase
	for _, ex := range all {
		if ex.BatchID == batchID {
			filtered = append(filtered, ex)
		}
	}
	if filtered == nil {
		filtered = []*ExceptionCase{}
	}
	return filtered
}

// AuditTrail returns hash-chained entries; empty batchID returns all.
func (e *Engine) AuditTrail(batchID string) []AuditEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if batchID == "" {
		out := make([]AuditEntry, len(e.audit))
		copy(out, e.audit)
		return out
	}
	var out []AuditEntry
	for _, a := range e.audit {
		if a.BatchID == batchID {
			out = append(out, a)
		}
	}
	if out == nil {
		out = []AuditEntry{}
	}
	return out
}

// BatchSummary counts outcomes plus open exceptions and corrections.
func (e *Engine) BatchSummary(batchID string) (*BatchSummary, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.batches[batchID]; !ok {
		return nil, ErrBatchNotFound
	}
	s := &BatchSummary{BatchID: batchID, Ran: e.ran[batchID]}
	if out, ok := e.outcomes[batchID]; ok {
		for _, oc := range out {
			s.Total++
			switch oc {
			case OutcomeMatched:
				s.Matched++
			case OutcomeMismatch:
				s.Mismatch++
			case OutcomePartial:
				s.Partial++
			default:
				s.Unknown++
			}
		}
	}
	// If never ran, total is the distinct ingested reference count.
	if !s.Ran {
		seen := make(map[string]struct{})
		for _, list := range e.items[batchID] {
			for _, it := range list {
				seen[it.Reference] = struct{}{}
			}
		}
		s.Total = len(seen)
	}
	for _, ex := range e.exceptions {
		if ex.BatchID == batchID && ex.Status != ExceptionResolved {
			s.OpenExceptions++
		}
	}
	for _, c := range e.corrections {
		if c.BatchID == batchID {
			s.Corrections++
		}
	}
	return s, nil
}

// VerifyChain checks the append-only hash chain integrity.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return verifyEntries(e.audit)
}

// VerifyBatchChain checks the chain restricted to one batch plus global
// link integrity across the full trail.
func (e *Engine) VerifyBatchChain(batchID string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for _, a := range e.audit {
		if computeHash(a.PrevHash, a.Payload) != a.Hash {
			return fmt.Errorf("audit entry %d hash mismatch", a.Seq)
		}
		if a.Seq > 1 && a.PrevHash != prev {
			return fmt.Errorf("audit entry %d prev-hash link broken", a.Seq)
		}
		prev = a.Hash
	}
	_ = batchID
	return nil
}

func verifyEntries(entries []AuditEntry) error {
	prev := ""
	for i, a := range entries {
		if a.Seq != int64(i+1) {
			return fmt.Errorf("audit entry %d has seq %d, want %d", i, a.Seq, i+1)
		}
		if a.PrevHash != prev {
			return fmt.Errorf("audit entry %d prev-hash link broken", a.Seq)
		}
		if computeHash(a.PrevHash, a.Payload) != a.Hash {
			return fmt.Errorf("audit entry %d hash mismatch", a.Seq)
		}
		prev = a.Hash
	}
	return nil
}
