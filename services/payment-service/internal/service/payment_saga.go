package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/repository"
)

type SagaStep func(ctx context.Context, payment *domain.Payment) error

type CompensationStep func(ctx context.Context, payment *domain.Payment) error

type PaymentSaga struct {
	paymentRepo   repository.PaymentRepository
	provider      provider.PaymentProvider
	eventPublisher events.EventPublisher
	logger        zerolog.Logger
}

func NewPaymentSaga(
	paymentRepo repository.PaymentRepository,
	provider provider.PaymentProvider,
	eventPublisher events.EventPublisher,
	logger zerolog.Logger,
) *PaymentSaga {
	return &PaymentSaga{
		paymentRepo:    paymentRepo,
		provider:       provider,
		eventPublisher: eventPublisher,
		logger:         logger,
	}
}

func (s *PaymentSaga) ExecuteCreatePayment(ctx context.Context, req *domain.CreatePaymentRequest, correlationID string) (*domain.Payment, error) {
	s.logger.Info().
		Str("idempotency_key", req.IdempotencyKey).
		Str("correlation_id", correlationID).
		Msg("executing create payment saga")

	existing, err := s.paymentRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Info().
			Str("payment_id", existing.PaymentID.String()).
			Msg("returning existing payment for idempotency key")
		return existing, nil
	}

	accountID, err := parseUUID(req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid account ID: %w", err)
	}

	payment := domain.NewPayment(
		req.IdempotencyKey,
		accountID,
		uuidNil(),
		req.PaymentType,
		req.Amount,
		req.Currency,
		req.CounterpartyID,
		req.CounterpartyName,
		req.Reference,
	)

	if req.Metadata != nil {
		payment.Metadata = req.Metadata
	}

	if err := s.paymentRepo.Create(ctx, payment); err != nil {
		return nil, fmt.Errorf("storing payment: %w", err)
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
		Metadata:         payment.Metadata,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentCreated, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.created event")
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Msg("payment saga: created")

	return payment, nil
}

func (s *PaymentSaga) ExecuteAuthorizePayment(ctx context.Context, paymentID string, correlationID string) (*domain.Payment, error) {
	s.logger.Info().
		Str("payment_id", paymentID).
		Str("correlation_id", correlationID).
		Msg("executing authorize payment saga")

	id, err := parseUUID(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment ID: %w", err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting payment: %w", err)
	}

	if payment.State != domain.PaymentStateCreated {
		return nil, fmt.Errorf("cannot authorize payment in state %s, must be CREATED", payment.State)
	}

	if err := payment.TransitionTo(domain.PaymentStateAuthorized); err != nil {
		return nil, fmt.Errorf("transition failed: %w", err)
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		return nil, fmt.Errorf("updating payment: %w", err)
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateCreated),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentAuthorized, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.authorized event")
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Msg("payment saga: authorized")

	return payment, nil
}

func (s *PaymentSaga) ExecuteProcessPayment(ctx context.Context, paymentID string, correlationID string) (*domain.Payment, error) {
	s.logger.Info().
		Str("payment_id", paymentID).
		Str("correlation_id", correlationID).
		Msg("executing process payment saga")

	id, err := parseUUID(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment ID: %w", err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting payment: %w", err)
	}

	if payment.State != domain.PaymentStateAuthorized {
		return nil, fmt.Errorf("cannot process payment in state %s, must be AUTHORIZED", payment.State)
	}

	if err := payment.TransitionTo(domain.PaymentStateProcessing); err != nil {
		return nil, fmt.Errorf("transition failed: %w", err)
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		return nil, fmt.Errorf("updating payment: %w", err)
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateAuthorized),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentProcessing, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.processing event")
	}

	payReq := &provider.PaymentRequest{
		PaymentID:      payment.PaymentID.String(),
		AccountID:      payment.AccountID.String(),
		Amount:         payment.Amount,
		Currency:       payment.Currency,
		CounterpartyID: payment.CounterpartyID,
		Reference:      payment.Reference,
	}

	result, err := s.provider.ProcessPayment(ctx, payReq)
	if err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("provider error")
		s.handleProviderError(ctx, payment, err, correlationID)
		return payment, nil
	}

	s.handleProviderResult(ctx, payment, result, correlationID)

	return payment, nil
}

func (s *PaymentSaga) handleProviderResult(ctx context.Context, payment *domain.Payment, result *provider.PaymentResult, correlationID string) {
	payment.ProviderResponse = &domain.ProviderResponse{
		ProviderID:     result.ProviderID,
		TransactionRef: result.TransactionRef,
		Status:         result.Status,
		Message:        result.Message,
		ResponseCode:   result.ResponseCode,
		ReceivedAt:     result.ProcessedAt,
	}

	switch result.Status {
	case "SUCCESS":
		s.confirmAndSettle(ctx, payment, correlationID)
	case "FAILED":
		s.failPayment(ctx, payment, result.Message, correlationID)
	case "UNKNOWN":
		s.markUnknown(ctx, payment, correlationID)
	default:
		s.failPayment(ctx, payment, fmt.Sprintf("unexpected provider status: %s", result.Status), correlationID)
	}
}

func (s *PaymentSaga) confirmAndSettle(ctx context.Context, payment *domain.Payment, correlationID string) {
	if err := payment.TransitionTo(domain.PaymentStateConfirmed); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to transition to CONFIRMED")
		return
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to update payment to CONFIRMED")
		return
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateProcessing),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentConfirmed, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.confirmed event")
	}

	s.settlePayment(ctx, payment, correlationID)
}

