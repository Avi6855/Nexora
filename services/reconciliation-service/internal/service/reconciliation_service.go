package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/reconciliation-service/internal/clients"
	"github.com/nexora/nexora/services/reconciliation-service/internal/domain"
	"github.com/nexora/nexora/services/reconciliation-service/internal/events"
	"github.com/nexora/nexora/services/reconciliation-service/internal/repository"
	"github.com/rs/zerolog"
)

const (
	// unknownPaymentState is how payment-service spells "we do not know yet"
	// (ADR-007). A case whose payment reads UNKNOWN is unresolved work, not a
	// match: both sides agreeing on "unknown" means nobody knows anything.
	unknownPaymentState = "UNKNOWN"

	// defaultMaxAttempts matches the case model's own default.
	defaultMaxAttempts = 3
)

// PaymentGateway is reconciliation's only route back into payment-service.
// Without it a case could be resolved on paper while the payment stayed UNKNOWN
// and the customer's authorisation hold stayed in place — the exact failure this
// service exists to prevent.
type PaymentGateway interface {
	// GetPayment reads the payment's current state, so the sweep compares
	// against the payment rather than against a stale copy of it.
	GetPayment(ctx context.Context, paymentID string) (*clients.PaymentSnapshot, error)
	// ResolveUnknown concludes an UNKNOWN payment with the outcome
	// reconciliation established.
	ResolveUnknown(ctx context.Context, paymentID, outcome, reason string) error
}

type ReconciliationService struct {
	reconRepo repository.ReconciliationRepository
	producer  *events.KafkaProducer
	// gateway is optional: without a configured payment-service the service
	// still records and escalates cases, it just cannot write the outcome back.
	gateway PaymentGateway
	logger  zerolog.Logger
}

func NewReconciliationService(reconRepo repository.ReconciliationRepository, producer *events.KafkaProducer, logger zerolog.Logger) *ReconciliationService {
	return &ReconciliationService{reconRepo: reconRepo, producer: producer, logger: logger}
}

// SetPaymentGateway enables the reconciliation write-back path. Called from main
// when payment-service is reachable.
func (s *ReconciliationService) SetPaymentGateway(g PaymentGateway) {
	s.gateway = g
}

// publish emits an operational event if the producer is configured. A missing
// Kafka producer must never fail a reconciliation decision: the decision is
// already durable in Cassandra, the event is only how other services hear about
// it.
func (s *ReconciliationService) publish(ctx context.Context, eventType string, payload map[string]interface{}) {
	if s.producer == nil {
		return
	}
	if err := s.producer.Publish(ctx, eventType, payload); err != nil {
		s.logger.Warn().Err(err).Str("event_type", eventType).Msg("failed to publish reconciliation event")
	}
}

