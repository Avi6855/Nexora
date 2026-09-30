package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/transfer-service/internal/clients"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
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

// EventSink publishes transfer lifecycle events. It is Kafka when a broker is
// configured and nil when it is not, but the saga and the sweep only ever need
// to publish — so the interface keeps both testable without a broker.
type EventSink interface {
	Publish(ctx context.Context, eventType string, payload interface{}) error
}

const (
	// defaultSweepBatchSize bounds one pass, so the sweep cannot turn into an
	// unbounded query against the ledger on a bad day.
	defaultSweepBatchSize = 50
	// defaultEscalationWindow is how long a transfer may stay indeterminate
	// before a human is asked to look at it. Retrying is cheap and safe while
	// the ledger might still answer; it stops being either once the money has
	// been in limbo long enough for a customer to notice.
	defaultEscalationWindow = 15 * time.Minute
)

// SweepSummary reports what one pass over the indeterminate transfers did.
type SweepSummary struct {
	Examined     int
	Completed    int
	Failed       int
	StillUnknown int
	Escalated    int
}

type transferSweepOutcome int

const (
	sweepStillUnknown transferSweepOutcome = iota
	sweepCompleted
	sweepFailed
	sweepEscalated
)

type TransferService struct {
	transferRepo  repository.TransferRepository
	ledgerClient  *clients.LedgerClient
	accountClient *clients.AccountClient
	producer      EventSink
	logger        zerolog.Logger
	// escalationWindow overrides how long a transfer may stay UNKNOWN before
	// the sweep stops retrying and asks for a human.
	escalationWindow time.Duration
}

func NewTransferService(transferRepo repository.TransferRepository, ledgerClient *clients.LedgerClient, accountClient *clients.AccountClient, producer EventSink, logger zerolog.Logger) *TransferService {
	return &TransferService{
		transferRepo:     transferRepo,
		ledgerClient:     ledgerClient,
		accountClient:    accountClient,
		producer:         producer,
		logger:           logger,
		escalationWindow: defaultEscalationWindow,
	}
}

// SetEscalationWindow overrides the escalation window (from configuration).
func (s *TransferService) SetEscalationWindow(d time.Duration) {
	if d > 0 {
		s.escalationWindow = d
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
			// The retry of a transfer that already went through: return the same
			// transfer rather than booking a second one.
			return existing, nil
		}
		if existing.Status == domain.TransferStatusFailed {
			return nil, errors.New("transfer previously failed")
		}
		// PENDING or UNKNOWN: fall through and attempt the booking again.
		// UNKNOWN matters most: the first attempt timed out, the outcome may
		// have been a success, and the ledger's own idempotency key is what
		// makes this safe to retry rather than re-spend.
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
		// A booking that timed out, or that failed while the ledger was
		// unreachable, is not a booking that was refused. The ledger may have
		// moved the money and only the answer was lost, so the transfer is left
		// UNKNOWN for reconciliation instead of being closed as failed — and it
		// keeps the ledger key, which is what makes a later resume safe.
		if clients.IsIndeterminate(err) {
			return s.markUnknown(ctx, transfer, err)
		}

		if transitionErr := transfer.TransitionTo(domain.TransferStatusFailed); transitionErr != nil {
			return nil, fmt.Errorf("marking transfer failed: %w", transitionErr)
		}
		if updateErr := s.transferRepo.Update(ctx, transfer); updateErr != nil {
			return nil, fmt.Errorf("storing failed transfer: %w", updateErr)
		}
		if s.producer != nil {
			_ = s.producer.Publish(ctx, "transfer.failed", transfer)
		}
		if errors.Is(err, clients.ErrInsufficientFunds) {
			return nil, ErrInsufficientFunds
		}
		return nil, fmt.Errorf("booking transfer: %w", err)
	}

	if err := transfer.TransitionTo(domain.TransferStatusCompleted); err != nil {
		return nil, fmt.Errorf("marking transfer completed: %w", err)
	}
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

