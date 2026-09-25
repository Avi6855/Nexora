package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/card-service/internal/domain"
)

// capturedPresentment authorizes and captures one presentment, which is the
// only state a refund is valid from.
func capturedPresentment(t *testing.T, h *harness, card *domain.Card, amount int64) *domain.CardAuthorization {
	t.Helper()
	req := presentment()
	req.Amount = amount

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, req)
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}
	captured, err := h.svc.CaptureAuthorization(context.Background(), card.CardID, auth.AuthorizationID)
	if err != nil {
		t.Fatalf("CaptureAuthorization: %v", err)
	}
	return captured
}

func TestPartialThenFullRefundCreditsExactlyTheCapturedAmount(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	captured := capturedPresentment(t, h, card, 50_00)

	partial, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 20_00, Reason: "damaged item"})
	if err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if partial.Status != domain.AuthStatusPartiallyRefunded {
		t.Fatalf("status = %s, want PARTIALLY_REFUNDED", partial.Status)
	}
	if partial.RefundedAmount != 20_00 || partial.RefundedAt == nil {
		t.Fatalf("refunded = %d (at %v), want 2000 with a timestamp", partial.RefundedAmount, partial.RefundedAt)
	}
	if h.ledger.refundCount() != 1 {
		t.Fatalf("ledger refunds = %d, want 1", h.ledger.refundCount())
	}

	rest, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 30_00})
	if err != nil {
		t.Fatalf("remaining refund: %v", err)
	}
	if rest.Status != domain.AuthStatusRefunded || rest.RefundedAmount != 50_00 {
		t.Fatalf("status/refunded = %s/%d, want REFUNDED/5000", rest.Status, rest.RefundedAmount)
	}
	if h.ledger.refundCount() != 2 {
		t.Fatalf("ledger refunds = %d, want 2", h.ledger.refundCount())
	}

	// Nothing is left to refund, and the authorization says so.
	if _, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 1}); !errors.Is(err, domain.ErrRefundOverCaptured) {
		t.Fatalf("refund of a fully refunded presentment error = %v, want ErrRefundOverCaptured", err)
	}
	if h.ledger.refundCount() != 2 {
		t.Fatalf("ledger refunds = %d after a refused refund, want 2", h.ledger.refundCount())
	}
}

func TestOverRefundIsRefusedBeforeAnyMoneyMoves(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	captured := capturedPresentment(t, h, card, 50_00)

	if _, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 60_00}); !errors.Is(err, domain.ErrRefundOverCaptured) {
		t.Fatalf("error = %v, want ErrRefundOverCaptured", err)
	}
	if h.ledger.refundCount() != 0 {
		t.Fatal("an over-refund reached the ledger")
	}
	stored, _ := h.auths.GetByID(context.Background(), captured.AuthorizationID)
	if stored.RefundedAmount != 0 || stored.Status != domain.AuthStatusCaptured {
		t.Fatalf("a refused refund changed the row: refunded=%d status=%s", stored.RefundedAmount, stored.Status)
	}
}

func TestOnlyCapturedPresentmentsCanBeRefunded(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	// Approved but not yet captured: the merchant has not settled, so there is
	// nothing to give back.
	approved, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}
	if _, err := h.svc.RefundAuthorization(context.Background(), card.CardID, approved.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 5_00}); !errors.Is(err, domain.ErrAuthNotRefundable) {
		t.Fatalf("refund of an uncaptured presentment error = %v, want ErrAuthNotRefundable", err)
	}

	// Voided: the hold was released, so no money ever left the customer.
	if _, err := h.svc.VoidAuthorization(context.Background(), card.CardID, approved.AuthorizationID); err != nil {
		t.Fatalf("VoidAuthorization: %v", err)
	}
	if _, err := h.svc.RefundAuthorization(context.Background(), card.CardID, approved.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 5_00}); !errors.Is(err, domain.ErrAuthNotRefundable) {
		t.Fatalf("refund of a voided presentment error = %v, want ErrAuthNotRefundable", err)
	}
	if h.ledger.refundCount() != 0 {
		t.Fatal("a non-refundable presentment reached the ledger")
	}

	// A zero or negative refund is a bad request, not a no-op.
	captured := capturedPresentment(t, h, card, 10_00)
	if _, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 0}); !errors.Is(err, domain.ErrRefundAmount) {
		t.Fatalf("zero refund error = %v, want ErrRefundAmount", err)
	}
}

