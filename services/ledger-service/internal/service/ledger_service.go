package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/events"
	"github.com/nexora/nexora/services/ledger-service/internal/repository"
)

type LedgerService struct {
	ledgerRepo repository.LedgerRepository
	producer   *events.KafkaProducer
	logger     zerolog.Logger
}

func NewLedgerService(ledgerRepo repository.LedgerRepository, producer *events.KafkaProducer, logger zerolog.Logger) *LedgerService {
	return &LedgerService{
		ledgerRepo: ledgerRepo,
		producer:   producer,
		logger:     logger,
	}
}

func (s *LedgerService) CreateDoubleEntryTransaction(ctx context.Context, req *domain.CreateDoubleEntryRequest) (*domain.LedgerTransaction, []*domain.LedgerEntry, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}

	existing, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		entries, _ := s.ledgerRepo.GetEntriesByTransaction(ctx, existing.TransactionID)
		return existing, entries, nil
	}

	txID := uuid.New()
	now := time.Now().UTC()

	tx := &domain.LedgerTransaction{
		TransactionID:  txID,
		IdempotencyKey: req.IdempotencyKey,
		TransactionType: req.TransactionType,
		Status:         domain.TransactionStatusPending,
		TotalAmount:    req.Amount,
		Currency:       req.Currency,
		Description:    req.Description,
		CorrelationID:  req.CorrelationID,
		CausationID:    req.CausationID,
		EventVersion:   1,
		CreatedAt:      now,
	}

	if err := s.ledgerRepo.CreateTransaction(ctx, tx); err != nil {
		return nil, nil, fmt.Errorf("creating transaction: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "ledger.transaction.created", tx)
	}

	s.logger.Info().
		Str("transaction_id", txID.String()).
		Str("idempotency_key", req.IdempotencyKey).
		Msg("double-entry transaction created")

	var entries []*domain.LedgerEntry

	for _, line := range req.Lines {
		balance, err := s.ledgerRepo.GetLatestBalance(ctx, line.AccountID)
		if err != nil {
			s.logger.Warn().Err(err).Str("account_id", line.AccountID.String()).Msg("could not get balance, defaulting to 0")
			balance = 0
		}

		var balanceAfter int64
		if line.EntryType == domain.EntryTypeDebit {
			balanceAfter = balance - line.Amount
		} else {
			balanceAfter = balance + line.Amount
		}

		direction := domain.EntryDirectionOutbound
		if line.EntryType == domain.EntryTypeCredit {
			direction = domain.EntryDirectionInbound
		}

		entry := &domain.LedgerEntry{
			EntryID:        uuid.New(),
			AccountID:      line.AccountID,
			TransactionID:  txID,
			EntryType:      line.EntryType,
			EntryDirection: direction,
			Amount:         line.Amount,
			Currency:       line.Currency,
			BalanceBefore:  balance,
			BalanceAfter:   balanceAfter,
			Description:    req.Description,
			CorrelationID:  req.CorrelationID,
			CausationID:    req.CausationID,
			EventVersion:   1,
			CreatedAt:      now,
		}

		if err := s.ledgerRepo.CreateEntry(ctx, entry); err != nil {
			return nil, nil, fmt.Errorf("creating entry for account %s: %w", line.AccountID, err)
		}

		entries = append(entries, entry)
	}

	completedAt := now
	tx.Status = domain.TransactionStatusCompleted
	tx.CompletedAt = &completedAt
	if err := s.ledgerRepo.UpdateTransactionStatus(ctx, txID, domain.TransactionStatusCompleted); err != nil {
		s.logger.Error().Err(err).Str("transaction_id", txID.String()).Msg("failed to mark transaction completed")
	}

	s.logger.Info().
		Str("transaction_id", txID.String()).
		Int("entry_count", len(entries)).
		Msg("double-entry transaction completed")

	return tx, entries, nil
}

func (s *LedgerService) VerifyBalanceIntegrity(ctx context.Context, accountID uuid.UUID) (*domain.BalanceIntegrityResult, error) {
	result := &domain.BalanceIntegrityResult{
		AccountID: accountID,
	}

	totalDebits, err := s.ledgerRepo.SumDebitsByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("summing debits: %w", err)
	}
	result.TotalDebits = totalDebits

	totalCredits, err := s.ledgerRepo.SumCreditsByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("summing credits: %w", err)
	}
	result.TotalCredits = totalCredits

	result.ComputedBalance = totalCredits - totalDebits

	latestBalance, err := s.ledgerRepo.GetLatestBalance(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("getting latest balance: %w", err)
	}
	result.LatestBalance = latestBalance

	result.IsBalanced = result.ComputedBalance == result.LatestBalance

	s.logger.Info().
		Str("account_id", accountID.String()).
		Int64("computed", result.ComputedBalance).
		Int64("latest", result.LatestBalance).
		Bool("balanced", result.IsBalanced).
		Msg("balance integrity check")

	return result, nil
}

