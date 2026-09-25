package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
)

// BookRefund credits a customer for money that came back from outside the bank
// (a merchant refund of a settled card presentment).
//
// It is the mirror of the settled card transaction: the customer is CREDITED
// and the suspense clearing account is DEBITED, so the system-wide
// debits == credits invariant holds for refunds exactly as it does for spends.
// The pair travels in one logged batch (CreateEntryPair): a refund that wrote
// only the customer leg would leave the ledger unbalanced, and a retry could
// not tell whether the credit had landed.
//
// Exactly-once is the refund id used as the idempotency key: a replayed refund
// returns the original transaction instead of paying the customer twice.
func (s *LedgerService) BookRefund(ctx context.Context, req *domain.RefundRequest) (*domain.LedgerTransaction, []*domain.LedgerEntry, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}

	// Serialise with every other clearing-account movement so two refunds
	// cannot read the same clearing balance and both book off it.
	unlock := s.lockAccount(domain.ClearingAccountID)
	defer unlock()

	existing, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		entries, err := s.ledgerRepo.GetEntriesByTransaction(ctx, existing.TransactionID)
		if err != nil {
			return nil, nil, err
		}
		return existing, entries, nil
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	tx := &domain.LedgerTransaction{
		TransactionID:   uuid.New(),
		IdempotencyKey:  req.IdempotencyKey,
		TransactionType: domain.TransactionTypeRefund,
		Status:          domain.TransactionStatusPending,
		TotalAmount:     req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		CorrelationID:   req.IdempotencyKey,
		EventVersion:    1,
		CreatedAt:       now,
	}
	if err := s.ledgerRepo.CreateTransaction(ctx, tx); err != nil {
		return nil, nil, fmt.Errorf("creating refund transaction: %w", err)
	}

	clearingBalance, err := s.ledgerRepo.GetLatestBalance(ctx, domain.ClearingAccountID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not read the clearing balance, defaulting to 0")
		clearingBalance = 0
	}
	customerBalance, err := s.ledgerRepo.GetLatestBalance(ctx, req.AccountID)
	if err != nil {
		s.logger.Warn().Err(err).Str("account_id", req.AccountID.String()).Msg("could not read the account balance, defaulting to 0")
		customerBalance = 0
	}

	// The customer's leg is the CREDIT (money in), the clearing account's leg
	// is the DEBIT (money out of suspense).
	credit := &domain.LedgerEntry{
		EntryID:        uuid.New(),
		AccountID:      req.AccountID,
		TransactionID:  tx.TransactionID,
		EntryType:      domain.EntryTypeCredit,
		EntryDirection: domain.EntryDirectionInbound,
		Amount:         req.Amount,
		Currency:       req.Currency,
		BalanceBefore:  customerBalance,
		BalanceAfter:   customerBalance + req.Amount,
		Description:    req.Description,
		Category:       domain.InferCategory(req.Description),
		CorrelationID:  req.IdempotencyKey,
		EventVersion:   1,
		CreatedAt:      now,
	}
	debit := &domain.LedgerEntry{
		EntryID:        uuid.New(),
		AccountID:      domain.ClearingAccountID,
		TransactionID:  tx.TransactionID,
		EntryType:      domain.EntryTypeDebit,
		EntryDirection: domain.EntryDirectionOutbound,
		Amount:         req.Amount,
		Currency:       req.Currency,
		BalanceBefore:  clearingBalance,
		BalanceAfter:   clearingBalance - req.Amount,
		Description:    req.Description,
		Category:       domain.InferCategory(req.Description),
		CorrelationID:  req.IdempotencyKey,
		EventVersion:   1,
		CreatedAt:      now,
	}
	if err := s.ledgerRepo.CreateEntryPair(ctx, debit, credit); err != nil {
		// Leave the transaction PENDING; a retry of the same idempotency key
		// reports what was actually written.
		return nil, nil, fmt.Errorf("booking refund pair: %w", err)
	}

	completedAt := now
	tx.Status = domain.TransactionStatusCompleted
	tx.CompletedAt = &completedAt
	if err := s.ledgerRepo.UpdateTransactionStatus(ctx, tx.TransactionID, domain.TransactionStatusCompleted); err != nil {
		s.logger.Error().Err(err).Str("transaction_id", tx.TransactionID.String()).Msg("failed to mark refund completed")
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "ledger.refund.completed", tx)
	}

	s.logger.Info().
		Str("transaction_id", tx.TransactionID.String()).
		Str("account_id", req.AccountID.String()).
		Int64("amount", req.Amount).
		Msg("refund booked")

	return tx, []*domain.LedgerEntry{debit, credit}, nil
}