func TestRefundAcrossCardsIsRefused(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	captured := capturedPresentment(t, h, card, 10_00)

	if _, err := h.svc.RefundAuthorization(context.Background(), uuid.New(), captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 5_00}); !errors.Is(err, domain.ErrAuthCardMismatch) {
		t.Fatalf("error = %v, want ErrAuthCardMismatch", err)
	}
	if h.ledger.refundCount() != 0 {
		t.Fatal("a cross-card refund reached the ledger")
	}
}

func TestFailedLedgerCreditIsRolledBackSoTheRefundCanBeRetried(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	captured := capturedPresentment(t, h, card, 50_00)
	h.ledger.setRefundStatus(http.StatusInternalServerError)

	if _, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 20_00}); err == nil {
		t.Fatal("a refund whose credit never landed was reported as successful")
	}

	// The claim must be rolled back: a row that says the customer was paid when
	// the ledger has no such entry would hide the missing money.
	stored, _ := h.auths.GetByID(context.Background(), captured.AuthorizationID)
	if stored.RefundedAmount != 0 || stored.RefundedAt != nil {
		t.Fatalf("failed refund left refunded=%d (at %v) on the row", stored.RefundedAmount, stored.RefundedAt)
	}
	if stored.Status != domain.AuthStatusCaptured {
		t.Fatalf("status = %s, want CAPTURED after a rolled-back refund", stored.Status)
	}

	// And the refund is retryable.
	h.ledger.setRefundStatus(http.StatusCreated)
	retried, err := h.svc.RefundAuthorization(context.Background(), card.CardID, captured.AuthorizationID, &domain.RefundAuthorizationRequest{Amount: 20_00})
	if err != nil {
		t.Fatalf("retried refund: %v", err)
	}
	if retried.RefundedAmount != 20_00 || retried.Status != domain.AuthStatusPartiallyRefunded {
		t.Fatalf("retry left refunded=%d status=%s, want 2000/PARTIALLY_REFUNDED", retried.RefundedAmount, retried.Status)
	}
}

func TestRefundClaimFromAStaleReadIsRejected(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	captured := capturedPresentment(t, h, card, 50_00)

	// Two concurrent callers read the same version of the row.
	first, err := h.auths.GetByID(context.Background(), captured.AuthorizationID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	stale := *first

	// The first claim wins.
	now := time.Now().UTC().Truncate(time.Millisecond)
	at := now
	first.RefundedAmount = 10_00
	first.RefundedAt = &at
	first.Status = domain.AuthStatusPartiallyRefunded
	first.UpdatedAt = now
	applied, err := h.auths.SaveRefund(context.Background(), first, domain.AuthStatusCaptured, captured.UpdatedAt)
	if err != nil || !applied {
		t.Fatalf("first claim applied=%v err=%v, want true", applied, err)
	}

	// The second claim is built from the version that is now stale, so it must
	// be refused rather than overwrite the first refund.
	stale.RefundedAmount = 20_00
	stale.RefundedAt = &at
	stale.Status = domain.AuthStatusPartiallyRefunded
	stale.UpdatedAt = now
	applied, err = h.auths.SaveRefund(context.Background(), &stale, domain.AuthStatusCaptured, captured.UpdatedAt)
	if err != nil {
		t.Fatalf("SaveRefund: %v", err)
	}
	if applied {
		t.Fatal("a refund claim from a stale read overwrote a newer refund")
	}

	stored, _ := h.auths.GetByID(context.Background(), captured.AuthorizationID)
	if stored.RefundedAmount != 10_00 {
		t.Fatalf("refunded = %d, want the first claim's 1000", stored.RefundedAmount)
	}
}