func (s *ReconciliationService) ReconcilePayment(ctx context.Context, paymentID uuid.UUID, providerResponse *domain.ProviderPaymentStatus) (*domain.ReconcileResult, error) {
	s.logger.Info().Str("payment_id", paymentID.String()).Msg("reconciling payment")

	existingCase, _ := s.reconRepo.GetCaseByPaymentID(ctx, paymentID)
	if existingCase != nil && existingCase.Status == domain.ReconciliationStatusResolved {
		return &domain.ReconcileResult{
			CaseID:        existingCase.CaseID,
			PaymentID:     paymentID,
			Matched:       true,
			InternalState: existingCase.InternalState,
			ExternalState: providerResponse.Status,
			Resolution:    existingCase.Resolution,
			ReconciledAt:  time.Now().UTC(),
		}, nil
	}

	internalState := "UNKNOWN"
	var internalAmount int64
	currency := providerResponse.Currency

	if existingCase != nil {
		internalState = existingCase.InternalState
		internalAmount = existingCase.InternalAmount
	} else {
		internalAmount = providerResponse.Amount
	}

	externalState := providerResponse.Status
	externalAmount := providerResponse.Amount

	matched := internalState == externalState && internalAmount == externalAmount

	now := time.Now().UTC()
	if matched {
		resolution := domain.ResolutionTypeAutoMatch
		if existingCase != nil {
			existingCase.Status = domain.ReconciliationStatusMatched
			existingCase.ExternalState = externalState
			existingCase.Resolution = resolution
			existingCase.ResolvedAt = &now
			existingCase.UpdatedAt = now
			s.reconRepo.UpdateCaseStatus(ctx, existingCase.CaseID, domain.ReconciliationStatusMatched, "")
			s.reconRepo.UpdateCaseResolution(ctx, existingCase.CaseID, resolution, "auto-matched")
		} else {
			reconCase := domain.NewReconciliationCase(paymentID, internalState, externalState, internalAmount, externalAmount, currency)
			reconCase.Status = domain.ReconciliationStatusMatched
			reconCase.Resolution = resolution
			reconCase.ResolvedAt = &now
			reconCase.ProviderRef = providerResponse.TransactionRef
			s.reconRepo.CreateCase(ctx, reconCase)
			existingCase = reconCase
		}

		s.logger.Info().Str("payment_id", paymentID.String()).Msg("payment reconciled - match")
		return &domain.ReconcileResult{
			CaseID:        existingCase.CaseID,
			PaymentID:     paymentID,
			Matched:       true,
			InternalState: internalState,
			ExternalState: externalState,
			Resolution:    resolution,
			ReconciledAt:  now,
		}, nil
	}

	reason := fmt.Sprintf("internal=%s(%d) vs external=%s(%d)", internalState, internalAmount, externalState, externalAmount)
	var reconCase *domain.ReconciliationCase
	if existingCase != nil {
		reconCase = existingCase
		reconCase.ExternalState = externalState
		reconCase.ExternalAmount = externalAmount
		reconCase.DiscrepancyReason = reason
		reconCase.UpdatedAt = now
		s.reconRepo.UpdateCaseStatus(ctx, reconCase.CaseID, domain.ReconciliationStatusDiscrepancy, reason)
	} else {
		reconCase = domain.NewReconciliationCase(paymentID, internalState, externalState, internalAmount, externalAmount, currency)
		reconCase.DiscrepancyReason = reason
		reconCase.ProviderRef = providerResponse.TransactionRef
		s.reconRepo.CreateCase(ctx, reconCase)
	}

	s.logger.Warn().Str("payment_id", paymentID.String()).Str("reason", reason).Msg("payment reconciled - discrepancy")
	return &domain.ReconcileResult{
		CaseID:        reconCase.CaseID,
		PaymentID:     paymentID,
		Matched:       false,
		InternalState: internalState,
		ExternalState: externalState,
		Discrepancy:   reason,
		ReconciledAt:  now,
	}, nil
}

func (s *ReconciliationService) ReconcileUnknownPayment(ctx context.Context, paymentID uuid.UUID) (*domain.ReconcileResult, error) {
	s.logger.Info().Str("payment_id", paymentID.String()).Msg("reconciling unknown payment")

	existingCase, err := s.reconRepo.GetCaseByPaymentID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("no reconciliation case found for payment: %w", err)
	}

	s.reconRepo.IncrementAttemptCount(ctx, existingCase.CaseID)

	if existingCase.AttemptCount >= existingCase.MaxAttempts {
		s.reconRepo.UpdateCaseStatus(ctx, existingCase.CaseID, domain.ReconciliationStatusEscalated, "max attempts exceeded")
		return &domain.ReconcileResult{
			CaseID:        existingCase.CaseID,
			PaymentID:     paymentID,
			Matched:       false,
			InternalState: existingCase.InternalState,
			ExternalState: "UNKNOWN",
			Discrepancy:   "provider check failed after max attempts",
			ReconciledAt:  time.Now().UTC(),
		}, nil
	}

	s.logger.Warn().Str("payment_id", paymentID.String()).Int("attempt", existingCase.AttemptCount).Msg("unknown payment - will retry")
	return &domain.ReconcileResult{
		CaseID:        existingCase.CaseID,
		PaymentID:     paymentID,
		Matched:       false,
		InternalState: existingCase.InternalState,
		ExternalState: "UNKNOWN",
		ReconciledAt:  time.Now().UTC(),
	}, nil
}

func (s *ReconciliationService) RunScheduledReconciliation(ctx context.Context, batchSize int) ([]*domain.ReconcileResult, error) {
	s.logger.Info().Int("batch_size", batchSize).Msg("running scheduled reconciliation")

	if batchSize <= 0 {
		batchSize = 50
	}

	cases, err := s.reconRepo.GetCasesByStatus(ctx, domain.ReconciliationStatusPending, batchSize)
	if err != nil {
		return nil, fmt.Errorf("fetching pending cases: %w", err)
	}

	results := make([]*domain.ReconcileResult, 0, len(cases))
	for _, c := range cases {
		results = append(results, s.reconcileCase(ctx, c))
	}

	s.logger.Info().Int("processed", len(results)).Msg("scheduled reconciliation complete")
	return results, nil
}

