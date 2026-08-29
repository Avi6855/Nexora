package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/reconciliation-service/internal/domain"
	"github.com/nexora/nexora/services/reconciliation-service/internal/events"
	"github.com/nexora/nexora/services/reconciliation-service/internal/repository"
)

type ReconciliationService struct {
	reconRepo repository.ReconciliationRepository
	producer  *events.KafkaProducer
	logger    zerolog.Logger
}

func NewReconciliationService(reconRepo repository.ReconciliationRepository, producer *events.KafkaProducer, logger zerolog.Logger) *ReconciliationService {
	return &ReconciliationService{reconRepo: reconRepo, producer: producer, logger: logger}
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

	var results []*domain.ReconcileResult
	for _, c := range cases {
		result := &domain.ReconcileResult{
			CaseID:        c.CaseID,
			PaymentID:     c.PaymentID,
			Matched:       c.Status == domain.ReconciliationStatusMatched,
			InternalState: c.InternalState,
			ExternalState: c.ExternalState,
			ReconciledAt:  time.Now().UTC(),
		}
		if c.InternalState == c.ExternalState && c.InternalAmount == c.ExternalAmount {
			result.Matched = true
			result.Resolution = domain.ResolutionTypeAutoMatch
			s.reconRepo.UpdateCaseStatus(ctx, c.CaseID, domain.ReconciliationStatusMatched, "scheduled auto-match")
			s.reconRepo.UpdateCaseResolution(ctx, c.CaseID, domain.ResolutionTypeAutoMatch, "scheduled batch reconciliation")
		} else {
			result.Discrepancy = c.DiscrepancyReason
			s.reconRepo.IncrementAttemptCount(ctx, c.CaseID)
		}
		results = append(results, result)
	}

	s.logger.Info().Int("processed", len(results)).Msg("scheduled reconciliation complete")
	return results, nil
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
