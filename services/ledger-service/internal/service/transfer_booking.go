package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/ledger-service/internal/domain"
)

// BookTransfer moves funds between two accounts as one balanced double-entry
// transaction, guaranteeing:
//
//  1. Funds availability — the source account's available balance (cleared
//     minus active reservations) is checked under the per-account writer lock
//     before any entry is written, so concurrent spends cannot overshoot.
//  2. Exactly-once application — an idempotency-keyed lookup returns the
//     original result on retry, and a partially-booked transaction (crash
//     between the debit and credit inserts) is completed, never duplicated.
//
// The destination is credited as a normal CREDIT leg, so the recipient sees
// the money immediately in their real ledger balance (Monzo-style instant
// internal transfer).
func (s *LedgerService) BookTransfer(ctx context.Context, req *domain.TransferRequest) (*domain.LedgerTransaction, []*domain.LedgerEntry, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}

	// Serialise availability check + booking per source account.
	unlock := s.lockAccount(req.SourceAccountID)
	defer unlock()

	// Exactly-once: a retried transfer returns the original transaction.
	existing, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		entries, err := s.ledgerRepo.GetEntriesByTransaction(ctx, existing.TransactionID)
		if err != nil {
			return nil, nil, err
		}
		if len(entries) == 2 {
			return existing, entries, nil
		}
		// Crash recovery: finish the missing leg(s) of a half-booked transfer.
		completed, repaired, err := s.completePartialBooking(ctx, existing, req)
		if err != nil {
			return nil, nil, err
		}
		if completed {
			return existing, repaired, nil
		}
		return existing, repaired, nil
	}

	available, err := s.GetAvailableBalance(ctx, req.SourceAccountID)
	if err != nil {
		return nil, nil, fmt.Errorf("checking available balance: %w", err)
	}
	if available < req.Amount {
		return nil, nil, domain.ErrInsufficientFunds
	}

	// Defense-in-depth: the ledger of record independently refuses bookings
	// against accounts under emergency lockdown or frozen by the invariant
	// monitor, so every money-OUT path (transfers, pot withdrawals, payments)
	// is covered even if an upstream check is skipped.
	if err := s.ensureAccountWritable(ctx, req.SourceAccountID); err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	txID := uuid.New()
	tx := &domain.LedgerTransaction{
		TransactionID:   txID,
		IdempotencyKey:  req.IdempotencyKey,
		TransactionType: domain.TransactionTypeTransfer,
		Status:          domain.TransactionStatusPending,
		TotalAmount:     req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		CorrelationID:   req.IdempotencyKey,
		EventVersion:    1,
		CreatedAt:       now,
	}
	if err := s.ledgerRepo.CreateTransaction(ctx, tx); err != nil {
		return nil, nil, fmt.Errorf("creating transfer transaction: %w", err)
	}

	entries, err := s.insertTransferLegs(ctx, tx, req, now)
	if err != nil {
		// Leave the transaction PENDING; a retry of the same idempotency key
		// repairs and completes it (completePartialBooking).
		return nil, nil, err
	}

	completedAt := now
	tx.Status = domain.TransactionStatusCompleted
	tx.CompletedAt = &completedAt
	if err := s.ledgerRepo.UpdateTransactionStatus(ctx, txID, domain.TransactionStatusCompleted); err != nil {
		s.logger.Error().Err(err).Str("transaction_id", txID.String()).Msg("failed to mark transfer completed")
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "ledger.transfer.completed", tx)
	}

	s.logger.Info().
		Str("transaction_id", txID.String()).
		Str("source_account_id", req.SourceAccountID.String()).
		Str("destination_account_id", req.DestinationAccountID.String()).
		Int64("amount", req.Amount).
		Msg("transfer booked")

	return tx, entries, nil
}

