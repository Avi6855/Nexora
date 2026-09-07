package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/clients"
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
	// fraud asks the scam-intelligence engine for an outbound-transfer
	// decision before money leaves the account (nil = checks disabled).
	fraud *clients.FraudClient
	// accounts resolves live account state (lockdown enforcement).
	accounts *clients.AccountClient
}

// SetFraudClient enables the real-time scam-intelligence gate. Called from
// main when FRAUD_SERVICE_URL is configured; tests without a fraud service
// keep the default constructor.
func (s *PaymentService) SetFraudClient(c *clients.FraudClient) {
	s.fraud = c
}

// SetAccountClient enables the lockdown enforcement lookup.
func (s *PaymentService) SetAccountClient(c *clients.AccountClient) {
	s.accounts = c
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
	// ── Pre-flight gates (before ANY state is persisted) ────────────────
	// 1. Emergency lockdown: money out of a locked account is refused,
	//    regardless of risk score. Money in is never affected.
	if s.accounts != nil && req.AccountID != "" {
		info, err := s.accounts.GetAccount(ctx, req.AccountID)
		if err == nil && info.LockdownEnabled {
			return nil, fmt.Errorf("%w: account is locked", domain.ErrBlockedByRisk)
		}
		// Lookup failures never block payments (fail-open): the ledger
		// availability check still guarantees no over-spend.
	}

	// 2. Scam intelligence: evaluate the payment intent over the user's real
	//    history. BLOCK refuses the payment; REVIEW/STEP_UP travel as payment
	//    metadata so the app can warn the user before processing completes.
	if s.fraud != nil && req.Amount > 0 && req.UserID != "" {
		decision, err := s.fraud.EvaluateTransfer(ctx, &clients.TransferRiskRequest{
			RequestID:        req.IdempotencyKey,
			UserID:           req.UserID,
			AccountID:        req.AccountID,
			Amount:           req.Amount,
			Currency:         req.Currency,
			CounterpartyID:   req.CounterpartyID,
			CounterpartyName: req.CounterpartyName,
			Reference:        req.Reference,
		})
		if err != nil {
			s.logger.Warn().Err(err).Msg("risk evaluation unavailable, allowing payment")
		} else {
			if decision.Action == "BLOCK" {
				s.logger.Warn().
					Str("idempotency_key", req.IdempotencyKey).
					Float64("risk_score", decision.RiskScore).
					Msg("payment blocked by risk engine")
				return nil, fmt.Errorf("%w: %s", domain.ErrBlockedByRisk, strings.Join(decision.Reasons, ", "))
			}
			if req.Metadata == nil {
				req.Metadata = map[string]string{}
			}
			req.Metadata["risk_action"] = decision.Action
			req.Metadata["risk_score"] = fmt.Sprintf("%.2f", decision.RiskScore)
			if decision.Advice != "" {
				req.Metadata["risk_advice"] = decision.Advice
			}
		}
	}

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

// GetPaymentsByUser returns all payments belonging to a user (the app's
// payments list).
func (s *PaymentService) GetPaymentsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Payment, error) {
	s.logger.Info().Str("user_id", userID.String()).Msg("getting user payments")
	return s.paymentRepo.GetByUserID(ctx, userID)
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

	if err := s.saga.persistTransition(ctx, payment, events.EventTypePaymentReversed, eventPayload, correlationID); err != nil {
		return fmt.Errorf("persisting reversal: %w", err)
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