// reconcileCase is one case's turn in the sweep. It answers, in order: do the
// two sides already agree, what does payment-service say right now, is there
// evidence on hand that concludes the payment, and if none of that settles it —
// retry, then escalate to a human.
func (s *ReconciliationService) reconcileCase(ctx context.Context, c *domain.ReconciliationCase) *domain.ReconcileResult {
	result := &domain.ReconcileResult{
		CaseID:        c.CaseID,
		PaymentID:     c.PaymentID,
		Matched:       c.Status == domain.ReconciliationStatusMatched,
		InternalState: c.InternalState,
		ExternalState: c.ExternalState,
		ReconciledAt:  time.Now().UTC(),
	}

	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	// 1. Both sides agree on a real state: nothing to reconcile. UNKNOWN is
	//    deliberately excluded — two copies of "we do not know" is not a
	//    match, and treating it as one would close every case the moment it
	//    was opened.
	if c.InternalState != unknownPaymentState && c.InternalState == c.ExternalState && c.InternalAmount == c.ExternalAmount {
		result.Matched = true
		result.Resolution = domain.ResolutionTypeAutoMatch
		s.reconRepo.UpdateCaseStatus(ctx, c.CaseID, domain.ReconciliationStatusMatched, "scheduled auto-match")
		s.reconRepo.UpdateCaseResolution(ctx, c.CaseID, domain.ResolutionTypeAutoMatch, "scheduled batch reconciliation")
		return result
	}

	if s.gateway != nil {
		snapshot, err := s.gateway.GetPayment(ctx, c.PaymentID.String())
		switch {
		case err != nil:
			// Cannot prove anything about the payment. Count the attempt anyway
			// so a payment-service that stays unreachable escalates instead of
			// spinning forever.
			s.logger.Warn().Err(err).Str("payment_id", c.PaymentID.String()).Msg("cannot read payment state during reconciliation sweep")
			result.Discrepancy = "payment state unavailable: " + err.Error()
			s.recordAttempt(ctx, c, maxAttempts, result)
			return result

		case snapshot.State != "" && snapshot.State != unknownPaymentState:
			// The payment concluded without reconciliation: a late provider
			// callback settled or failed it. The payment is the source of truth,
			// so the case closes itself against it rather than second-guessing
			// money that already moved.
			s.logger.Info().
				Str("payment_id", c.PaymentID.String()).
				Str("payment_state", snapshot.State).
				Msg("payment already concluded; closing reconciliation case")
			s.reconRepo.UpdateCaseExternalState(ctx, c.CaseID, snapshot.State, snapshot.Amount)
			s.reconRepo.UpdateCaseStatus(ctx, c.CaseID, domain.ReconciliationStatusMatched, "payment concluded as "+snapshot.State)
			s.reconRepo.UpdateCaseResolution(ctx, c.CaseID, domain.ResolutionTypeAutoMatch, "closed by payment state "+snapshot.State)
			s.publish(ctx, "reconciliation.case.closed", map[string]interface{}{
				"case_id":       c.CaseID.String(),
				"payment_id":    c.PaymentID.String(),
				"payment_state": snapshot.State,
			})
			result.Matched = true
			result.ExternalState = snapshot.State
			result.Resolution = domain.ResolutionTypeAutoMatch
			return result

		case outcomeForExternalState(c.ExternalState) != "":
			// The payment is still UNKNOWN, but the case already holds the
			// provider's answer (recorded from a statement, a provider check or
			// an operator). That answer is the evidence that concludes the
			// payment — closing the case and leaving the payment UNKNOWN would
			// leave the customer's money held for a decision already made.
			outcome := outcomeForExternalState(c.ExternalState)
			reason := fmt.Sprintf("reconciled from external state %s", c.ExternalState)
			if err := s.gateway.ResolveUnknown(ctx, c.PaymentID.String(), outcome, reason); err != nil {
				s.logger.Error().Err(err).Str("payment_id", c.PaymentID.String()).Msg("failed to write reconciliation outcome back to the payment")
				result.Discrepancy = "write-back failed: " + err.Error()
				s.recordAttempt(ctx, c, maxAttempts, result)
				return result
			}
			s.reconRepo.UpdateCaseResolution(ctx, c.CaseID, domain.ResolutionTypeProviderOverride, reason)
			s.publish(ctx, "reconciliation.case.resolved", map[string]interface{}{
				"case_id":    c.CaseID.String(),
				"payment_id": c.PaymentID.String(),
				"outcome":    outcome,
			})
			result.Matched = outcome == string(clients.ResolutionOutcomeConfirmed)
			result.Resolution = domain.ResolutionTypeProviderOverride
			return result
		}
	}

	// 2. Still unknown, and nothing on hand can conclude it. Retry while
	//    attempts remain, then escalate: a case that retries forever is a case
	//    nobody ever notices.
	result.Discrepancy = c.DiscrepancyReason
	s.recordAttempt(ctx, c, maxAttempts, result)
	return result
}

