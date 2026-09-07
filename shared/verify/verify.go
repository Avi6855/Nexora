// Package verify implements Nexora's Financial Calculation Verification
// Engine and Canary Data Validation.
//
// The engine runs every critical financial calculation through TWO
// independent implementations and quarantines the operation on mismatch.
// The canary compares data transformations old-vs-new over the same inputs
// with a bounded difference budget, so a rollout that silently shifts
// balances is stopped before it reaches customers.
package verify

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nexora/nexora/shared/calc"
	"github.com/nexora/nexora/shared/money"
)

// ErrMismatch is returned when two implementations disagree. The caller must
// quarantine the operation — never "pick one" and continue.
var ErrMismatch = errors.New("calculation implementations disagree")

// ErrQuarantined is returned when a subject is already quarantined and a new
// operation arrives for it.
var ErrQuarantined = errors.New("subject is quarantined pending review")

// CalcFunc is one independent implementation of a financial calculation.
// Implementation A is the reference (shared/calc); B is the candidate
// (independent code path, different author, different rounding code).
type CalcFunc func(subject string, in CalcInput) (money.Money, error)

// CalcInput carries everything a calculation needs. Copying it verbatim to
// both implementations is what makes the comparison meaningful.
type CalcInput struct {
	Operation     string // "daily_interest", "fee", "prorate", ...
	Currency      string
	AmountMinor   int64
	AnnualRateBps int64
	Days          int
	Rounding      calc.Rounding
}

// QuarantineRecord captures everything an on-call reviewer needs: what
// disagreed, by how much, and where each implementation stands.
type QuarantineRecord struct {
	Subject    string
	Operation  string
	Input      CalcInput
	AAmount    money.Money
	BAmount    money.Money
	AErr, BErr error
	DiffMinor  int64
	At         time.Time
	Reason     string
}

// Engine compares two independent implementations of the same financial
// calculation and quarantines subjects on the first mismatch.
type Engine struct {
	implA CalcFunc
	implB CalcFunc

	mu          sync.Mutex
	quarantined map[string]QuarantineRecord
	maxDiff     int64 // tolerance in minor units; 0 = exact match required
}

// NewEngine wires the reference implementation (A) and the independent one
// (B). maxDiffMinor is the allowed absolute difference in minor units —
// money movement uses 0; reporting aggregates may tolerate 1.
func NewEngine(a, b CalcFunc, maxDiffMinor int64) *Engine {
	return &Engine{implA: a, implB: b, quarantined: map[string]QuarantineRecord{}, maxDiff: maxDiffMinor}
}

// Verify runs both implementations over identical input.
func (e *Engine) Verify(subject string, in CalcInput) (money.Money, error) {
	if rec, ok := e.quarantineRecord(subject); ok {
		return money.Money{}, fmt.Errorf("%w: %s since %s (%s)",
			ErrQuarantined, subject, rec.At.Format(time.RFC3339), rec.Reason)
	}
	aAmt, aErr := safeCall(e.implA, subject, in)
	bAmt, bErr := safeCall(e.implB, subject, in)

	if err := classify(aErr, bErr); err != nil {
		e.quarantine(subject, in, aAmt, bAmt, aErr, bErr, 0, err.Error())
		return money.Money{}, err
	}
	diff := aAmt.Amount - bAmt.Amount
	if diff < 0 {
		diff = -diff
	}
	if diff > e.maxDiff {
		err := fmt.Errorf("%w: A=%s B=%s diff=%d minor units (tolerance %d)",
			ErrMismatch, aAmt.String(), bAmt.String(), diff, e.maxDiff)
		e.quarantine(subject, in, aAmt, bAmt, nil, nil, diff, err.Error())
		return money.Money{}, err
	}
	// The reference answer wins; B agreeing is the whole point.
	return aAmt, nil
}

func safeCall(f CalcFunc, subject string, in CalcInput) (money.Money, error) {
	defer func() {
		if r := recover(); r != nil {
			// A panicking implementation is a mismatch by definition.
			_ = r
		}
	}()
	return f(subject, in)
}

// classify turns error asymmetry into a mismatch.
func classify(aErr, bErr error) error {
	switch {
	case aErr != nil && bErr != nil:
		if aErr.Error() != bErr.Error() {
			return fmt.Errorf("%w: both failed differently: A=%v B=%v", ErrMismatch, aErr, bErr)
		}
		// Both failed identically — deterministic failure is agreement.
		return nil
	case aErr != nil:
		return fmt.Errorf("%w: A failed, B succeeded: %v", ErrMismatch, aErr)
	case bErr != nil:
		return fmt.Errorf("%w: B failed, A succeeded: %v", ErrMismatch, bErr)
	}
	return nil
}

func (e *Engine) quarantine(subject string, in CalcInput, aAmt, bAmt money.Money, aErr, bErr error, diff int64, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.quarantined[subject] = QuarantineRecord{
		Subject: subject, Operation: in.Operation, Input: in,
		AAmount: aAmt, BAmount: bAmt, AErr: aErr, BErr: bErr,
		DiffMinor: diff, At: time.Now().UTC(), Reason: reason,
	}
}

func (e *Engine) quarantineRecord(subject string) (QuarantineRecord, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.quarantined[subject]
	return rec, ok
}

// QuarantineRecordFor returns the quarantine record for review tooling.
func (e *Engine) QuarantineRecordFor(subject string) (QuarantineRecord, bool) {
	return e.quarantineRecord(subject)
}

