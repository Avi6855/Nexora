package service

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/dispute-service/internal/domain"
)

func newOrchSvc() *OrchestrationService {
	svc := NewOrchestrationService(zerolog.Nop())
	fixed := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc.nowFunc = func() time.Time { return fixed }
	return svc
}

func TestOrchestratedCardLifecycle(t *testing.T) {
	svc := newOrchSvc()
	now := svc.nowFunc()
	d, err := svc.Create(context.Background(), domain.OrchestratedKindCard, "u-1", "a-1", 2500, "GBP", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("create card failed: %v", err)
	}
	if d.Status != domain.OrchestratedStatusAwaitingEvidence {
		t.Fatalf("card status = %s, want AWAITING_EVIDENCE", d.Status)
	}
	if _, err := svc.AddEvidence(context.Background(), d.DisputeID, "RECEIPT", "receipt.png"); err != nil {
		t.Fatalf("add evidence failed: %v", err)
	}
	adv, err := svc.Advance(context.Background(), d.DisputeID, "submit")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if adv.Status != domain.OrchestratedStatusUnderReview {
		t.Fatalf("status = %s, want UNDER_REVIEW", adv.Status)
	}
	won, err := svc.Advance(context.Background(), d.DisputeID, "resolve-won")
	if err != nil {
		t.Fatalf("resolve-won failed: %v", err)
	}
	if won.Status != domain.OrchestratedStatusResolved || won.Adjustment == nil {
		t.Fatalf("expected RESOLVED with adjustment, got %+v", won)
	}
	if !won.Adjustment.Balanced() {
		t.Fatalf("adjustment legs must balance: %+v", won.Adjustment.Legs)
	}
}

func TestOrchestratedTransferRecallWindows(t *testing.T) {
	svc := newOrchSvc()
	now := svc.nowFunc()
	// Fast recall inside 10 days.
	fast, err := svc.Create(context.Background(), domain.OrchestratedKindTransfer, "u-1", "a-1", 10000, "GBP", now.Add(-5*24*time.Hour))
	if err != nil {
		t.Fatalf("fast recall create failed: %v", err)
	}
	if fast.Status != domain.OrchestratedStatusAwaitingEvidence {
		t.Fatalf("fast status = %s", fast.Status)
	}
	// Late recall up to 60 days still eligible.
	late, err := svc.Create(context.Background(), domain.OrchestratedKindTransfer, "u-1", "a-1", 10000, "GBP", now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("late recall create failed: %v", err)
	}
	if late.Status != domain.OrchestratedStatusAwaitingEvidence {
		t.Fatalf("late status = %s", late.Status)
	}
	// Beyond 60 days is ineligible.
	if _, err := svc.Create(context.Background(), domain.OrchestratedKindTransfer, "u-1", "a-1", 10000, "GBP", now.Add(-61*24*time.Hour)); err == nil {
		t.Fatalf("stale transfer must be ineligible")
	}
	// Transfer evidence allow-list.
	if _, err := svc.AddEvidence(context.Background(), fast.DisputeID, "TRANSFER_CONFIRMATION", "confirm.pdf"); err != nil {
		t.Fatalf("transfer evidence failed: %v", err)
	}
	if _, err := svc.AddEvidence(context.Background(), fast.DisputeID, "ATM_RECEIPT", "atm.png"); err == nil {
		t.Fatalf("ATM_RECEIPT must be rejected for TRANSFER")
	}
}