// recordAttempt counts one reconciliation attempt and, once the case has used
// up its attempts, escalates it to a human instead of retrying forever.
func (s *ReconciliationService) recordAttempt(ctx context.Context, c *domain.ReconciliationCase, maxAttempts int, result *domain.ReconcileResult) {
	if err := s.reconRepo.IncrementAttemptCount(ctx, c.CaseID); err != nil {
		s.logger.Error().Err(err).Str("case_id", c.CaseID.String()).Msg("failed to record reconciliation attempt")
	}
	attempts := c.AttemptCount + 1

	if attempts < maxAttempts {
		s.logger.Warn().
			Str("case_id", c.CaseID.String()).
			Str("payment_id", c.PaymentID.String()).
			Int("attempt", attempts).
			Int("max_attempts", maxAttempts).
			Msg("unknown payment - will retry")
		return
	}

	s.logger.Error().
		Str("case_id", c.CaseID.String()).
		Str("payment_id", c.PaymentID.String()).
		Int("attempts", attempts).
		Msg("payment still UNKNOWN after max attempts; escalating")
	s.reconRepo.UpdateCaseStatus(ctx, c.CaseID, domain.ReconciliationStatusEscalated, "max attempts exceeded")
	s.publish(ctx, "reconciliation.case.escalated", map[string]interface{}{
		"case_id":    c.CaseID.String(),
		"payment_id": c.PaymentID.String(),
		"attempts":   attempts,
	})
	if result.Discrepancy == "" {
		result.Discrepancy = fmt.Sprintf("provider outcome still unknown after %d attempts", attempts)
	}
}

// OpenCaseForUnknownPayment files the case that puts a payment which just went
// UNKNOWN in front of the sweep. Called from the payment.unknown consumer, so
// the bucket is worked rather than merely filled.
//
// Opening is idempotent per payment: a redelivered event (at-least-once
// delivery, a consumer restart) must not create a second case for one payment,
// or the sweep would work the same payment twice and count attempts against the
// wrong row.
func (s *ReconciliationService) OpenCaseForUnknownPayment(ctx context.Context, paymentID uuid.UUID, amount int64, currency string) (*domain.ReconciliationCase, error) {
	if paymentID == uuid.Nil {
		return nil, fmt.Errorf("payment id is required")
	}

	if existing, err := s.reconRepo.GetCaseByPaymentID(ctx, paymentID); err == nil && existing != nil {
		s.logger.Info().
			Str("payment_id", paymentID.String()).
			Str("case_id", existing.CaseID.String()).
			Str("status", string(existing.Status)).
			Msg("reconciliation case already open for payment")
		return existing, nil
	}

	if currency == "" {
		currency = "GBP"
	}

	reconCase := domain.NewReconciliationCase(paymentID, unknownPaymentState, unknownPaymentState, amount, amount, currency)
	reconCase.DiscrepancyReason = "provider outcome unknown; reconciliation owns the decision"
	if err := s.reconRepo.CreateCase(ctx, reconCase); err != nil {
		return nil, fmt.Errorf("storing reconciliation case: %w", err)
	}

	s.logger.Info().
		Str("payment_id", paymentID.String()).
		Str("case_id", reconCase.CaseID.String()).
		Int64("amount", amount).
		Msg("opened reconciliation case for UNKNOWN payment")

	s.publish(ctx, "reconciliation.case.opened", map[string]interface{}{
		"case_id":    reconCase.CaseID.String(),
		"payment_id": paymentID.String(),
		"amount":     amount,
		"currency":   currency,
	})

	return reconCase, nil
}