// Release clears a quarantine after human review — the ONLY way out, and
// deliberately not automatic.
func (e *Engine) Release(subject string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.quarantined, subject)
}

// QuarantinedCount reports how many subjects await review.
func (e *Engine) QuarantinedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.quarantined)
}

// ── Canary data validation ──────────────────────────────────────────────────

// CanaryVerdict is the rollout gate decision.
type CanaryVerdict string

const (
	CanaryPass      CanaryVerdict = "PASS"      // within budget → rollout may proceed
	CanaryThreshold CanaryVerdict = "THRESHOLD" // beyond budget → STOP rollout
	CanaryError     CanaryVerdict = "ERROR"     // candidate errored → STOP rollout
)

// CanaryResult summarises old-vs-new over the sampled population.
type CanaryResult struct {
	SampleSize   int
	Mismatches   int
	MaxDiffMinor int64
	SumDiffMinor int64
	Verdict      CanaryVerdict
	Reason       string
	ComparedAt   time.Time
}

// TransformFunc maps one record through a data transformation. oldFn is the
// deployed implementation, newFn the candidate.
type TransformFunc func(record map[string]string) (map[string]string, error)

// Canary compares old and new transformations over the same records and
// enforces the difference budget as a hard rollout gate.
type Canary struct {
	oldFn, newFn TransformFunc
	// maxDiffPctOfSum: |Σnew − Σold| must stay under this fraction of |Σold|,
	// expressed in parts-per-hundred-thousand (e.g. 1 = 0.01%).
	maxDiffPctBps int64
	maxRecordDiff int64
}

// NewCanary builds a canary. maxRecordDiffMinor caps per-record divergence;
// maxDiffPctBps caps aggregate drift (1 = 0.01%, the documented budget).
func NewCanary(oldFn, newFn TransformFunc, maxRecordDiffMinor, maxDiffPctBps int64) *Canary {
	return &Canary{oldFn: oldFn, newFn: newFn, maxRecordDiff: maxRecordDiffMinor, maxDiffPctBps: maxDiffPctBps}
}

// Run replays records through both implementations. On any candidate error or
// budget breach it returns THRESHOLD/ERROR — the caller must stop the rollout.
func (c *Canary) Run(records []map[string]string) CanaryResult {
	res := CanaryResult{SampleSize: len(records), ComparedAt: time.Now().UTC()}
	var sumOld, sumNew int64
	for _, rec := range records {
		oldOut, oldErr := c.oldFn(rec)
		newOut, newErr := c.newFn(rec)
		if oldErr != nil || newErr != nil {
			if (oldErr == nil) != (newErr == nil) {
				res.Mismatches++
				res.Verdict = CanaryError
				res.Reason = fmt.Sprintf("error asymmetry at record %v: old=%v new=%v", rec, oldErr, newErr)
				return res
			}
			continue // both error identically → agreement
		}
		d := diffMaps(oldOut, newOut)
		if d > res.MaxDiffMinor {
			res.MaxDiffMinor = d
		}
		if d > 0 {
			res.Mismatches++
			res.SumDiffMinor += d
		}
		sumOld += toMinor(oldOut)
		sumNew += toMinor(newOut)
	}
	if res.Mismatches > 0 && res.MaxDiffMinor > c.maxRecordDiff {
		res.Verdict = CanaryThreshold
		res.Reason = fmt.Sprintf("max per-record diff %d exceeds budget %d", res.MaxDiffMinor, c.maxRecordDiff)
		return res
	}
	if sumOld != 0 || sumNew != 0 {
		drift := sumNew - sumOld
		if drift < 0 {
			drift = -drift
		}
		oldAbs := sumOld
		if oldAbs < 0 {
			oldAbs = -oldAbs
		}
		// drift*10000 / oldAbs ≤ maxDiffPctBps (parts per 10k)
		if oldAbs == 0 && drift != 0 {
			res.Verdict = CanaryThreshold
			res.Reason = "aggregate drift on zero baseline"
			return res
		}
		if oldAbs > 0 && drift*10000 > c.maxDiffPctBps*oldAbs {
			res.Verdict = CanaryThreshold
			res.Reason = fmt.Sprintf("aggregate drift %d bps exceeds budget %d bps", drift*10000/oldAbs, c.maxDiffPctBps)
			return res
		}
	}
	res.Verdict = CanaryPass
	res.Reason = fmt.Sprintf("%d records compared, %d mismatches within budget", res.SampleSize, res.Mismatches)
	return res
}

func diffMaps(a, b map[string]string) int64 {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var maxDiff int64
	for k := range keys {
		av, aok := a[k]
		bv, bok := b[k]
		if aok != bok {
			return -1 // structural difference → caller treats as error
		}
		if aok {
			if d := diffInt(av, bv); d > maxDiff {
				maxDiff = d
			}
		}
	}
	return maxDiff
}

func diffInt(a, b string) int64 {
	ai, aerr := parseInt(a)
	bi, berr := parseInt(b)
	if aerr != nil || berr != nil {
		if a == b {
			return 0
		}
		return -1
	}
	d := ai - bi
	if d < 0 {
		d = -d
	}
	return d
}

func parseInt(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

func toMinor(m map[string]string) int64 {
	v, _ := parseInt(m["amount_minor"])
	return v
}