func (s *LedgerService) GetAccountBalance(ctx context.Context, accountID uuid.UUID) (*domain.BalanceBreakdown, error) {
	latestBalance, err := s.ledgerRepo.GetLatestBalance(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("getting latest balance: %w", err)
	}

	pending, err := s.ledgerRepo.SumActiveReservationAmounts(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("summing pending reservations: %w", err)
	}

	totalDebits, err := s.ledgerRepo.SumDebitsByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("summing debits: %w", err)
	}

	totalCredits, err := s.ledgerRepo.SumCreditsByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("summing credits: %w", err)
	}

	return &domain.BalanceBreakdown{
		AccountID:    accountID,
		Balance:      latestBalance,
		Pending:      pending,
		Available:    latestBalance - pending,
		TotalDebits:  totalDebits,
		TotalCredits: totalCredits,
	}, nil
}

func (s *LedgerService) GetPendingBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	pending, err := s.ledgerRepo.SumActiveReservationAmounts(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("summing pending reservations: %w", err)
	}
	return pending, nil
}

func (s *LedgerService) GetAvailableBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	balance, err := s.ledgerRepo.GetLatestBalance(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("getting latest balance: %w", err)
	}

	pending, err := s.ledgerRepo.SumActiveReservationAmounts(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("summing pending reservations: %w", err)
	}

	return balance - pending, nil
}

func (s *LedgerService) ReserveFunds(ctx context.Context, accountID uuid.UUID, amount int64, txID uuid.UUID, currency string, ttl time.Duration) (*domain.Reservation, error) {
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	s.logger.Info().
		Str("account_id", accountID.String()).
		Int64("amount", amount).
		Msg("reserving funds")

	available, err := s.GetAvailableBalance(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("checking available balance: %w", err)
	}

	if available < amount {
		return nil, domain.ErrInsufficientFunds
	}

	now := time.Now().UTC()
	reservation := &domain.Reservation{
		ReservationID: uuid.New(),
		AccountID:     accountID,
		TransactionID: txID,
		Amount:        amount,
		Currency:      currency,
		Status:        domain.ReservationStatusActive,
		ExpiresAt:     now.Add(ttl),
		CreatedAt:     now,
	}

	applied, err := s.ledgerRepo.CreateReservation(ctx, reservation)
	if err != nil {
		return nil, fmt.Errorf("creating reservation: %w", err)
	}
	if !applied {
		return nil, domain.ErrDuplicateEntry
	}

	s.logger.Info().
		Str("reservation_id", reservation.ReservationID.String()).
		Str("account_id", accountID.String()).
		Int64("amount", amount).
		Msg("funds reserved")

	return reservation, nil
}

func (s *LedgerService) ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error {
	s.logger.Info().Str("reservation_id", reservationID.String()).Msg("releasing reservation")

	res, err := s.ledgerRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return err
	}

	if res.Status != domain.ReservationStatusActive {
		return domain.ErrReservationNotActive
	}

	if time.Now().UTC().After(res.ExpiresAt) {
		_ = s.ledgerRepo.UpdateReservationStatus(ctx, reservationID, domain.ReservationStatusExpired)
		return domain.ErrReservationExpired
	}

	return s.ledgerRepo.UpdateReservationStatus(ctx, reservationID, domain.ReservationStatusReleased)
}

func (s *LedgerService) SettleReservation(ctx context.Context, reservationID uuid.UUID) (*domain.LedgerEntry, error) {
	s.logger.Info().Str("reservation_id", reservationID.String()).Msg("settling reservation")

	res, err := s.ledgerRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return nil, err
	}

	if res.Status != domain.ReservationStatusActive {
		return nil, domain.ErrReservationNotActive
	}

	if time.Now().UTC().After(res.ExpiresAt) {
		_ = s.ledgerRepo.UpdateReservationStatus(ctx, reservationID, domain.ReservationStatusExpired)
		return nil, domain.ErrReservationExpired
	}

	if err := s.ledgerRepo.UpdateReservationStatus(ctx, reservationID, domain.ReservationStatusSettled); err != nil {
		return nil, fmt.Errorf("settling reservation: %w", err)
	}

	now := time.Now().UTC()
	balance, err := s.ledgerRepo.GetLatestBalance(ctx, res.AccountID)
	if err != nil {
		balance = 0
	}

	entry := &domain.LedgerEntry{
		EntryID:        uuid.New(),
		AccountID:      res.AccountID,
		TransactionID:  res.TransactionID,
		EntryType:      domain.EntryTypeDebit,
		EntryDirection: domain.EntryDirectionOutbound,
		Amount:         res.Amount,
		Currency:       res.Currency,
		BalanceBefore:  balance,
		BalanceAfter:   balance - res.Amount,
		Description:    fmt.Sprintf("settlement of reservation %s", reservationID.String()),
		EventVersion:   1,
		CreatedAt:      now,
	}

	if err := s.ledgerRepo.CreateEntry(ctx, entry); err != nil {
		return nil, fmt.Errorf("creating settlement entry: %w", err)
	}

	s.logger.Info().
		Str("reservation_id", reservationID.String()).
		Str("entry_id", entry.EntryID.String()).
		Msg("reservation settled")

	return entry, nil
}

func (s *LedgerService) GetEntries(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.LedgerEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.ledgerRepo.GetEntriesByAccount(ctx, accountID, limit)
}

func (s *LedgerService) GetTransaction(ctx context.Context, txID uuid.UUID) (*domain.LedgerTransaction, error) {
	return s.ledgerRepo.GetTransaction(ctx, txID)
}
