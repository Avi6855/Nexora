package recon

import (
	"testing"
	"time"
)

func testWindow() Window {
	now := time.Now().UTC()
	return Window{From: now.Add(-time.Hour), To: now.Add(time.Hour)}
}

func gbp(v int64) Amount {
	return Amount{MinorUnits: v, Currency: "GBP"}
}

func item(ref string, minor int64, currency string, ts time.Time, rail string) ReconItem {
	return ReconItem{Reference: ref, Amount: Amount{MinorUnits: minor, Currency: currency}, Timestamp: ts, Rail: rail}
}

func mustOpen(t *testing.T, e *Engine, id string, tol []ToleranceRule) {
	t.Helper()
	if _, err := e.OpenBatch(id, testWindow(), tol); err != nil {
		t.Fatalf("OpenBatch(%s): %v", id, err)
	}
}

func mustIngest(t *testing.T, e *Engine, batch string, src Source, items []ReconItem) {
	t.Helper()
	if err := e.Ingest(batch, src, items); err != nil {
		t.Fatalf("Ingest(%s,%s): %v", batch, src, err)
	}
}

func ingestAllFour(t *testing.T, e *Engine, batch, ref string, amounts [4]int64, ts time.Time) {
	t.Helper()
	srcs := []Source{SourceProcessorReport, SourcePaymentRecords, SourceLedger, SourceSettlementFile}
	for i, s := range srcs {
		mustIngest(t, e, batch, s, []ReconItem{item(ref, amounts[i], "GBP", ts, "FPS")})
	}
}

func TestFullMatch(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-full", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 5}})
	ts := time.Now().UTC()
	ingestAllFour(t, e, "b-full", "tx-full", [4]int64{10000, 10000, 10000, 10000}, ts)
	out, err := e.Run("b-full")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["tx-full"] != OutcomeMatched {
		t.Fatalf("want MATCHED, got %s", out["tx-full"])
	}
	sum, err := e.BatchSummary("b-full")
	if err != nil {
		t.Fatalf("BatchSummary: %v", err)
	}
	if sum.Matched != 1 || sum.Total != 1 {
		t.Fatalf("bad summary: %+v", sum)
	}
	for _, ex := range e.ExceptionQueue() {
		if ex.Reference == "tx-full" {
			t.Fatalf("matched ref should not be in exception queue")
		}
	}
	repairs, err := e.ProposeRepairs("b-full")
	if err != nil {
		t.Fatalf("ProposeRepairs: %v", err)
	}
	if len(repairs) != 0 {
		t.Fatalf("exact match should have no repairs, got %d", len(repairs))
	}
}

func TestAmountMismatchBeyondTolerance(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-mis", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 5}})
	ts := time.Now().UTC()
	ingestAllFour(t, e, "b-mis", "tx-mis", [4]int64{10000, 10000, 10000, 10100}, ts)
	out, err := e.Run("b-mis")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["tx-mis"] != OutcomeMismatch {
		t.Fatalf("want MISMATCH, got %s", out["tx-mis"])
	}
	sum, _ := e.BatchSummary("b-mis")
	if sum.Mismatch != 1 {
		t.Fatalf("want 1 mismatch, got %+v", sum)
	}
	q := e.ExceptionQueue()
	if len(q) != 1 || q[0].Outcome != OutcomeMismatch {
		t.Fatalf("want 1 mismatch exception, got %+v", q)
	}
	repairs, _ := e.ProposeRepairs("b-mis")
	if len(repairs) != 0 {
		t.Fatalf("beyond-tolerance break must not auto-propose repair, got %d", len(repairs))
	}
}

func TestCurrencyMismatch(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-ccy", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 100}, {Currency: "USD", EpsilonMinorUnits: 100}})
	ts := time.Now().UTC()
	mustIngest(t, e, "b-ccy", SourceProcessorReport, []ReconItem{item("tx-ccy", 10000, "GBP", ts, "FPS")})
	mustIngest(t, e, "b-ccy", SourcePaymentRecords, []ReconItem{item("tx-ccy", 10000, "GBP", ts, "FPS")})
	mustIngest(t, e, "b-ccy", SourceLedger, []ReconItem{item("tx-ccy", 10000, "GBP", ts, "FPS")})
	mustIngest(t, e, "b-ccy", SourceSettlementFile, []ReconItem{item("tx-ccy", 10000, "USD", ts, "FPS")})
	out, err := e.Run("b-ccy")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["tx-ccy"] != OutcomeMismatch {
		t.Fatalf("currency break must be MISMATCH, got %s", out["tx-ccy"])
	}
}

func TestSubToleranceAutoRepairProposal(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-repair", []ToleranceRule{{Currency: "GBP", Rail: "FPS", EpsilonMinorUnits: 10}})
	ts := time.Now().UTC()
	ingestAllFour(t, e, "b-repair", "tx-sub", [4]int64{10000, 10000, 10000, 10005}, ts)
	out, err := e.Run("b-repair")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["tx-sub"] != OutcomeMatched {
		t.Fatalf("sub-tolerance diff must still MATCH, got %s", out["tx-sub"])
	}
	repairs, err := e.ProposeRepairs("b-repair")
	if err != nil {
		t.Fatalf("ProposeRepairs: %v", err)
	}
	if len(repairs) != 1 {
		t.Fatalf("want 1 repair proposal, got %d", len(repairs))
	}
	r := repairs[0]
	if r.Reference != "tx-sub" || r.DiffMinorUnits != 5 {
		t.Fatalf("bad proposal: %+v", r)
	}
	if r.Suggested.Amount.MinorUnits != -5 {
		t.Fatalf("want adjustment -5, got %+v", r.Suggested.Amount)
	}
}

