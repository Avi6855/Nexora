package regreport

import (
	"errors"
	"testing"
)

func sampleProv(key string, value int64) Provenance {
	return Provenance{
		FigureKey: key, FigureValue: value,
		Sources:   []string{"ledger.entries@2026-08", "accounts.accounts@2026-08"},
		Query:     "SELECT sum(amount) FROM ledger_entries WHERE period='2026-08' AND type='CREDIT'",
		Transform: "regulatory.credit_total", TransformVer: "v3",
		RowsIn: 1000, RowsOut: 1000,
	}
}

func TestAddFigureRequiresProvenance(t *testing.T) {
	b := NewBuilder()
	if _, err := b.CreateReport("rep-1", "FCA_COR007", "2026-08"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Missing query → rejected.
	bad := sampleProv("total_credits", 100)
	bad.Query = ""
	if err := b.AddFigure("rep-1", bad); err == nil {
		t.Fatal("figure without query must be rejected")
	}
	// Missing sources → rejected.
	bad2 := sampleProv("total_credits", 100)
	bad2.Sources = nil
	if err := b.AddFigure("rep-1", bad2); err == nil {
		t.Fatal("figure without sources must be rejected")
	}
	if err := b.AddFigure("rep-1", sampleProv("total_credits", 2_481_920)); err != nil {
		t.Fatalf("complete provenance must be accepted: %v", err)
	}
}

func TestValidationGates(t *testing.T) {
	b := NewBuilder()
	b.CreateReport("rep-2", "FCA_COR007", "2026-08")
	b.AddFigure("rep-2", sampleProv("total_credits", 100))
	b.AddFigure("rep-2", sampleProv("total_debits", 98))

	// Completeness failure.
	err := b.Validate("rep-2", []ValidationGate{
		CompletenessGate{Required: []string{"total_credits", "total_debits", "customer_count"}},
	})
	if err == nil || err.Error() != ErrValidationFailed.Error()+": gate \"completeness\": mandatory figure \"customer_count\" missing" {
		t.Fatalf("completeness gate must fail on missing figure, got %v", err)
	}

	// All gates pass → VALIDATED.
	err = b.Validate("rep-2", []ValidationGate{
		CompletenessGate{Required: []string{"total_credits", "total_debits"}},
		ReconciliationGate{MaxLossRatio: 0.01},
	})
	if err != nil {
		t.Fatalf("validation must pass: %v", err)
	}
	r, _ := b.Get("rep-2")
	if r.Status != "VALIDATED" {
		t.Fatalf("status = %s, want VALIDATED", r.Status)
	}
}

func TestReconciliationGateCatchesRowLoss(t *testing.T) {
	b := NewBuilder()
	b.CreateReport("rep-3", "R", "P")
	p := sampleProv("k", 1)
	p.RowsIn, p.RowsOut = 1000, 800 // 20% loss ≫ 1% budget
	b.AddFigure("rep-3", p)
	err := b.Validate("rep-3", []ValidationGate{ReconciliationGate{MaxLossRatio: 0.01}})
	if err == nil {
		t.Fatal("20% row loss must fail reconciliation")
	}
}

func TestVarianceGateCatchesUnexplainedSwing(t *testing.T) {
	b := NewBuilder()
	b.CreateReport("rep-4", "R", "2026-09")
	b.AddFigure("rep-4", sampleProv("total_credits", 3_100_000)) // prior 2.48M → +25%
	err := b.Validate("rep-4", []ValidationGate{
		VarianceGate{Prior: map[string]int64{"total_credits": 2_481_920}, MaxPctBps: 1000}, // 10%
	})
	if err == nil {
		t.Fatal("25% swing must fail the 10% variance gate")
	}
}

func TestSubmitRequiresValidationAndIsImmutable(t *testing.T) {
	b := NewBuilder()
	b.CreateReport("rep-5", "R", "P")
	b.AddFigure("rep-5", sampleProv("k", 1))

	// Submit before validation → refused.
	if err := b.Submit("rep-5", "SUB-1"); !errors.Is(err, ErrNotValidated) {
		t.Fatalf("want ErrNotValidated, got %v", err)
	}
	if err := b.Validate("rep-5", []ValidationGate{CompletenessGate{Required: []string{"k"}}}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := b.Submit("rep-5", "SUB-1"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Double submission → refused.
	if err := b.Submit("rep-5", "SUB-2"); !errors.Is(err, ErrAlreadySubmitted) {
		t.Fatalf("want ErrAlreadySubmitted, got %v", err)
	}
	// No figure edits after submission.
	if err := b.AddFigure("rep-5", sampleProv("k2", 2)); err == nil {
		t.Fatal("submitted reports are immutable")
	}
}

func TestEvidenceChainHashVerification(t *testing.T) {
	b := NewBuilder()
	b.CreateReport("rep-6", "R", "P")
	p := sampleProv("mystery_number", 2_481_920)
	b.AddFigure("rep-6", p)

	got, err := b.EvidenceFor("rep-6", "mystery_number")
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if got.ChainHash == "" || len(got.Sources) != 2 {
		t.Fatal("evidence must carry the full chain")
	}

	// Tamper with the stored chain → hash check must catch it.
	b.reports["rep-6"].Figures[0].FigureValue = 1
	if _, err := b.EvidenceFor("rep-6", "mystery_number"); err == nil {
		t.Fatal("tampered evidence chain must fail hash verification")
	}
}

func TestEvidenceForUnknownFigure(t *testing.T) {
	b := NewBuilder()
	b.CreateReport("rep-7", "R", "P")
	b.AddFigure("rep-7", sampleProv("k", 1))
	if _, err := b.EvidenceFor("rep-7", "nope"); !errors.Is(err, ErrUnknownFigure) {
		t.Fatalf("want ErrUnknownFigure, got %v", err)
	}
}
