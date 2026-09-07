package events

import (
	"context"

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
}

// BookingGate claims a payment exactly once (LWT) so duplicate lifecycle
// events (confirmed + settled for the same payment) never double-book.
type BookingGate interface {
	MarkPaymentBooked(ctx context.Context, paymentID uuid.UUID, idempotencyKey, eventType string) (bool, error)
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
	return p.createLedgerEntry(ctx, payload, eventType)
}

func (p *PaymentEventProcessor) createLedgerEntry(ctx context.Context, payload *PaymentEventPayload, eventType string) error {
	accountID, err := uuid.Parse(payload.AccountID)
	if err != nil {
		return err
	}

	clearingAccountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	req := &domain.CreateDoubleEntryRequest{
		IdempotencyKey:  payload.IdempotencyKey,
		TransactionType: domain.TransactionTypePayment,
		Amount:          payload.Amount,
		Currency:        payload.Currency,
		Description:     payload.Reference,
		DebitAccountID:  clearingAccountID,
		CreditAccountID: accountID,
		Lines: []domain.DoubleEntryLine{
			{
				AccountID: clearingAccountID,
				EntryType: domain.EntryTypeDebit,
				Amount:    payload.Amount,
				Currency:  payload.Currency,
			},
			{
				AccountID: accountID,
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