// ResolveCase concludes a case with a definite outcome and, when that outcome
// says what the payment actually did, writes it back to payment-service. This
// is the operator/provider path (a provider statement, a manual review
// decision).
func (s *ReconciliationService) ResolveCase(ctx context.Context, caseID uuid.UUID, externalState string, resolution domain.ResolutionType, actor, reason string) (*domain.ReconciliationCase, error) {
	existing, err := s.reconRepo.GetCaseByID(ctx, caseID)
	if err != nil {
		return nil, err
	}

	if existing.Status == domain.ReconciliationStatusResolved {
		s.logger.Info().Str("case_id", caseID.String()).Msg("case already resolved; nothing to do")
		return existing, nil
	}

	if resolution == "" {
		resolution = domain.ResolutionTypeManualReview
	}
	externalState = strings.ToUpper(strings.TrimSpace(externalState))
	if reason == "" {
		reason = fmt.Sprintf("resolved by %s", actor)
	}
	detail := fmt.Sprintf("external=%s; %s", externalState, reason)

	// Write the outcome back before closing the case. If the write-back fails
	// the case stays open on purpose: a case marked resolved while the payment
	// is still UNKNOWN is a lie that hides the held funds.
	outcome := resolutionOutcomeFor(externalState, resolution)
	if outcome != "" && s.gateway != nil {
		if err := s.gateway.ResolveUnknown(ctx, existing.PaymentID.String(), outcome, reason); err != nil {
			s.logger.Error().Err(err).
				Str("case_id", caseID.String()).
				Str("payment_id", existing.PaymentID.String()).
				Msg("failed to write case outcome back to the payment")
			return nil, fmt.Errorf("resolving payment %s as %s: %w", existing.PaymentID, outcome, err)
		}
	}

	if externalState != "" {
		s.reconRepo.UpdateCaseExternalState(ctx, caseID, externalState, existing.ExternalAmount)
	}
	s.reconRepo.UpdateCaseStatus(ctx, caseID, domain.ReconciliationStatusResolved, reason)
	if err := s.reconRepo.UpdateCaseResolution(ctx, caseID, resolution, detail); err != nil {
		return nil, fmt.Errorf("closing reconciliation case: %w", err)
	}

	s.logger.Info().
		Str("case_id", caseID.String()).
		Str("payment_id", existing.PaymentID.String()).
		Str("resolution", string(resolution)).
		Str("actor", actor).
		Msg("reconciliation case resolved")

	s.publish(ctx, "reconciliation.case.resolved", map[string]interface{}{
		"case_id":    caseID.String(),
		"payment_id": existing.PaymentID.String(),
		"resolution": string(resolution),
		"actor":      actor,
		"outcome":    outcome,
	})

	existing.Status = domain.ReconciliationStatusResolved
	existing.Resolution = resolution
	existing.DiscrepancyReason = detail
	if externalState != "" {
		existing.ExternalState = externalState
	}
	return existing, nil
}

// RunScheduler runs the reconciliation sweep on an interval until ctx is
// cancelled. Without it the sweep is only as reliable as whoever remembers to
// call the endpoint: an UNKNOWN payment would sit unresolved and the customer's
// hold would stay in place until it expired on its own.
func (s *ReconciliationService) RunScheduler(ctx context.Context, interval time.Duration, batchSize int) {
	if interval <= 0 {
		s.logger.Warn().Msg("reconciliation scheduler disabled (interval <= 0); rely on POST /v1/reconciliation/run-scheduled")
		return
	}

	s.logger.Info().
		Dur("interval", interval).
		Int("batch_size", batchSize).
		Msg("reconciliation scheduler started")

	// Sweep once at boot: a service that restarted mid-incident should not wait
	// a whole interval before it starts working the backlog.
	s.sweep(ctx, batchSize)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("reconciliation scheduler stopped")
			return
		case <-ticker.C:
			s.sweep(ctx, batchSize)
		}
	}
}

func (s *ReconciliationService) sweep(ctx context.Context, batchSize int) {
	results, err := s.RunScheduledReconciliation(ctx, batchSize)
	if err != nil {
		s.logger.Error().Err(err).Msg("scheduled reconciliation failed")
		return
	}

	var matched, unresolved int
	for _, r := range results {
		if r.Matched {
			matched++
		} else if r.Discrepancy != "" {
			unresolved++
		}
	}
	s.logger.Info().
		Int("cases", len(results)).
		Int("matched", matched).
		Int("unresolved", unresolved).
		Msg("reconciliation sweep complete")
}

