package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/transfer-service/internal/clients"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
	"github.com/nexora/nexora/services/transfer-service/internal/events"
	"github.com/nexora/nexora/services/transfer-service/internal/repository"
	"github.com/rs/zerolog"
)

var (
	// ErrInsufficientFunds surfaces the ledger's availability decision.
	ErrInsufficientFunds = clients.ErrInsufficientFunds
	// ErrAccountNotOwned guards cross-account transfers.
	ErrAccountNotOwned = errors.New("account does not belong to caller")
	// ErrAccountLocked marks transfers refused by emergency lockdown.
	ErrAccountLocked = errors.New("account is locked: outbound transfers are temporarily disabled")
)

type TransferService struct {
	transferRepo  repository.TransferRepository
	ledgerClient  *clients.LedgerClient
	accountClient *clients.AccountClient
	producer      *events.KafkaProducer
	logger        zerolog.Logger
}

func NewTransferService(transferRepo repository.TransferRepository, ledgerClient *clients.LedgerClient, accountClient *clients.AccountClient, producer *events.KafkaProducer, logger zerolog.Logger) *TransferService {
	return &TransferService{
		transferRepo:  transferRepo,
		ledgerClient:  ledgerClient,
		accountClient: accountClient,
		producer:      producer,
		logger:        logger,
	}
}

// CreateTransfer moves money between two of the caller's own accounts by
// booking a balanced double-entry transaction in the ledger (source DEBIT,
// destination CREDIT) with an atomic availability check. Retries with the same
// idempotency key return the original transfer instead of double-spending.
func (s *TransferService) CreateTransfer(ctx context.Context, userID uuid.UUID, req *domain.CreateTransferRequest) (*domain.Transfer, error) {
	s.logger.Info().Str("idempotency_key", req.IdempotencyKey).Msg("creating transfer")

	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid from account ID: %w", err)
	}
	toID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid to account ID: %w", err)
	}
	if fromID == toID {
		return nil, errors.New("from and to accounts must be different")
	}
	if req.Amount <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if req.Currency == "" {
		req.Currency = "GBP"
	}
	if req.IdempotencyKey == "" {
		return nil, errors.New("idempotency_key is required")
	}

	// Idempotent resume: a previously completed transfer is returned as-is.
	existing, err := s.transferRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		if existing.Status == domain.TransferStatusCompleted {
			return existing, nil
		}
		if existing.Status == domain.TransferStatusFailed {
			return nil, errors.New("transfer previously failed")
		}
	}

	// Ownership: both accounts must belong to the authenticated caller.
	if err := s.requireOwnedAccount(ctx, userID, fromID); err != nil {
		return nil, err
	}
	if err := s.requireOwnedAccount(ctx, userID, toID); err != nil {
		return nil, err
	}

	// Emergency lockdown: the SOURCE account refusing to move money out
	// blocks the transfer. The destination being locked does not — money in
	// is always allowed.
	if err := s.requireUnlocked(ctx, fromID); err != nil {
		return nil, err
	}

	var transfer *domain.Transfer
	if existing != nil {
		// PENDING row from a previous attempt: resume booking it.
		transfer = existing
	} else {
		transfer = domain.NewTransfer(req.IdempotencyKey, fromID, toID, req.Amount, req.Currency, req.Description)
		// Persist intent as PENDING before touching money, so a crash leaves a
		// recoverable record and a retry resumes the ledger booking.
		if err := s.transferRepo.Create(ctx, transfer); err != nil {
			return nil, fmt.Errorf("storing transfer: %w", err)
		}
	}

	ledgerKey := "transfer:" + req.IdempotencyKey
	_, err = s.ledgerClient.BookTransfer(ctx, fromID, toID, req.Amount, req.Currency, ledgerKey, "Internal transfer: "+req.Description)
	if err != nil {
		_ = s.transferRepo.Update(ctx, &domain.Transfer{
			TransferID: transfer.TransferID,
			Status:     domain.TransferStatusFailed,
		})
		if errors.Is(err, clients.ErrInsufficientFunds) {
			return nil, ErrInsufficientFunds
		}
		return nil, fmt.Errorf("booking transfer: %w", err)
	}

	now := time.Now().UTC()
	transfer.Status = domain.TransferStatusCompleted
	transfer.CompletedAt = &now
	if err := s.transferRepo.Update(ctx, transfer); err != nil {
		return nil, fmt.Errorf("marking transfer completed: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "transfer.completed", transfer)
	}

	s.logger.Info().
		Str("transfer_id", transfer.TransferID.String()).
		Int64("amount", req.Amount).
		Msg("transfer completed")
	return transfer, nil
}

// requireOwnedAccount verifies the caller owns an account via account-service.
func (s *TransferService) requireOwnedAccount(ctx context.Context, userID, accountID uuid.UUID) error {
	info, err := s.accountClient.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if info.UserID != userID {
		return ErrAccountNotOwned
	}
	return nil
}

// requireUnlocked refuses transfers out of accounts in emergency lockdown.
// Lookup failure never blocks a transfer (fail-open): the ledger availability
// check still guards against over-spend.
func (s *TransferService) requireUnlocked(ctx context.Context, accountID uuid.UUID) error {
	info, err := s.accountClient.GetAccount(ctx, accountID)
	if err != nil {
		s.logger.Warn().Err(err).Str("account_id", accountID.String()).Msg("lockdown lookup unavailable, allowing transfer")
		return nil
	}
	if info.LockdownEnabled {
		return ErrAccountLocked
	}
	return nil
}

// GetTransferForUser returns a transfer only when the caller owns its source
// account.
func (s *TransferService) GetTransferForUser(ctx context.Context, userID, id uuid.UUID) (*domain.Transfer, error) {
	transfer, err := s.transferRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireOwnedAccount(ctx, userID, transfer.FromAccountID); err != nil {
		return nil, err
	}
	return transfer, nil
}

func (s *TransferService) GetTransfersByAccount(ctx context.Context, userID, accountID uuid.UUID) ([]*domain.Transfer, error) {
	if err := s.requireOwnedAccount(ctx, userID, accountID); err != nil {
		return nil, err
	}
	return s.transferRepo.GetByFromAccount(ctx, accountID)
}
