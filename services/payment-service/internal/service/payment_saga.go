package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/clients"
	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/repository"
	"github.com/nexora/nexora/shared/outbox"
)

// authorizationHoldTTL is how long a payment's hold lives at the ledger. It is
// the fenced window in which the payment must reach a terminal state; a hold
// that is never released expires on its own rather than stranding the money
// forever, which is the failure mode that matters when a retry never arrives.
const authorizationHoldTTL = "30m"

type SagaStep func(ctx context.Context, payment *domain.Payment) error

type CompensationStep func(ctx context.Context, payment *domain.Payment) error

// FundHolder reserves and releases funds availability at the ledger of record.
// Payment authorisation takes a hold so the money cannot be spent twice while
// the payment is in flight; terminal outcomes give the hold back. Implemented
// by clients.LedgerClient; nil in environments without a ledger (the saga then
// skips holding, exactly as it did before holds existed).
type FundHolder interface {
	Reserve(ctx context.Context, accountID uuid.UUID, amount int64, currency string, txID uuid.UUID, ttl string) (*clients.LedgerReservation, error)
	ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error
}

type PaymentSaga struct {
	paymentRepo    repository.PaymentRepository
	provider       provider.PaymentProvider
	eventPublisher events.EventPublisher
	logger         zerolog.Logger
	producer       string
	topicPrefix    string
	holder         FundHolder
}

// SetFundHolder enables ledger-backed authorisation holds.
func (s *PaymentSaga) SetFundHolder(h FundHolder) {
	s.holder = h
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
		producer:       "payment-service",
		topicPrefix:    "nexora",
	}
}

// persistTransition persists the payment state transition and its outbox
// event in ONE atomic operation when the repository supports it (logged
// Cassandra batch — ADR-005). This eliminates the dual-write problem: the
// payment can never change state without its event being durably recorded.
// Repositories without atomic support (e.g. in-memory test doubles) fall back
// to a sequential update + enqueue.
func (s *PaymentSaga) persistTransition(ctx context.Context, payment *domain.Payment, eventType events.EventType, payload interface{}, correlationID string) error {
	if aw, ok := s.paymentRepo.(repository.AtomicEventWriter); ok {
		ev, err := events.BuildPaymentOutboxEvent(eventType, payload, correlationID, s.producer, s.topicPrefix)
		if err != nil {
			return fmt.Errorf("building outbox event: %w", err)
		}
		return aw.UpdateWithEvents(ctx, payment, []*outbox.Event{ev})
	}

	if err := s.paymentRepo.Update(ctx, payment); err != nil {
		return err
	}
	return s.eventPublisher.PublishPaymentEvent(ctx, eventType, payload, correlationID)
}

