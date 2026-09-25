package events

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
)

type PaymentEventProcessor struct {
	ledgerService LedgerServiceInterface
	gate          BookingGate
	logger        zerolog.Logger
}

type LedgerServiceInterface interface {
	CreateDoubleEntryTransaction(ctx context.Context, req *domain.CreateDoubleEntryRequest) (*domain.LedgerTransaction, []*domain.LedgerEntry, error)

	// GetAvailableBalance reports cleared funds minus active holds, the same
	// measure the transfer path uses to refuse an unaffordable booking.
	GetAvailableBalance(ctx context.Context, accountID uuid.UUID) (int64, error)

	// RecordPaymentBookingFailure files a refused or unbookable payment so it is
	// visible to an operator instead of only appearing in a log line.
	RecordPaymentBookingFailure(ctx context.Context, paymentID, accountID uuid.UUID, reason string) error
}

// BookingGate claims a payment exactly once (LWT) so duplicate lifecycle
// events (confirmed + settled for the same payment) never double-book.
type BookingGate interface {
	MarkPaymentBooked(ctx context.Context, paymentID uuid.UUID, idempotencyKey, eventType string) (bool, error)

	// ReleasePaymentClaim undoes a claim whose booking failed, so the next
	// delivery of the event can try again instead of being skipped as a
	// duplicate for the rest of the payment's life.
	ReleasePaymentClaim(ctx context.Context, paymentID uuid.UUID) error
}

func NewPaymentEventProcessor(ledgerService LedgerServiceInterface, gate BookingGate, logger zerolog.Logger) *PaymentEventProcessor {
	return &PaymentEventProcessor{
		ledgerService: ledgerService,
		gate:          gate,
		logger:        logger,
	}
}

func (p *PaymentEventProcessor) HandlePaymentEvent(ctx context.Context, payload *PaymentEventPayload, eventType string) error {
	switch eventType {
	case "payment.confirmed", "payment.settled":
		return p.bookOnce(ctx, payload, eventType)
	default:
		p.logger.Debug().Str("event_type", eventType).Msg("ignoring payment event")
		return nil
	}
}

func (p *PaymentEventProcessor) bookOnce(ctx context.Context, payload *PaymentEventPayload, eventType string) error {
	paymentID, err := uuid.Parse(payload.PaymentID)
	if err != nil {
		return err
	}

	claimed, err := p.gate.MarkPaymentBooked(ctx, paymentID, payload.IdempotencyKey, eventType)
	if err != nil {
		return err
	}
	if !claimed {
		p.logger.Info().Str("payment_id", payload.PaymentID).Str("event_type", eventType).Msg("payment already booked; skipping duplicate event")
		return nil
	}

	p.logger.Info().Str("payment_id", payload.PaymentID).Str("event_type", eventType).Msg("payment claimed for booking")

	if err := p.createLedgerEntry(ctx, payload, eventType); err != nil {
		// A payment the ledger refused (or could not book) is a divergence: the
		// payment service believes it settled. Record it where an operator can
		// see it before the error is swallowed by the consumer.
		if accountID, parseErr := uuid.Parse(payload.AccountID); parseErr == nil {
			if recErr := p.ledgerService.RecordPaymentBookingFailure(ctx, paymentID, accountID, err.Error()); recErr != nil {
				p.logger.Error().Err(recErr).
					Str("payment_id", payload.PaymentID).
					Msg("failed to record refused payment booking")
			}
		}

		// Compensate: the claim is taken before the entries are written, so a
		// failed booking must hand it back. Otherwise the payment stays marked
		// as booked with no money moved, and every later delivery is skipped as
		// a duplicate — the debit would be lost silently.
		if releaseErr := p.gate.ReleasePaymentClaim(ctx, paymentID); releaseErr != nil {
			p.logger.Error().Err(releaseErr).
				Str("payment_id", payload.PaymentID).
				Msg("failed to release booking claim after booking failure; payment may need manual repair")
		}
		return err
	}

	return nil
}

func (p *PaymentEventProcessor) createLedgerEntry(ctx context.Context, payload *PaymentEventPayload, eventType string) error {
	accountID, err := uuid.Parse(payload.AccountID)
	if err != nil {
		return err
	}

	if payload.Amount <= 0 {
		return fmt.Errorf("payment %s has non-positive amount %d: %w", payload.PaymentID, payload.Amount, domain.ErrInvalidAmount)
	}

	clearingAccountID := domain.ClearingAccountID

	// An outbound payment takes money OUT of the paying account, so the payer is
	// the DEBIT leg (a DEBIT lowers the balance) and the clearing account is
	// credited. The ledger of record refuses to write that debit unless the
	// account can actually cover it: upstream code can be wrong or racing, and
	// without this check a payment would simply push the balance negative.
	available, err := p.ledgerService.GetAvailableBalance(ctx, accountID)
	if err != nil {
		return fmt.Errorf("checking available balance for account %s: %w", accountID, err)
	}
	if available < payload.Amount {
		return fmt.Errorf(
			"payment %s refused: account %s has %d available, needs %d: %w",
			payload.PaymentID, accountID, available, payload.Amount, domain.ErrInsufficientFunds)
	}

	req := &domain.CreateDoubleEntryRequest{
		IdempotencyKey:  payload.IdempotencyKey,
		TransactionType: domain.TransactionTypePayment,
		Amount:          payload.Amount,
		Currency:        payload.Currency,
		Description:     payload.Reference,
		DebitAccountID:  accountID,
		CreditAccountID: clearingAccountID,
		Lines: []domain.DoubleEntryLine{
			{
				AccountID: accountID,
				EntryType: domain.EntryTypeDebit,
				Amount:    payload.Amount,
				Currency:  payload.Currency,
			},
			{
				AccountID: clearingAccountID,
				EntryType: domain.EntryTypeCredit,
				Amount:    payload.Amount,
				Currency:  payload.Currency,
			},
		},
	}

	tx, entries, err := p.ledgerService.CreateDoubleEntryTransaction(ctx, req)
	if err != nil {
		return err
	}

	p.logger.Info().
		Str("transaction_id", tx.TransactionID.String()).
		Str("payment_id", payload.PaymentID).
		Str("event_type", eventType).
		Int("entries", len(entries)).
		Msg("ledger entry created from payment event")

	return nil
}
