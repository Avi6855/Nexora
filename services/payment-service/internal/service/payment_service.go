package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/repository"
)

type PaymentService struct {
	paymentRepo    repository.PaymentRepository
	saga           *PaymentSaga
	eventPublisher events.EventPublisher
	logger         zerolog.Logger
}

func NewPaymentService(
	paymentRepo repository.PaymentRepository,
	provider provider.PaymentProvider,
	eventPublisher events.EventPublisher,
	logger zerolog.Logger,
) *PaymentService {
	saga := NewPaymentSaga(paymentRepo, provider, eventPublisher, logger)
	return &PaymentService{
		paymentRepo:    paymentRepo,
		saga:           saga,
		eventPublisher: eventPublisher,
		logger:         logger,
	}
}

func (s *PaymentService) CreatePayment(ctx context.Context, req *domain.CreatePaymentRequest) (*domain.Payment, error) {
	correlationID := extractOrCreateCorrelationID(ctx)
	return s.saga.ExecuteCreatePayment(ctx, req, correlationID)
}

func (s *PaymentService) GetPayment(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	s.logger.Info().Str("payment_id", id.String()).Msg("getting payment")
	return s.paymentRepo.GetByID(ctx, id)
}

func (s *PaymentService) GetPaymentsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error) {
	s.logger.Info().Str("account_id", accountID.String()).Msg("getting account payments")
	return s.paymentRepo.GetByAccountID(ctx, accountID)
}

func (s *PaymentService) AuthorizePayment(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	correlationID := extractOrCreateCorrelationID(ctx)
	return s.saga.ExecuteAuthorizePayment(ctx, id.String(), correlationID)
}

func (s *PaymentService) ProcessPayment(ctx context.Context, id uuid.UUID, req *domain.ProcessPaymentRequest) (*domain.Payment, error) {
	correlationID := extractOrCreateCorrelationID(ctx)
	return s.saga.ExecuteProcessPayment(ctx, id.String(), correlationID)
}

func (s *PaymentService) CancelPayment(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	correlationID := extractOrCreateCorrelationID(ctx)
	return s.saga.ExecuteCancelPayment(ctx, id.String(), correlationID)
}

func (s *PaymentService) HandleProviderCallback(ctx context.Context, callback *domain.ProviderCallbackRequest) (*domain.Payment, error) {
	correlationID := extractOrCreateCorrelationID(ctx)
	return s.saga.ExecuteHandleProviderCallback(ctx, callback, correlationID)
}

func (s *PaymentService) HandleTimeout(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	correlationID := extractOrCreateCorrelationID(ctx)
	return s.saga.ExecuteHandleTimeout(ctx, id.String(), correlationID)
}

func (s *PaymentService) FailPayment(ctx context.Context, id uuid.UUID, reason string) error {
	correlationID := extractOrCreateCorrelationID(ctx)
	_, err := s.saga.failPaymentByID(ctx, id.String(), reason, correlationID)
	return err
}

func (s *PaymentService) ReversePayment(ctx context.Context, id uuid.UUID) error {
	correlationID := extractOrCreateCorrelationID(ctx)
	idStr := id.String()

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("getting payment: %w", err)
	}

	if payment.State != domain.PaymentStateConfirmed && payment.State != domain.PaymentStateSettled {
		return fmt.Errorf("cannot reverse payment in state %s, must be CONFIRMED or SETTLED", payment.State)
	}

	if err := payment.TransitionTo(domain.PaymentStateReversed); err != nil {
		return fmt.Errorf("transition failed: %w", err)
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		return fmt.Errorf("updating payment: %w", err)
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentReversed, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.reversed event")
	}

	s.logger.Info().
		Str("payment_id", idStr).
		Msg("payment reversed")

	return nil
}

func (s *PaymentService) SettlePayment(ctx context.Context, id uuid.UUID) error {
	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("getting payment: %w", err)
	}

	if err := payment.TransitionTo(domain.PaymentStateSettled); err != nil {
		return fmt.Errorf("transition failed: %w", err)
	}

	return s.paymentRepo.Update(ctx, payment)
}

func extractOrCreateCorrelationID(ctx context.Context) string {
	type contextKey string
	const correlationIDKey contextKey = "correlation_id"

	if val := ctx.Value(correlationIDKey); val != nil {
		if str, ok := val.(string); ok && str != "" {
			return str
		}
	}

	return uuid.New().String()
}

func (s *PaymentSaga) failPaymentByID(ctx context.Context, paymentID string, reason string, correlationID string) (*domain.Payment, error) {
	id, err := parseUUID(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment ID: %w", err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting payment: %w", err)
	}

	s.failPayment(ctx, payment, reason, correlationID)

	return payment, nil
}

func (s *PaymentService) HandlePaymentEvent(ctx context.Context, eventType string, payload []byte) error {
	s.logger.Info().
		Str("event_type", eventType).
		Msg("processing payment event from Kafka")

	return nil
}