// persistCreate atomically persists a new payment together with its first
// outbox event (payment.created).
func (s *PaymentSaga) persistCreate(ctx context.Context, payment *domain.Payment, eventType events.EventType, payload interface{}, correlationID string) error {
	if aw, ok := s.paymentRepo.(repository.AtomicEventWriter); ok {
		ev, err := events.BuildPaymentOutboxEvent(eventType, payload, correlationID, s.producer, s.topicPrefix)
		if err != nil {
			return fmt.Errorf("building outbox event: %w", err)
		}
		return aw.CreateWithEvents(ctx, payment, []*outbox.Event{ev})
	}

	if err := s.paymentRepo.Create(ctx, payment); err != nil {
		return err
	}
	return s.eventPublisher.PublishPaymentEvent(ctx, eventType, payload, correlationID)
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

	userID := uuidNil()
	if req.UserID != "" {
		if parsed, err := uuid.Parse(req.UserID); err == nil {
			userID = parsed
		}
	}

	payment := domain.NewPayment(
		req.IdempotencyKey,
		accountID,
		userID,
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

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
		Metadata:         payment.Metadata,
	}

	// Claim the idempotency key before writing. The read above is only an
	// optimisation: two requests that arrive together can both miss it and both
	// insert, which would move money twice for one user action. The claim is a
	// compare-and-set, so exactly one writer wins; the loser returns the
	// winner's payment instead of creating a second one.
	if claimer, ok := s.paymentRepo.(repository.IdempotencyClaimer); ok {
		existingID, claimed, err := claimer.ClaimIdempotencyKey(ctx, req.IdempotencyKey, payment.PaymentID)
		if err != nil {
			return nil, fmt.Errorf("claiming idempotency key: %w", err)
		}

		if !claimed {
			winner, err := s.loadClaimedPayment(ctx, existingID)
			if err != nil {
				return nil, fmt.Errorf("idempotency key %q is claimed by payment %s: %w", req.IdempotencyKey, existingID, err)
			}
			s.logger.Info().
				Str("idempotency_key", req.IdempotencyKey).
				Str("payment_id", winner.PaymentID.String()).
				Msg("idempotency key already claimed, returning winning payment")
			return winner, nil
		}

		if err := s.persistCreate(ctx, payment, events.EventTypePaymentCreated, eventPayload, correlationID); err != nil {
			// Compensate: a claimed key with no payment would block the client's
			// retry on its own key forever, so free it before failing.
			if releaseErr := claimer.ReleaseIdempotencyClaim(ctx, req.IdempotencyKey); releaseErr != nil {
				s.logger.Error().Err(releaseErr).
					Str("idempotency_key", req.IdempotencyKey).
					Msg("failed to release idempotency claim after create failure")
			}
			return nil, fmt.Errorf("storing payment with event: %w", err)
		}

		s.logger.Info().
			Str("payment_id", payment.PaymentID.String()).
			Msg("payment saga: created (idempotency key claimed)")

		return payment, nil
	}

	if err := s.persistCreate(ctx, payment, events.EventTypePaymentCreated, eventPayload, correlationID); err != nil {
		return nil, fmt.Errorf("storing payment with event: %w", err)
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Msg("payment saga: created")

	return payment, nil
}

// loadClaimedPayment loads the payment that owns an idempotency key. The
// winning writer persists the payment immediately after claiming the key, so a
// loser of the race can arrive inside the window where the claim exists but the
// row is not readable yet. A short bounded wait turns that transient state into
// the winning payment instead of a spurious error for a user double-tapping.
func (s *PaymentSaga) loadClaimedPayment(ctx context.Context, paymentID uuid.UUID) (*domain.Payment, error) {
	const attempts = 3
	const wait = 20 * time.Millisecond

	var lastErr error
	for i := 0; i < attempts; i++ {
		payment, err := s.paymentRepo.GetByID(ctx, paymentID)
		if err == nil && payment != nil {
			return payment, nil
		}
		lastErr = err
		if i < attempts-1 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("payment %s not found", paymentID)
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

	// ── Hold the funds before the payment is authorised ─────────────────
	// Authorising is a promise that the money is there. Without a hold the
	// balance stays fully spendable while the payment is in flight, so the same
	// money can be committed twice (a card spend plus this payment) and nothing
	// notices until the settlement is booked. The hold is taken BEFORE the state
	// change, and a refusal leaves the payment in CREATED: a payment that could
	// not be funded must never look authorised.
	if s.holder != nil {
		reservation, err := s.holder.Reserve(ctx, payment.AccountID, payment.Amount, payment.Currency, payment.PaymentID, authorizationHoldTTL)
		if err != nil {
			if clients.IsInsufficientFunds(err) {
				s.logger.Warn().
					Str("payment_id", payment.PaymentID.String()).
					Int64("amount", payment.Amount).
					Msg("payment not authorised: insufficient available balance")
				return nil, fmt.Errorf("%w: payment %s needs %d %s",
					clients.ErrInsufficientFunds, payment.PaymentID, payment.Amount, payment.Currency)
			}
			// Fail closed. Treating a ledger outage as "no hold needed" would
			// authorise payments against money we cannot prove exists.
			s.logger.Error().Err(err).
				Str("payment_id", payment.PaymentID.String()).
				Msg("cannot hold funds, refusing to authorise payment")
			return nil, fmt.Errorf("holding funds for payment %s: %w", payment.PaymentID, err)
		}
		payment.ReservationID = reservation.ReservationID.String()
		s.logger.Info().
			Str("payment_id", payment.PaymentID.String()).
			Str("reservation_id", payment.ReservationID).
			Msg("funds held for payment")
	}

	if err := payment.TransitionTo(domain.PaymentStateAuthorized); err != nil {
		return nil, fmt.Errorf("transition failed: %w", err)
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateCreated),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentAuthorized, eventPayload, correlationID); err != nil {
		// Compensate: the hold was taken for a transition we could not persist,
		// so hand the funds straight back instead of leaving them held until
		// the TTL expires.
		s.releaseReservation(ctx, payment, correlationID, "authorization could not be persisted")
		return nil, fmt.Errorf("persisting authorization: %w", err)
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

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateAuthorized),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentProcessing, eventPayload, correlationID); err != nil {
		return nil, fmt.Errorf("persisting processing state: %w", err)
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

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateProcessing),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentConfirmed, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to persist confirmed transition; payment remains PROCESSING")
		return
	}

	s.settlePayment(ctx, payment, correlationID)
}