func (s *PaymentSaga) settlePayment(ctx context.Context, payment *domain.Payment, correlationID string) {
	if err := payment.TransitionTo(domain.PaymentStateSettled); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to transition to SETTLED")
		return
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to update payment to SETTLED")
		return
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateConfirmed),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentSettled, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.settled event")
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Msg("payment saga: settled")
}

func (s *PaymentSaga) handleProviderError(ctx context.Context, payment *domain.Payment, err error, correlationID string) {
	if ctx.Err() != nil || err == context.DeadlineExceeded || err == context.Canceled {
		s.markUnknown(ctx, payment, correlationID)
		return
	}

	s.failPayment(ctx, payment, err.Error(), correlationID)
}

func (s *PaymentSaga) failPayment(ctx context.Context, payment *domain.Payment, reason string, correlationID string) {
	if err := payment.TransitionTo(domain.PaymentStateFailed); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to transition to FAILED")
		return
	}

	payment.FailureReason = reason
	payment.UpdatedAt = time.Now().UTC()

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to update payment to FAILED")
		return
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		FailureReason:    reason,
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentFailed, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.failed event")
	}

	s.releaseReservation(ctx, payment, correlationID)

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Str("reason", reason).
		Msg("payment saga: failed")
}

func (s *PaymentSaga) markUnknown(ctx context.Context, payment *domain.Payment, correlationID string) {
	if err := payment.TransitionTo(domain.PaymentStateUnknown); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to transition to UNKNOWN")
		return
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to update payment to UNKNOWN")
		return
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateProcessing),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentUnknown, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.unknown event")
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Msg("payment saga: marked unknown")
}

func (s *PaymentSaga) ExecuteCancelPayment(ctx context.Context, paymentID string, correlationID string) (*domain.Payment, error) {
	s.logger.Info().
		Str("payment_id", paymentID).
		Str("correlation_id", correlationID).
		Msg("executing cancel payment saga")

	id, err := parseUUID(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment ID: %w", err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting payment: %w", err)
	}

	if payment.State != domain.PaymentStateCreated && payment.State != domain.PaymentStateAuthorized {
		return nil, fmt.Errorf("cannot cancel payment in state %s, must be CREATED or AUTHORIZED", payment.State)
	}

	if err := payment.TransitionTo(domain.PaymentStateCancelled); err != nil {
		return nil, fmt.Errorf("transition failed: %w", err)
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		return nil, fmt.Errorf("updating payment: %w", err)
	}

	s.releaseReservation(ctx, payment, correlationID)

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateCreated),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentCancelled, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish payment.cancelled event")
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Msg("payment saga: cancelled")

	return payment, nil
}

func (s *PaymentSaga) ExecuteHandleProviderCallback(ctx context.Context, callback *domain.ProviderCallbackRequest, correlationID string) (*domain.Payment, error) {
	s.logger.Info().
		Str("payment_id", callback.PaymentID).
		Str("provider_id", callback.ProviderID).
		Str("status", callback.Status).
		Str("correlation_id", correlationID).
		Msg("handling provider callback")

	id, err := parseUUID(callback.PaymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment ID: %w", err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting payment: %w", err)
	}

	if payment.State != domain.PaymentStateProcessing {
		return nil, fmt.Errorf("cannot handle callback for payment in state %s, must be PROCESSING", payment.State)
	}

	payment.ProviderResponse = &domain.ProviderResponse{
		ProviderID:     callback.ProviderID,
		TransactionRef: callback.TransactionRef,
		Status:         callback.Status,
		Message:        callback.Message,
		ResponseCode:   callback.ResponseCode,
		ReceivedAt:     time.Now().UTC(),
	}

	switch callback.Status {
	case "SUCCESS":
		s.confirmAndSettle(ctx, payment, correlationID)
	case "FAILED":
		s.failPayment(ctx, payment, callback.Message, correlationID)
	case "UNKNOWN":
		s.markUnknown(ctx, payment, correlationID)
	default:
		s.failPayment(ctx, payment, fmt.Sprintf("unexpected callback status: %s", callback.Status), correlationID)
	}

	return payment, nil
}

func (s *PaymentSaga) ExecuteHandleTimeout(ctx context.Context, paymentID string, correlationID string) (*domain.Payment, error) {
	s.logger.Info().
		Str("payment_id", paymentID).
		Str("correlation_id", correlationID).
		Msg("handling payment timeout")

	id, err := parseUUID(paymentID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment ID: %w", err)
	}

	payment, err := s.paymentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting payment: %w", err)
	}

	if payment.State != domain.PaymentStateProcessing {
		return nil, fmt.Errorf("cannot handle timeout for payment in state %s, must be PROCESSING", payment.State)
	}

	s.markUnknown(ctx, payment, correlationID)

	return payment, nil
}

func (s *PaymentSaga) releaseReservation(ctx context.Context, payment *domain.Payment, correlationID string) {
	if payment.ReservationID == "" {
		return
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Str("reservation_id", payment.ReservationID).
		Msg("releasing reservation")

	eventPayload := events.PaymentEventPayload{
		PaymentID:     payment.PaymentID.String(),
		AccountID:     payment.AccountID.String(),
		ReservationID: payment.ReservationID,
		Amount:        payment.Amount,
		Currency:      payment.Currency,
	}

	if err := s.eventPublisher.PublishPaymentEvent(ctx, events.EventTypePaymentReversed, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to publish reservation release event")
	}
}

func parseUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.UUID{}, fmt.Errorf("empty string")
	}
	return uuid.Parse(s)
}

func uuidNil() uuid.UUID {
	return uuid.UUID{}
}
