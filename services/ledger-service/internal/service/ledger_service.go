package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/clients"
	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/events"
	"github.com/nexora/nexora/services/ledger-service/internal/repository"
)

type LedgerService struct {
	ledgerRepo repository.LedgerRepository
	producer   *events.KafkaProducer
	logger     zerolog.Logger
	// incidents is the optional ops integration used by the invariant monitor
	// to file SEV1 integrity incidents (nil = freeze + event trail only).
	incidents clients.IncidentReporter

	// accountLocks serialises balance-mutating operations per account. The
	// check-then-act in ReserveFunds (available >= amount, then INSERT) is
	// otherwise a TOCTOU race; with a single writer per account the invariant
	// "reserved never exceeds balance" holds deterministically. Scale-out would
	// shard accounts onto dedicated single-writer partitions (actor model).
	accountLocks sync.Map // accountID (uuid.UUID) -> *sync.Mutex
}

func NewLedgerService(ledgerRepo repository.LedgerRepository, producer *events.KafkaProducer, logger zerolog.Logger) *LedgerService {
	return &LedgerService{
		ledgerRepo:   ledgerRepo,
		producer:     producer,
		logger:       logger,
		accountLocks: sync.Map{},
	}
}

// lockAccount acquires the per-account mutex and returns its unlock function.
func (s *LedgerService) lockAccount(accountID uuid.UUID) func() {
	v, _ := s.accountLocks.LoadOrStore(accountID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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

	// Refuse new bookings against accounts under lockdown or frozen by the
	// invariant monitor before any state is written (idempotent retries of an
	// earlier transaction return above and are never blocked).
	for _, line := range req.Lines {
		if err := s.ensureAccountWritable(ctx, line.AccountID); err != nil {
			return nil, nil, err
		}
	}

	txID := uuid.New()
	now := time.Now().UTC()

	tx := &domain.LedgerTransaction{
		TransactionID:   txID,
		IdempotencyKey:  req.IdempotencyKey,
		TransactionType: req.TransactionType,
		Status:          domain.TransactionStatusPending,
		TotalAmount:     req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		CorrelationID:   req.CorrelationID,
		CausationID:     req.CausationID,
		EventVersion:    1,
		CreatedAt:       now,
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
			Category:       domain.InferCategory(req.Description),
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

	// Serialise the availability check + insert per account so concurrent
	// authorisations cannot overspend the available balance.
	unlock := s.lockAccount(accountID)
	defer unlock()

	// A frozen (integrity-violated) account cannot take new holds either.
	if err := s.ensureAccountWritable(ctx, accountID); err != nil {
		return nil, err
	}

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
		Category:       domain.CategoryOther,
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
	return s.GetEntriesFiltered(ctx, accountID, domain.EntryFilter{Limit: limit})
}

// GetEntriesFiltered returns ledger entries with optional search/category/type
// filters, and attaches any user notes from transaction_notes to each entry so
// the app sees description + note in one payload.
func (s *LedgerService) GetEntriesFiltered(ctx context.Context, accountID uuid.UUID, filter domain.EntryFilter) ([]*domain.LedgerEntry, error) {
	entries, err := s.ledgerRepo.GetEntriesByAccount(ctx, accountID, filter)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		note, err := s.ledgerRepo.GetNote(ctx, e.EntryID)
		if err != nil {
			s.logger.Warn().Err(err).Str("entry_id", e.EntryID.String()).Msg("could not read transaction note")
			continue
		}
		if note != nil {
			e.Note = note.Note
		}
	}
	return entries, nil
}

// UpdateEntryNote stores (or clears, with an empty note) the caller's
// annotation on a ledger entry. The caller must own the entry's account.
func (s *LedgerService) UpdateEntryNote(ctx context.Context, entryID, userID uuid.UUID, note string) error {
	entry, err := s.ledgerRepo.GetEntryByID(ctx, entryID)
	if err != nil {
		return err
	}
	owner, err := s.ledgerRepo.GetAccountOwner(ctx, entry.AccountID)
	if err != nil {
		return fmt.Errorf("resolving entry owner: %w", err)
	}
	if owner != userID {
		return domain.ErrAccountNotFound // do not leak other users' entries
	}
	return s.ledgerRepo.UpsertNote(ctx, &domain.TransactionNote{
		EntryID:   entryID,
		UserID:    userID,
		Note:      note,
		UpdatedAt: time.Now().UTC(),
	})
}

func (s *LedgerService) GetTransaction(ctx context.Context, txID uuid.UUID) (*domain.LedgerTransaction, error) {
	return s.ledgerRepo.GetTransaction(ctx, txID)
}

// AccountOwner resolves the user that owns an account (ownership checks on
// user-facing reads/writes live in the transport layer).
func (s *LedgerService) AccountOwner(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error) {
	return s.ledgerRepo.GetAccountOwner(ctx, accountID)
}

// GetReservation returns a reservation so callers can authorise access before
// releasing/settling it.
func (s *LedgerService) GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	return s.ledgerRepo.GetReservation(ctx, id)
}