func (s *PaymentSaga) settlePayment(ctx context.Context, payment *domain.Payment, correlationID string) {
	if err := payment.TransitionTo(domain.PaymentStateSettled); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to transition to SETTLED")
		return
	}

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateConfirmed),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentSettled, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to persist settled transition; payment remains CONFIRMED")
		return
	}

	// The settlement event is durably in the outbox, so the ledger will book the
	// real debit from it. The hold has done its job and must now be given back:
	// holding AND debiting the same money would reduce the customer's available
	// balance by the amount twice, permanently.
	s.releaseReservation(ctx, payment, correlationID, "payment settled")

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

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		FailureReason:    reason,
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentFailed, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to persist failed transition")
		return
	}

	s.releaseReservation(ctx, payment, correlationID, reason)

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

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateProcessing),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentUnknown, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Str("payment_id", payment.PaymentID.String()).Msg("failed to persist UNKNOWN transition")
		return
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

	eventPayload := events.PaymentEventPayload{
		PaymentID:        payment.PaymentID.String(),
		IdempotencyKey:   payment.IdempotencyKey,
		AccountID:        payment.AccountID.String(),
		UserID:           payment.UserID.String(),
		PaymentType:      string(payment.PaymentType),
		Amount:           payment.Amount,
		Currency:         payment.Currency,
		State:            string(payment.State),
		PreviousState:    string(domain.PaymentStateCreated),
		CounterpartyID:   payment.CounterpartyID,
		CounterpartyName: payment.CounterpartyName,
		Reference:        payment.Reference,
	}

	if err := s.persistTransition(ctx, payment, events.EventTypePaymentCancelled, eventPayload, correlationID); err != nil {
		return nil, fmt.Errorf("persisting cancellation: %w", err)
	}

	s.releaseReservation(ctx, payment, correlationID, "payment cancelled")

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

// releaseReservation gives a payment's hold back to the customer. It is the
// compensation for every path where money did not leave the account: a failed
// authorisation, a failed payment, a cancellation, or a settlement whose real
// debit has already been booked. Failure here is never fatal to the caller's
// state transition (that is already durable) but it is always logged loudly,
// because an unreleased hold keeps the customer's money unusable until it
// expires — money that is theirs and we cannot spend.
//
// The hold is deliberately NOT released on the UNKNOWN path: an unknown
// provider outcome means the money may still have moved, so the funds stay
// held and reconciliation decides. Releasing there would make a payment we
// cannot account for spendable a second time.
func (s *PaymentSaga) releaseReservation(ctx context.Context, payment *domain.Payment, correlationID, reason string) {
	if payment.ReservationID == "" {
		return
	}

	reservationID := payment.ReservationID

	if s.holder != nil {
		id, err := uuid.Parse(reservationID)
		if err != nil {
			s.logger.Error().Err(err).
				Str("payment_id", payment.PaymentID.String()).
				Str("reservation_id", reservationID).
				Msg("hold has an unparseable reservation id; funds stay held until it expires")
			return
		}
		if err := s.holder.ReleaseReservation(ctx, id); err != nil {
			s.logger.Error().Err(err).
				Str("payment_id", payment.PaymentID.String()).
				Str("reservation_id", reservationID).
				Str("reason", reason).
				Msg("failed to release hold; funds stay held until the hold expires")
			return
		}
	}

	s.logger.Info().
		Str("payment_id", payment.PaymentID.String()).
		Str("reservation_id", reservationID).
		Str("reason", reason).
		Msg("released payment hold")

	// Cleared in memory so a second terminal handler in the same call chain
	// cannot release the same hold twice.
	payment.ReservationID = ""

	eventPayload := events.PaymentEventPayload{
		PaymentID:     payment.PaymentID.String(),
		AccountID:     payment.AccountID.String(),
		UserID:        payment.UserID.String(),
		ReservationID: reservationID,
		Amount:        payment.Amount,
		Currency:      payment.Currency,
	}

	// A dedicated event, not payment.reversed: reversal means money is going
	// back to the customer, and a consumer that treats this release as a
	// reversal would pay them again for a payment that never left.
	if err := s.persistTransition(ctx, payment, events.EventTypePaymentReservationReleased, eventPayload, correlationID); err != nil {
		s.logger.Error().Err(err).Msg("failed to persist hold-release event")
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