// insertTransferLegs writes the DEBIT (source) and CREDIT (destination) legs.
// The debit is written first so a crash mid-booking can only leave the credit
// missing — never the funds released without the recipient being credited.
func (s *LedgerService) insertTransferLegs(ctx context.Context, tx *domain.LedgerTransaction, req *domain.TransferRequest, now time.Time) ([]*domain.LedgerEntry, error) {
	srcBalance, err := s.ledgerRepo.GetLatestBalance(ctx, req.SourceAccountID)
	if err != nil {
		return nil, fmt.Errorf("reading source balance: %w", err)
	}
	debit := &domain.LedgerEntry{
		EntryID:        uuid.New(),
		AccountID:      req.SourceAccountID,
		TransactionID:  tx.TransactionID,
		EntryType:      domain.EntryTypeDebit,
		EntryDirection: domain.EntryDirectionOutbound,
		Amount:         req.Amount,
		Currency:       req.Currency,
		BalanceBefore:  srcBalance,
		BalanceAfter:   srcBalance - req.Amount,
		Description:    req.Description,
		Category:       domain.InferCategory(req.Description),
		CorrelationID:  req.IdempotencyKey,
		EventVersion:   1,
		CreatedAt:      now,
	}
	if err := s.ledgerRepo.CreateEntry(ctx, debit); err != nil {
		return nil, fmt.Errorf("booking debit leg: %w", err)
	}

	dstBalance, err := s.ledgerRepo.GetLatestBalance(ctx, req.DestinationAccountID)
	if err != nil {
		return nil, fmt.Errorf("reading destination balance: %w", err)
	}
	credit := &domain.LedgerEntry{
		EntryID:        uuid.New(),
		AccountID:      req.DestinationAccountID,
		TransactionID:  tx.TransactionID,
		EntryType:      domain.EntryTypeCredit,
		EntryDirection: domain.EntryDirectionInbound,
		Amount:         req.Amount,
		Currency:       req.Currency,
		BalanceBefore:  dstBalance,
		BalanceAfter:   dstBalance + req.Amount,
		Description:    req.Description,
		Category:       domain.InferCategory(req.Description),
		CorrelationID:  req.IdempotencyKey,
		EventVersion:   1,
		CreatedAt:      now,
	}
	if err := s.ledgerRepo.CreateEntry(ctx, credit); err != nil {
		return nil, fmt.Errorf("booking credit leg: %w", err)
	}

	return []*domain.LedgerEntry{debit, credit}, nil
}

// completePartialBooking repairs a transaction that was interrupted between
// the two entry inserts (debit written, credit missing) so retries converge on
// a fully-booked transfer.
func (s *LedgerService) completePartialBooking(ctx context.Context, tx *domain.LedgerTransaction, req *domain.TransferRequest) (bool, []*domain.LedgerEntry, error) {
	entries, err := s.ledgerRepo.GetEntriesByTransaction(ctx, tx.TransactionID)
	if err != nil {
		return false, nil, err
	}

	hasDebit := false
	hasCredit := false
	for _, e := range entries {
		if e.AccountID == req.SourceAccountID {
			hasDebit = true
		}
		if e.AccountID == req.DestinationAccountID {
			hasCredit = true
		}
	}

	now := time.Now().UTC()
	if !hasDebit {
		debit, err := s.buildLeg(ctx, req.SourceAccountID, tx.TransactionID, domain.EntryTypeDebit, req, now)
		if err != nil {
			return false, nil, err
		}
		if err := s.ledgerRepo.CreateEntry(ctx, debit); err != nil {
			return false, nil, err
		}
		entries = append(entries, debit)
	}
	if !hasCredit {
		credit, err := s.buildLeg(ctx, req.DestinationAccountID, tx.TransactionID, domain.EntryTypeCredit, req, now)
		if err != nil {
			return false, nil, err
		}
		if err := s.ledgerRepo.CreateEntry(ctx, credit); err != nil {
			return false, nil, err
		}
		entries = append(entries, credit)
	}

	completedAt := now
	tx.Status = domain.TransactionStatusCompleted
	tx.CompletedAt = &completedAt
	if err := s.ledgerRepo.UpdateTransactionStatus(ctx, tx.TransactionID, domain.TransactionStatusCompleted); err != nil {
		s.logger.Error().Err(err).Str("transaction_id", tx.TransactionID.String()).Msg("failed to mark repaired transfer completed")
	}
	return true, entries, nil
}

func (s *LedgerService) buildLeg(ctx context.Context, accountID, txID uuid.UUID, entryType domain.EntryType, req *domain.TransferRequest, now time.Time) (*domain.LedgerEntry, error) {
	balance, err := s.ledgerRepo.GetLatestBalance(ctx, accountID)
	if err != nil {
		return nil, err
	}
	direction := domain.EntryDirectionInbound
	balanceAfter := balance + req.Amount
	if entryType == domain.EntryTypeDebit {
		direction = domain.EntryDirectionOutbound
		balanceAfter = balance - req.Amount
	}
	return &domain.LedgerEntry{
		EntryID:        uuid.New(),
		AccountID:      accountID,
		TransactionID:  txID,
		EntryType:      entryType,
		EntryDirection: direction,
		Amount:         req.Amount,
		Currency:       req.Currency,
		BalanceBefore:  balance,
		BalanceAfter:   balanceAfter,
		Description:    req.Description,
		CorrelationID:  req.IdempotencyKey,
		EventVersion:   1,
		CreatedAt:      now,
	}, nil
}
