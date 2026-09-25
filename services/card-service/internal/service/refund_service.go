package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/events"
)

// ── Card refunds ────────────────────────────────────────────────────────────
//
// A captured card presentment can be refunded in full or in part. The shape of
// the operation is claim-then-credit:
//
//  1. Claim the refundable amount on the authorization row with an optimistic
//     concurrency check, so two concurrent partial refunds can never exceed the
//     captured total.
//  2. Credit the customer through the ledger of record, which writes the
//     balanced customer-credit/suspense-debit pair exactly once under the refund
//     id as its idempotency key.
//  3. If the credit never landed, write the claim back (compensation) so the
//     refund can be retried instead of leaving a row that says the customer was
//     paid when they were not.

// RefundAuthorization credits a captured presentment back to the customer.
func (s *AuthorizationService) RefundAuthorization(ctx context.Context, cardID uuid.UUID, authID uuid.UUID, req *domain.RefundAuthorizationRequest) (*domain.CardAuthorization, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	auth, err := s.authRepo.GetByID(ctx, authID)
	if err != nil {
		return nil, err
	}
	if auth.CardID != cardID {
		return nil, domain.ErrAuthCardMismatch
	}

	// Only money that actually left the customer can come back: a declined,
	// challenged or voided presentment never took a hold, so there is nothing
	// to refund. A partially or fully refunded presentment stays on this path so
	// the amount check below reports precisely how much (if anything) is left.
	switch auth.Status {
	case domain.AuthStatusCaptured, domain.AuthStatusPartiallyRefunded, domain.AuthStatusRefunded:
	default:
		return nil, domain.ErrAuthNotRefundable
	}

	remaining := auth.Amount - auth.RefundedAmount
	if req.Amount > remaining {
		return nil, fmt.Errorf("%w: %d requested, %d left of %d", domain.ErrRefundOverCaptured, req.Amount, remaining, auth.Amount)
	}

	prevStatus := auth.Status
	prevUpdatedAt := auth.UpdatedAt
	prevRefunded := auth.RefundedAmount
	prevRefundedAt := auth.RefundedAt
	refundID := uuid.New()

	// Timestamps are truncated to milliseconds because Cassandra stores
	// TIMESTAMP at millisecond precision and the claim is our concurrency
	// token: a nanosecond value would never compare equal after a round trip.
	now := time.Now().UTC().Truncate(time.Millisecond)

	refundedAt := now
	auth.RefundedAmount = prevRefunded + req.Amount
	auth.RefundedAt = &refundedAt
	auth.UpdatedAt = now
	if auth.RefundedAmount >= auth.Amount {
		auth.Status = domain.AuthStatusRefunded
	} else {
		auth.Status = domain.AuthStatusPartiallyRefunded
	}

	applied, err := s.authRepo.SaveRefund(ctx, auth, prevStatus, prevUpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("claiming refund: %w", err)
	}
	if !applied {
		// Another refund changed the row first: the caller retries against the
		// fresh refundable balance rather than over-refunding from a stale read.
		return nil, domain.ErrAuthNotRefundable
	}

	description := fmt.Sprintf("refund %s", auth.Merchant)
	entry, err := s.ledger.Refund(ctx, auth.AccountID, req.Amount, auth.Currency, refundID.String(), description)
	if err != nil {
		if relErr := s.releaseRefundClaim(ctx, auth, prevStatus, prevRefunded, prevRefundedAt); relErr != nil {
			s.logger.Error().Err(relErr).
				Str("authorization_id", authID.String()).
				Str("refund_id", refundID.String()).
				Msg("failed to roll back a refund claim whose credit never landed")
		}
		return nil, fmt.Errorf("crediting refund: %w", err)
	}

	var balanceAfter *int64
	if entry != nil {
		bal := entry.BalanceAfter
		balanceAfter = &bal
	}

	s.publishEvent(ctx, events.EventTypeAuthRefunded, &events.AuthorizationEvent{
		AuthorizationID:  auth.AuthorizationID.String(),
		CardID:           auth.CardID.String(),
		UserID:           auth.UserID.String(),
		AccountID:        auth.AccountID.String(),
		Amount:           auth.Amount,
		Currency:         auth.Currency,
		Merchant:         auth.Merchant,
		MerchantCategory: auth.MerchantCategory,
		MerchantCity:     auth.MerchantCity,
		MerchantCountry:  auth.MerchantCountry,
		TerminalID:       auth.TerminalID,
		Status:           string(auth.Status),
		Decision:         string(auth.Decision),
		ReservationID:    reservationIDOf(auth),
		RefundedAmount:   auth.RefundedAmount,
		RefundID:         refundID.String(),
		BalanceAfter:     balanceAfter,
		CreatedAt:        now,
	})

	s.logger.Info().
		Str("authorization_id", authID.String()).
		Str("card_id", cardID.String()).
		Str("refund_id", refundID.String()).
		Int64("amount", req.Amount).
		Int64("refunded_total", auth.RefundedAmount).
		Str("status", string(auth.Status)).
		Msg("card refund credited")

	return auth, nil
}

// releaseRefundClaim writes the pre-claim refund state back after a credit that
// never landed, guarded by the version the claim produced so it can only undo
// its own claim.
func (s *AuthorizationService) releaseRefundClaim(ctx context.Context, auth *domain.CardAuthorization, prevStatus domain.AuthorizationStatus, prevRefunded int64, prevRefundedAt *time.Time) error {
	claimedStatus := auth.Status
	claimedUpdatedAt := auth.UpdatedAt

	auth.Status = prevStatus
	auth.RefundedAmount = prevRefunded
	auth.RefundedAt = prevRefundedAt
	auth.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)

	applied, err := s.authRepo.SaveRefund(ctx, auth, claimedStatus, claimedUpdatedAt)
	if err != nil {
		return err
	}
	if !applied {
		return domain.ErrAuthNotRefundable
	}
	return nil
}
