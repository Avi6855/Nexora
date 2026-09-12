package reconnet

import (
	"testing"
	"time"

	"github.com/nexora/nexora/shared/recon"
)

func openTestBatch(t *testing.T, svc *Service, id string) {
	t.Helper()
	now := time.Now().UTC()
	w := recon.Window{From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	tols := []recon.ToleranceRule{{Currency: "GBP", Rail: "FPS", EpsilonMinorUnits: 5}}
	if _, err := svc.OpenBatch(id, w, tols); err != nil {
		t.Fatalf("OpenBatch: %v", err)
	}
}

func ingestLeg(t *testing.T, svc *Service, batch string, src recon.Source, ref string, minor int64, ts time.Time) {
	t.Helper()
	err := svc.Ingest(batch, src, []recon.ReconItem{
		{Reference: ref, Amount: recon.Amount{MinorUnits: minor, Currency: "GBP"}, Timestamp: ts, Rail: "FPS"},
	})
	if err != nil {
		t.Fatalf("Ingest(%s,%s): %v", batch, src, err)
	}
}

// Mismatch batch run + repair + idempotent correction + exception ack/resolve.
func TestMismatchRunRepairCorrectionExceptions(t *testing.T) {
	svc := NewService()
	openTestBatch(t, svc, "svc-batch-1")
	ts := time.Now().UTC()

	// tx-mismatch: beyond tolerance (diff 200 > eps 5).
	for _, src := range []recon.Source{recon.SourceProcessorReport, recon.SourcePaymentRecords, recon.SourceLedger} {
		ingestLeg(t, svc, "svc-batch-1", src, "tx-mismatch", 10000, ts)
	}
	ingestLeg(t, svc, "svc-batch-1", recon.SourceSettlementFile, "tx-mismatch", 10200, ts)

	// tx-repair: sub-tolerance (diff 3 <= eps 5) -> MATCHED + repair proposal.
	for _, src := range []recon.Source{recon.SourceProcessorReport, recon.SourcePaymentRecords, recon.SourceLedger} {
		ingestLeg(t, svc, "svc-batch-1", src, "tx-repair", 10000, ts)
	}
	ingestLeg(t, svc, "svc-batch-1", recon.SourceSettlementFile, "tx-repair", 10003, ts)

	outcomes, err := svc.Run("svc-batch-1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcomes["tx-mismatch"] != recon.OutcomeMismatch {
		t.Fatalf("want MISMATCH for tx-mismatch, got %s", outcomes["tx-mismatch"])
	}
	if outcomes["tx-repair"] != recon.OutcomeMatched {
		t.Fatalf("want MATCHED for tx-repair, got %s", outcomes["tx-repair"])
	}

	repairs, err := svc.Repairs("svc-batch-1")
	if err != nil {
		t.Fatalf("Repairs: %v", err)
	}
	if len(repairs) != 1 || repairs[0].Reference != "tx-repair" {
		t.Fatalf("want 1 repair for tx-repair, got %+v", repairs)
	}

	// Idempotent correction double-post for the mismatch leg.
	corr := recon.Correction{
		ID:        "corr-svc-1",
		BatchID:   "svc-batch-1",
		Reference: "tx-mismatch",
		Amount:    recon.Amount{MinorUnits: -200, Currency: "GBP"},
		Reason:    "correct over-settlement",
	}
	first, err := svc.PostCorrection(corr)
	if err != nil {
		t.Fatalf("PostCorrection: %v", err)
	}
	second, err := svc.PostCorrection(corr)
	if err != nil {
		t.Fatalf("second PostCorrection: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent repost mismatch: %+v vs %+v", first, second)
	}
	sum, err := svc.Summary("svc-batch-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.Corrections != 1 {
		t.Fatalf("want 1 deduped correction, got %+v", sum)
	}
	if sum.Mismatch != 1 || sum.Matched != 1 {
		t.Fatalf("bad summary counts: %+v", sum)
	}

	// Exception ack/resolve for the mismatch.
	var mismatchID string
	for _, ex := range svc.ExceptionQueue() {
		if ex.Reference == "tx-mismatch" {
			mismatchID = ex.ID
		}
	}
	if mismatchID == "" {
		t.Fatalf("mismatch exception missing in queue")
	}
	acked, err := svc.AcknowledgeException(mismatchID)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if acked.Status != recon.ExceptionAcked {
		t.Fatalf("want ACKNOWLEDGED, got %s", acked.Status)
	}
	resolved, err := svc.ResolveException(mismatchID, "adjusted via corr-svc-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Status != recon.ExceptionResolved {
		t.Fatalf("want RESOLVED, got %s", resolved.Status)
	}
	for _, ex := range svc.ExceptionQueue() {
		if ex.ID == mismatchID {
			t.Fatalf("resolved exception must leave the queue")
		}
	}
	trail := svc.Audit("svc-batch-1")
	if len(trail) == 0 {
		t.Fatalf("empty audit trail")
	}
	if err := svc.Engine().VerifyChain(); err != nil {
		t.Fatalf("audit chain: %v", err)
	}
}