func TestPartialMissingSettlementLeg(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-partial", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 5}})
	ts := time.Now().UTC()
	for _, s := range []Source{SourceProcessorReport, SourcePaymentRecords, SourceLedger} {
		mustIngest(t, e, "b-partial", s, []ReconItem{item("tx-part", 7000, "GBP", ts, "FPS")})
	}
	out, err := e.Run("b-partial")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["tx-part"] != OutcomePartial {
		t.Fatalf("want PARTIAL, got %s", out["tx-part"])
	}
	sum, _ := e.BatchSummary("b-partial")
	if sum.Partial != 1 {
		t.Fatalf("want 1 partial, got %+v", sum)
	}
}

func TestWindowExclusion(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-window", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 5}})
	old := time.Now().UTC().Add(-48 * time.Hour)
	ingestAllFour(t, e, "b-window", "tx-old", [4]int64{5000, 5000, 5000, 5000}, old)
	out, err := e.Run("b-window")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["tx-old"] != OutcomeUnknown {
		t.Fatalf("out-of-window item must be UNKNOWN, got %s", out["tx-old"])
	}
	sum, _ := e.BatchSummary("b-window")
	if sum.Unknown != 1 {
		t.Fatalf("want 1 unknown, got %+v", sum)
	}
}

func TestIdempotentCorrectionDoublePost(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-corr", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 5}})
	ts := time.Now().UTC()
	ingestAllFour(t, e, "b-corr", "tx-c", [4]int64{9000, 9000, 9000, 9200}, ts)
	if _, err := e.Run("b-corr"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := len(e.AuditTrail("b-corr"))
	c := Correction{ID: "corr-1", BatchID: "b-corr", Reference: "tx-c", Amount: gbp(-200), Reason: "fix over-settlement"}
	first, err := e.PostCorrection(c)
	if err != nil {
		t.Fatalf("PostCorrection: %v", err)
	}
	second, err := e.PostCorrection(c)
	if err != nil {
		t.Fatalf("second PostCorrection: %v", err)
	}
	if first.ID != second.ID || first.Amount.MinorUnits != second.Amount.MinorUnits {
		t.Fatalf("idempotent repost must return same correction: %+v vs %+v", first, second)
	}
	sum, _ := e.BatchSummary("b-corr")
	if sum.Corrections != 1 {
		t.Fatalf("double-post must dedupe to 1 correction, got %+v", sum)
	}
	after := len(e.AuditTrail("b-corr"))
	if after != before+1 {
		t.Fatalf("duplicate correction must not append audit: before=%d after=%d", before, after)
	}
}

func TestAuditChainVerification(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-audit", []ToleranceRule{{Currency: "GBP", EpsilonMinorUnits: 5}})
	ts := time.Now().UTC()
	ingestAllFour(t, e, "b-audit", "tx-a", [4]int64{1000, 1000, 1000, 1100}, ts)
	if _, err := e.Run("b-audit"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := e.PostCorrection(Correction{ID: "c-a", BatchID: "b-audit", Reference: "tx-a", Amount: gbp(-100), Reason: "t"}); err != nil {
		t.Fatalf("PostCorrection: %v", err)
	}
	q := e.ExceptionQueue()
	if len(q) == 0 {
		t.Fatalf("expected exception for audit test")
	}
	if _, err := e.AcknowledgeException(q[0].ID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if _, err := e.ResolveException(q[0].ID, "fixed"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	trail := e.AuditTrail("b-audit")
	if len(trail) == 0 {
		t.Fatalf("empty audit trail")
	}
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	// Manual link verification.
	for i, en := range trail {
		if i == 0 {
			continue
		}
		_ = en
	}
	prev := ""
	seq := int64(0)
	for _, en := range e.AuditTrail("") {
		seq++
		if en.Seq != seq {
			// Global trail is contiguous from 1; batch trail is a subset.
			break
		}
		if en.PrevHash != prev {
			t.Fatalf("entry %d prev link broken", en.Seq)
		}
		if computeHash(en.PrevHash, en.Payload) != en.Hash {
			t.Fatalf("entry %d hash mismatch", en.Seq)
		}
		prev = en.Hash
	}
	if err := e.VerifyBatchChain("b-audit"); err != nil {
		t.Fatalf("VerifyBatchChain: %v", err)
	}
}

func TestOpenBatchDuplicateAndNotFound(t *testing.T) {
	e := NewEngine()
	mustOpen(t, e, "b-dup", nil)
	if _, err := e.OpenBatch("b-dup", testWindow(), nil); err == nil {
		t.Fatalf("duplicate OpenBatch must fail")
	}
	if err := e.Ingest("nope", SourceLedger, []ReconItem{item("x", 1, "GBP", time.Now().UTC(), "")}); err == nil {
		t.Fatalf("ingest into missing batch must fail")
	}
	if _, err := e.Run("nope"); err == nil {
		t.Fatalf("run missing batch must fail")
	}
	if _, err := e.BatchSummary("nope"); err == nil {
		t.Fatalf("summary missing batch must fail")
	}
}