func TestOrchestratedDirectDebitImmediateReturn(t *testing.T) {
	svc := newOrchSvc()
	now := svc.nowFunc()
	d, err := svc.Create(context.Background(), domain.OrchestratedKindDirectDebit, "u-dd", "a-dd", 4999, "GBP", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("create DD failed: %v", err)
	}
	// Indemnity-style immediate return: no evidence or review needed.
	if d.Status != domain.OrchestratedStatusReturned {
		t.Fatalf("DD status = %s, want RETURNED", d.Status)
	}
	if d.Adjustment == nil {
		t.Fatalf("DD must carry an immediate adjustment proposal")
	}
	if !d.Adjustment.Balanced() {
		t.Fatalf("DD adjustment must balance: %+v", d.Adjustment.Legs)
	}
	var debits, credits int64
	for _, l := range d.Adjustment.Legs {
		if l.Direction == domain.LegDebit {
			debits += l.AmountMinor
		} else {
			credits += l.AmountMinor
		}
	}
	if debits != 4999 || credits != 4999 {
		t.Fatalf("legs must equal disputed amount, got debit=%d credit=%d", debits, credits)
	}
	// Adjustment endpoint serves the same proposal.
	adj, err := svc.Adjustment(context.Background(), d.DisputeID)
	if err != nil {
		t.Fatalf("adjustment fetch failed: %v", err)
	}
	if !adj.Balanced() {
		t.Fatalf("fetched adjustment must balance")
	}
}

func TestOrchestratedCashEvidenceRequirements(t *testing.T) {
	svc := newOrchSvc()
	now := svc.nowFunc()
	d, err := svc.Create(context.Background(), domain.OrchestratedKindCash, "u-c", "a-c", 2000, "GBP", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("create cash failed: %v", err)
	}
	// Strict ATM evidence: non-ATM types rejected.
	if _, err := svc.AddEvidence(context.Background(), d.DisputeID, "RECEIPT", "r.png"); err == nil {
		t.Fatalf("RECEIPT must be rejected for CASH")
	}
	// Submit without ATM_RECEIPT must fail.
	if _, err := svc.Advance(context.Background(), d.DisputeID, "submit"); err == nil {
		t.Fatalf("cash submit without ATM_RECEIPT must fail")
	}
	if _, err := svc.AddEvidence(context.Background(), d.DisputeID, "ATM_RECEIPT", "atm.png"); err != nil {
		t.Fatalf("ATM_RECEIPT failed: %v", err)
	}
	if _, err := svc.AddEvidence(context.Background(), d.DisputeID, "POLICE_REPORT", "report.pdf"); err != nil {
		t.Fatalf("POLICE_REPORT failed: %v", err)
	}
	adv, err := svc.Advance(context.Background(), d.DisputeID, "submit")
	if err != nil {
		t.Fatalf("cash submit with ATM evidence failed: %v", err)
	}
	if adv.Status != domain.OrchestratedStatusUnderReview {
		t.Fatalf("status = %s, want UNDER_REVIEW", adv.Status)
	}
}

func TestOrchestratedAdjustmentLegsBalance(t *testing.T) {
	svc := newOrchSvc()
	now := svc.nowFunc()
	d, err := svc.Create(context.Background(), domain.OrchestratedKindCard, "u-1", "a-9", 7777, "GBP", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := svc.AddEvidence(context.Background(), d.DisputeID, "PHOTO", "p.png"); err != nil {
		t.Fatalf("evidence failed: %v", err)
	}
	if _, err := svc.Advance(context.Background(), d.DisputeID, "submit"); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	won, err := svc.Advance(context.Background(), d.DisputeID, "resolve-won")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	adj := won.Adjustment
	if adj == nil || len(adj.Legs) != 2 {
		t.Fatalf("expected 2-leg proposal, got %+v", adj)
	}
	if !adj.Balanced() {
		t.Fatalf("legs must balance")
	}
	foundCredit := false
	for _, l := range adj.Legs {
		if l.Direction == domain.LegCredit && l.AccountID == "a-9" && l.AmountMinor == 7777 {
			foundCredit = true
		}
	}
	if !foundCredit {
		t.Fatalf("customer credit leg missing: %+v", adj.Legs)
	}
	// Lost disputes carry no proposal.
	d2, err := svc.Create(context.Background(), domain.OrchestratedKindCard, "u-1", "a-9", 100, "GBP", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("create 2 failed: %v", err)
	}
	if _, err := svc.Advance(context.Background(), d2.DisputeID, "resolve-lost"); err != nil {
		t.Fatalf("resolve-lost failed: %v", err)
	}
	if _, err := svc.Adjustment(context.Background(), d2.DisputeID); err == nil {
		t.Fatalf("lost dispute must have no adjustment proposal")
	}
}