// markUnknown records that the booking outcome is unknown and hands the
// transfer to reconciliation. It reports success rather than an error because
// nothing has gone wrong yet that we can name: the answer is "we do not know",
// and telling the caller the transfer failed would be a guess about money.
func (s *TransferService) markUnknown(ctx context.Context, transfer *domain.Transfer, cause error) (*domain.Transfer, error) {
	if err := transfer.TransitionTo(domain.TransferStatusUnknown); err != nil {
		return nil, fmt.Errorf("marking transfer unknown: %w", err)
	}
	if err := s.transferRepo.Update(ctx, transfer); err != nil {
		return nil, fmt.Errorf("storing indeterminate transfer: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "transfer.unknown", transfer)
	}

	s.logger.Warn().
		Err(cause).
		Str("transfer_id", transfer.TransferID.String()).
		Str("idempotency_key", transfer.IdempotencyKey).
		Msg("transfer outcome unknown; reconciliation owns the decision")

	return transfer, nil
}

// ResolveUnknown concludes a transfer whose booking outcome was indeterminate,
// once somebody has established what the ledger actually did. It is idempotent:
// a transfer that already reached a definite state is returned untouched, so a
// retried sweep cannot complete the same transfer twice.
func (s *TransferService) ResolveUnknown(ctx context.Context, id uuid.UUID, outcome domain.ResolutionOutcome, reason string) (*domain.Transfer, error) {
	if !outcome.Valid() {
		return nil, fmt.Errorf("unsupported resolution outcome %q, want CONFIRMED or FAILED", outcome)
	}

	transfer, err := s.transferRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if transfer.Status != domain.TransferStatusUnknown {
		s.logger.Info().
			Str("transfer_id", id.String()).
			Str("status", string(transfer.Status)).
			Msg("transfer is not UNKNOWN; reconciliation resolution is a no-op")
		return transfer, nil
	}

	target := domain.TransferStatusFailed
	if outcome == domain.ResolutionOutcomeConfirmed {
		target = domain.TransferStatusCompleted
	}
	if err := transfer.TransitionTo(target); err != nil {
		return nil, fmt.Errorf("resolving transfer: %w", err)
	}
	if err := s.transferRepo.Update(ctx, transfer); err != nil {
		return nil, fmt.Errorf("storing resolved transfer: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "transfer.resolved", transfer)
	}

	if reason == "" {
		reason = "resolved by reconciliation"
	}
	s.logger.Info().
		Str("transfer_id", id.String()).
		Str("outcome", string(outcome)).
		Str("reason", reason).
		Msg("indeterminate transfer resolved by reconciliation")

	return transfer, nil
}

// GetTransfer reads a transfer without an ownership check, for internal callers
// (reconciliation needs to know whether this transfer is still unresolved).
func (s *TransferService) GetTransfer(ctx context.Context, id uuid.UUID) (*domain.Transfer, error) {
	return s.transferRepo.GetByID(ctx, id)
}

// RunUnknownSweep works the bucket of transfers whose booking outcome nobody
// knows. For each one it retries the booking with the transfer's own ledger key:
// the ledger is idempotent on that key, so a retry either settles the booking
// that already happened or books the one that never did — and cannot move the
// money twice. Whatever the ledger answers, the transfer stops being UNKNOWN.
//
// This is the account-to-account half of ADR-007: an internal move whose booking
// timed out usually succeeds on the retry, and a retry is the only recovery that
// needs no new information.
func (s *TransferService) RunUnknownSweep(ctx context.Context, batchSize int) (SweepSummary, error) {
	if batchSize <= 0 {
		batchSize = defaultSweepBatchSize
	}

	unknown, err := s.transferRepo.GetByStatus(ctx, domain.TransferStatusUnknown, batchSize)
	if err != nil {
		return SweepSummary{}, fmt.Errorf("fetching indeterminate transfers: %w", err)
	}

	var summary SweepSummary
	for _, transfer := range unknown {
		if ctx.Err() != nil {
			break
		}
		summary.Examined++
		switch s.sweepUnknown(ctx, transfer) {
		case sweepCompleted:
			summary.Completed++
		case sweepFailed:
			summary.Failed++
		case sweepEscalated:
			summary.Escalated++
		default:
			summary.StillUnknown++
		}
	}

	s.logger.Info().
		Int("examined", summary.Examined).
		Int("completed", summary.Completed).
		Int("failed", summary.Failed).
		Int("still_unknown", summary.StillUnknown).
		Int("escalated", summary.Escalated).
		Msg("indeterminate transfer sweep complete")

	return summary, nil
}

func (s *TransferService) sweepUnknown(ctx context.Context, transfer *domain.Transfer) transferSweepOutcome {
	if s.escalationWindow > 0 && time.Since(transfer.CreatedAt) >= s.escalationWindow {
		s.logger.Error().
			Str("transfer_id", transfer.TransferID.String()).
			Dur("age", time.Since(transfer.CreatedAt)).
			Dur("escalation_window", s.escalationWindow).
			Msg("transfer still indeterminate past the escalation window; needs a manual decision")
		if s.producer != nil {
			_ = s.producer.Publish(ctx, "transfer.reconciliation.required", transfer)
		}
		return sweepEscalated
	}

	_, err := s.ledgerClient.BookTransfer(ctx, transfer.FromAccountID, transfer.ToAccountID, transfer.Amount, transfer.Currency,
		"transfer:"+transfer.IdempotencyKey, "Internal transfer: "+transfer.Description)
	switch {
	case err == nil:
		if err := transfer.TransitionTo(domain.TransferStatusCompleted); err != nil {
			s.logger.Error().Err(err).Str("transfer_id", transfer.TransferID.String()).Msg("sweep could not complete the transfer")
			return sweepStillUnknown
		}
		if err := s.transferRepo.Update(ctx, transfer); err != nil {
			s.logger.Error().Err(err).Str("transfer_id", transfer.TransferID.String()).Msg("sweep could not persist the completed transfer")
			return sweepStillUnknown
		}
		if s.producer != nil {
			_ = s.producer.Publish(ctx, "transfer.completed", transfer)
		}
		s.logger.Info().
			Str("transfer_id", transfer.TransferID.String()).
			Msg("indeterminate transfer settled by retry")
		return sweepCompleted

	case clients.IsIndeterminate(err):
		s.logger.Warn().
			Err(err).
			Str("transfer_id", transfer.TransferID.String()).
			Msg("transfer still indeterminate; will retry next sweep")
		return sweepStillUnknown

	default:
		if transitionErr := transfer.TransitionTo(domain.TransferStatusFailed); transitionErr != nil {
			s.logger.Error().Err(transitionErr).Str("transfer_id", transfer.TransferID.String()).Msg("sweep could not fail the transfer")
			return sweepStillUnknown
		}
		if updateErr := s.transferRepo.Update(ctx, transfer); updateErr != nil {
			s.logger.Error().Err(updateErr).Str("transfer_id", transfer.TransferID.String()).Msg("sweep could not persist the failed transfer")
			return sweepStillUnknown
		}
		if s.producer != nil {
			_ = s.producer.Publish(ctx, "transfer.failed", transfer)
		}
		s.logger.Info().
			Str("transfer_id", transfer.TransferID.String()).
			Str("reason", err.Error()).
			Msg("indeterminate transfer resolved as failed by the ledger")
		return sweepFailed
	}
}

// RunUnknownScheduler runs the sweep on an interval until ctx is cancelled. A
// sweep nobody calls is not recovery: the transfers would sit UNKNOWN, and the
// customer's money with them, until somebody happened to look.
func (s *TransferService) RunUnknownScheduler(ctx context.Context, interval time.Duration, batchSize int) {
	if interval <= 0 {
		s.logger.Warn().Msg("indeterminate transfer sweep disabled (interval <= 0); rely on the manual sweep")
		return
	}

	s.logger.Info().
		Dur("interval", interval).
		Dur("escalation_window", s.escalationWindow).
		Int("batch_size", batchSize).
		Msg("indeterminate transfer sweep started")

	if _, err := s.RunUnknownSweep(ctx, batchSize); err != nil {
		s.logger.Error().Err(err).Msg("initial transfer sweep failed")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("indeterminate transfer sweep stopped")
			return
		case <-ticker.C:
			if _, err := s.RunUnknownSweep(ctx, batchSize); err != nil {
				s.logger.Error().Err(err).Msg("transfer sweep failed")
			}
		}
	}
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