// resolutionOutcomeFor maps a reconciliation decision onto the payment state it
// implies. An empty result means "no payment state follows from this decision":
// MANUAL_REVIEW, WRITE_OFF and REVERSAL are accounting decisions about the
// books, and matching two UNKNOWNs is not evidence about what the rail did.
func resolutionOutcomeFor(externalState string, resolution domain.ResolutionType) string {
	switch resolution {
	case domain.ResolutionTypeAutoMatch, domain.ResolutionTypeProviderOverride, domain.ResolutionTypeInternalOverride:
	default:
		return ""
	}
	return outcomeForExternalState(externalState)
}

// outcomeForExternalState maps a provider-side state onto the outcome the
// payment saga accepts. Only a definite outcome qualifies: anything else
// (including UNKNOWN, PENDING or an empty state) is not evidence about money.
func outcomeForExternalState(externalState string) string {
	switch strings.ToUpper(strings.TrimSpace(externalState)) {
	case "CONFIRMED", "SETTLED":
		return string(clients.ResolutionOutcomeConfirmed)
	case "FAILED", "DECLINED", "REJECTED":
		return string(clients.ResolutionOutcomeFailed)
	default:
		return ""
	}
}

func (s *ReconciliationService) RecordDiscrepancy(ctx context.Context, txID uuid.UUID, internalState, externalState string, resolution domain.ResolutionType) error {
	s.logger.Info().Str("tx_id", txID.String()).Msg("recording discrepancy")

	existingCase, err := s.reconRepo.GetCaseByPaymentID(ctx, txID)
	if err != nil {
		reconCase := domain.NewReconciliationCase(txID, internalState, externalState, 0, 0, "GBP")
		reconCase.DiscrepancyReason = fmt.Sprintf("manual discrepancy: %s vs %s", internalState, externalState)
		reconCase.Resolution = resolution
		return s.reconRepo.CreateCase(ctx, reconCase)
	}

	existingCase.InternalState = internalState
	existingCase.ExternalState = externalState
	existingCase.Resolution = resolution
	existingCase.UpdatedAt = time.Now().UTC()
	return s.reconRepo.UpdateCaseResolution(ctx, existingCase.CaseID, resolution, fmt.Sprintf("manual: %s vs %s", internalState, externalState))
}

func (s *ReconciliationService) CreateRecord(ctx context.Context, req *domain.CreateReconciliationRequest) (*domain.ReconciliationRecord, error) {
	s.logger.Info().Str("transaction_id", req.TransactionID).Msg("creating reconciliation record")
	txID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		return nil, fmt.Errorf("invalid transaction ID: %w", err)
	}
	record := domain.NewReconciliationRecord(txID, req.Source, req.Target, req.Amount, req.Currency)
	if err := s.reconRepo.CreateRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("storing record: %w", err)
	}
	return record, nil
}

func (s *ReconciliationService) MatchRecord(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("record_id", id.String()).Msg("matching record")
	return s.reconRepo.UpdateRecordStatus(ctx, id, domain.ReconciliationStatusMatched, "")
}

func (s *ReconciliationService) FlagDiscrepancy(ctx context.Context, id uuid.UUID, reason string) error {
	s.logger.Info().Str("record_id", id.String()).Msg("flagging discrepancy")
	return s.reconRepo.UpdateRecordStatus(ctx, id, domain.ReconciliationStatusDiscrepancy, reason)
}

func (s *ReconciliationService) ResolveRecord(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("record_id", id.String()).Msg("resolving record")
	return s.reconRepo.UpdateRecordStatus(ctx, id, domain.ReconciliationStatusResolved, "")
}

func (s *ReconciliationService) GetCase(ctx context.Context, id uuid.UUID) (*domain.ReconciliationCase, error) {
	return s.reconRepo.GetCaseByID(ctx, id)
}

func (s *ReconciliationService) GetPendingCases(ctx context.Context) ([]*domain.ReconciliationCase, error) {
	return s.reconRepo.GetCasesByStatus(ctx, domain.ReconciliationStatusPending, 100)
}

func (s *ReconciliationService) GetDiscrepancyCases(ctx context.Context) ([]*domain.ReconciliationCase, error) {
	return s.reconRepo.GetCasesByStatus(ctx, domain.ReconciliationStatusDiscrepancy, 100)
}

func (s *ReconciliationService) GetRecord(ctx context.Context, id uuid.UUID) (*domain.ReconciliationRecord, error) {
	return s.reconRepo.GetRecordByID(ctx, id)
}

func (s *ReconciliationService) GetPendingRecords(ctx context.Context) ([]*domain.ReconciliationRecord, error) {
	return s.reconRepo.GetRecordsByStatus(ctx, domain.ReconciliationStatusPending)
}
