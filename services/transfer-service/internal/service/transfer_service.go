package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
	"github.com/nexora/nexora/services/transfer-service/internal/events"
	"github.com/nexora/nexora/services/transfer-service/internal/repository"
)

type TransferService struct {
	transferRepo repository.TransferRepository
	producer     *events.KafkaProducer
	logger       zerolog.Logger
}

func NewTransferService(transferRepo repository.TransferRepository, producer *events.KafkaProducer, logger zerolog.Logger) *TransferService {
	return &TransferService{
		transferRepo: transferRepo,
		producer:     producer,
		logger:       logger,
	}
}

func (s *TransferService) CreateTransfer(ctx context.Context, req *domain.CreateTransferRequest) (*domain.Transfer, error) {
	s.logger.Info().Str("idempotency_key", req.IdempotencyKey).Msg("creating transfer")

	existing, err := s.transferRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err == nil && existing != nil {
		return existing, nil
	}

	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid from account ID: %w", err)
	}

	toID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid to account ID: %w", err)
	}

	transfer := domain.NewTransfer(req.IdempotencyKey, fromID, toID, req.Amount, req.Currency, req.Description)

	if err := s.transferRepo.Create(ctx, transfer); err != nil {
		return nil, fmt.Errorf("storing transfer: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "transfer.created", transfer)
	}

	return transfer, nil
}

func (s *TransferService) CompleteTransfer(ctx context.Context, id uuid.UUID) error {
	transfer, err := s.transferRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	transfer.Status = domain.TransferStatusCompleted
	now := time.Now().UTC()
	transfer.CompletedAt = &now

	if err := s.transferRepo.Update(ctx, transfer); err != nil {
		return err
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "transfer.completed", transfer)
	}

	return nil
}

func (s *TransferService) FailTransfer(ctx context.Context, id uuid.UUID) error {
	transfer, err := s.transferRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	transfer.Status = domain.TransferStatusFailed

	if err := s.transferRepo.Update(ctx, transfer); err != nil {
		return err
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "transfer.failed", transfer)
	}

	return nil
}

func (s *TransferService) GetTransfer(ctx context.Context, id uuid.UUID) (*domain.Transfer, error) {
	return s.transferRepo.GetByID(ctx, id)
}

func (s *TransferService) GetTransfersByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Transfer, error) {
	return s.transferRepo.GetByFromAccount(ctx, accountID)
}
