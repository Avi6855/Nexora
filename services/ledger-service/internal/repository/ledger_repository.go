package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/ledger-service/internal/domain"
)

type LedgerRepository interface {
	CreateTransaction(ctx context.Context, tx *domain.LedgerTransaction) error
	GetTransaction(ctx context.Context, id uuid.UUID) (*domain.LedgerTransaction, error)
	GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error)
	UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus) error

	CreateEntry(ctx context.Context, entry *domain.LedgerEntry) error
	CreateEntryIfNotExists(ctx context.Context, entry *domain.LedgerEntry) (bool, error)
	GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.LedgerEntry, error)
	GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error)
	GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error)

	CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error)
	GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error)
	GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error)
	UpdateReservationStatus(ctx context.Context, id uuid.UUID, status domain.ReservationStatus) error

	SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error)
	SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error)
	GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error)
	SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error)
}

type cassandraLedgerRepository struct {
	session *gocql.Session
}

func NewCassandraLedgerRepository(session *gocql.Session) LedgerRepository {
	return &cassandraLedgerRepository{session: session}
}

func (r *cassandraLedgerRepository) CreateTransaction(ctx context.Context, tx *domain.LedgerTransaction) error {
	query := `INSERT INTO ledger_transactions (transaction_id, idempotency_key, transaction_type, status, total_amount, currency, description, correlation_id, causation_id, event_version, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

	applied, err := r.session.Query(query,
		tx.TransactionID,
		tx.IdempotencyKey,
		string(tx.TransactionType),
		string(tx.Status),
		tx.TotalAmount,
		tx.Currency,
		tx.Description,
		tx.CorrelationID,
		tx.CausationID,
		tx.EventVersion,
		tx.CreatedAt,
		tx.CompletedAt,
	).WithContext(ctx).ScanCAS()

	if err != nil {
		return fmt.Errorf("inserting transaction: %w", err)
	}
	if !applied {
		return fmt.Errorf("idempotency conflict: transaction with key %s already exists", tx.IdempotencyKey)
	}
	return nil
}

func (r *cassandraLedgerRepository) GetTransaction(ctx context.Context, id uuid.UUID) (*domain.LedgerTransaction, error) {
	var tx domain.LedgerTransaction
	var txType, status string

	query := `SELECT transaction_id, idempotency_key, transaction_type, status, total_amount, currency, description, correlation_id, causation_id, event_version, created_at, completed_at
		FROM ledger_transactions WHERE transaction_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&tx.TransactionID,
		&tx.IdempotencyKey,
		&txType,
		&status,
		&tx.TotalAmount,
		&tx.Currency,
		&tx.Description,
		&tx.CorrelationID,
		&tx.CausationID,
		&tx.EventVersion,
		&tx.CreatedAt,
		&tx.CompletedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return nil, err
	}

	tx.TransactionType = domain.TransactionType(txType)
	tx.Status = domain.TransactionStatus(status)
	return &tx, nil
}

func (r *cassandraLedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	var tx domain.LedgerTransaction
	var txType, status string

	query := `SELECT transaction_id, idempotency_key, transaction_type, status, total_amount, currency, description, correlation_id, causation_id, event_version, created_at, completed_at
		FROM ledger_transactions WHERE idempotency_key = ?`

	err := r.session.Query(query, key).WithContext(ctx).Scan(
		&tx.TransactionID,
		&tx.IdempotencyKey,
		&txType,
		&status,
		&tx.TotalAmount,
		&tx.Currency,
		&tx.Description,
		&tx.CorrelationID,
		&tx.CausationID,
		&tx.EventVersion,
		&tx.CreatedAt,
		&tx.CompletedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	tx.TransactionType = domain.TransactionType(txType)
	tx.Status = domain.TransactionStatus(status)
	return &tx, nil
}

func (r *cassandraLedgerRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus) error {
	now := time.Now().UTC()
	query := `UPDATE ledger_transactions SET status = ?, completed_at = ? WHERE transaction_id = ?`
	return r.session.Query(query, string(status), now, id).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) CreateEntry(ctx context.Context, entry *domain.LedgerEntry) error {
	query := `INSERT INTO ledger_entries (entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, correlation_id, causation_id, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		entry.EntryID,
		entry.AccountID,
		entry.TransactionID,
		string(entry.EntryType),
		string(entry.EntryDirection),
		entry.Amount,
		entry.Currency,
		entry.BalanceBefore,
		entry.BalanceAfter,
		entry.Description,
		entry.CorrelationID,
		entry.CausationID,
		entry.EventVersion,
		entry.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) CreateEntryIfNotExists(ctx context.Context, entry *domain.LedgerEntry) (bool, error) {
	query := `INSERT INTO ledger_entries (entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, correlation_id, causation_id, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

	applied, err := r.session.Query(query,
		entry.EntryID,
		entry.AccountID,
		entry.TransactionID,
		string(entry.EntryType),
		string(entry.EntryDirection),
		entry.Amount,
		entry.Currency,
		entry.BalanceBefore,
		entry.BalanceAfter,
		entry.Description,
		entry.CorrelationID,
		entry.CausationID,
		entry.EventVersion,
		entry.CreatedAt,
	).WithContext(ctx).ScanCAS()

	if err != nil {
		return false, fmt.Errorf("inserting entry: %w", err)
	}
	return applied, nil
}

func (r *cassandraLedgerRepository) GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.LedgerEntry, error) {
	var entries []*domain.LedgerEntry

	query := `SELECT entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, correlation_id, causation_id, event_version, created_at
		FROM ledger_entries WHERE account_id = ? LIMIT ?`

	iter := r.session.Query(query, accountID, limit).WithContext(ctx).Iter()
	defer iter.Close()

	var entry domain.LedgerEntry
	var entryType, entryDirection string

	for iter.Scan(
		&entry.EntryID,
		&entry.AccountID,
		&entry.TransactionID,
		&entryType,
		&entryDirection,
		&entry.Amount,
		&entry.Currency,
		&entry.BalanceBefore,
		&entry.BalanceAfter,
		&entry.Description,
		&entry.CorrelationID,
		&entry.CausationID,
		&entry.EventVersion,
		&entry.CreatedAt,
	) {
		entry.EntryType = domain.EntryType(entryType)
		entry.EntryDirection = domain.EntryDirection(entryDirection)
		e := entry
		entries = append(entries, &e)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return entries, nil
}

func (r *cassandraLedgerRepository) GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error) {
	var entries []*domain.LedgerEntry

	query := `SELECT entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, correlation_id, causation_id, event_version, created_at
		FROM ledger_entries WHERE transaction_id = ?`

	iter := r.session.Query(query, txID).WithContext(ctx).Iter()
	defer iter.Close()

	var entry domain.LedgerEntry
	var entryType, entryDirection string

	for iter.Scan(
		&entry.EntryID,
		&entry.AccountID,
		&entry.TransactionID,
		&entryType,
		&entryDirection,
		&entry.Amount,
		&entry.Currency,
		&entry.BalanceBefore,
		&entry.BalanceAfter,
		&entry.Description,
		&entry.CorrelationID,
		&entry.CausationID,
		&entry.EventVersion,
		&entry.CreatedAt,
	) {
		entry.EntryType = domain.EntryType(entryType)
		entry.EntryDirection = domain.EntryDirection(entryDirection)
		e := entry
		entries = append(entries, &e)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return entries, nil
}

func (r *cassandraLedgerRepository) GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error) {
	var entries []*domain.LedgerEntry

	query := `SELECT entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, correlation_id, causation_id, event_version, created_at
		FROM ledger_entries WHERE account_id = ?`

	iter := r.session.Query(query, accountID).WithContext(ctx).Iter()
	defer iter.Close()

	var entry domain.LedgerEntry
	var entryType, entryDirection string

	for iter.Scan(
		&entry.EntryID,
		&entry.AccountID,
		&entry.TransactionID,
		&entryType,
		&entryDirection,
		&entry.Amount,
		&entry.Currency,
		&entry.BalanceBefore,
		&entry.BalanceAfter,
		&entry.Description,
		&entry.CorrelationID,
		&entry.CausationID,
		&entry.EventVersion,
		&entry.CreatedAt,
	) {
		entry.EntryType = domain.EntryType(entryType)
		entry.EntryDirection = domain.EntryDirection(entryDirection)
		e := entry
		entries = append(entries, &e)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return entries, nil
}

func (r *cassandraLedgerRepository) CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error) {
	query := `INSERT INTO reservations (reservation_id, account_id, transaction_id, amount, currency, status, expires_at, created_at, released_at, settled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

	applied, err := r.session.Query(query,
		reservation.ReservationID,
		reservation.AccountID,
		reservation.TransactionID,
		reservation.Amount,
		reservation.Currency,
		string(reservation.Status),
		reservation.ExpiresAt,
		reservation.CreatedAt,
		reservation.ReleasedAt,
		reservation.SettledAt,
	).WithContext(ctx).ScanCAS()

	if err != nil {
		return false, fmt.Errorf("inserting reservation: %w", err)
	}
	return applied, nil
}

func (r *cassandraLedgerRepository) GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	var res domain.Reservation
	var status string

	query := `SELECT reservation_id, account_id, transaction_id, amount, currency, status, expires_at, created_at, released_at, settled_at
		FROM reservations WHERE reservation_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&res.ReservationID,
		&res.AccountID,
		&res.TransactionID,
		&res.Amount,
		&res.Currency,
		&status,
		&res.ExpiresAt,
		&res.CreatedAt,
		&res.ReleasedAt,
		&res.SettledAt,
	)

	if err == gocql.ErrNotFound {
		return nil, domain.ErrReservationNotFound
	}
	if err != nil {
		return nil, err
	}

	res.Status = domain.ReservationStatus(status)
	return &res, nil
}

func (r *cassandraLedgerRepository) GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error) {
	var reservations []*domain.Reservation

	query := `SELECT reservation_id, account_id, transaction_id, amount, currency, status, expires_at, created_at, released_at, settled_at
		FROM reservations WHERE account_id = ? AND status = 'ACTIVE'`

	iter := r.session.Query(query, accountID).WithContext(ctx).Iter()
	defer iter.Close()

	var res domain.Reservation
	var status string

	for iter.Scan(
		&res.ReservationID,
		&res.AccountID,
		&res.TransactionID,
		&res.Amount,
		&res.Currency,
		&status,
		&res.ExpiresAt,
		&res.CreatedAt,
		&res.ReleasedAt,
		&res.SettledAt,
	) {
		res.Status = domain.ReservationStatus(status)
		r := res
		reservations = append(reservations, &r)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return reservations, nil
}

func (r *cassandraLedgerRepository) UpdateReservationStatus(ctx context.Context, id uuid.UUID, status domain.ReservationStatus) error {
	now := time.Now().UTC()
	var query string

	switch status {
	case domain.ReservationStatusReleased:
		query = `UPDATE reservations SET status = ?, released_at = ? WHERE reservation_id = ? IF status = 'ACTIVE'`
	case domain.ReservationStatusSettled:
		query = `UPDATE reservations SET status = ?, settled_at = ? WHERE reservation_id = ? IF status = 'ACTIVE'`
	case domain.ReservationStatusExpired:
		query = `UPDATE reservations SET status = ? WHERE reservation_id = ? IF status = 'ACTIVE'`
	default:
		return fmt.Errorf("invalid reservation status transition to %s", status)
	}

	var err error
	if status == domain.ReservationStatusExpired {
		applied, updateErr := r.session.Query(query, string(status), id).WithContext(ctx).ScanCAS()
		err = updateErr
		if !applied && err == nil {
			return domain.ErrReservationNotActive
		}
	} else {
		applied, updateErr := r.session.Query(query, string(status), now, id).WithContext(ctx).ScanCAS()
		err = updateErr
		if !applied && err == nil {
			return domain.ErrReservationNotActive
		}
	}

	return err
}

func (r *cassandraLedgerRepository) SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT SUM(amount) FROM ledger_entries WHERE account_id = ? AND entry_type = 'DEBIT'`
	var total int64
	err := r.session.Query(query, accountID).WithContext(ctx).Scan(&total)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return total, err
}

func (r *cassandraLedgerRepository) SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT SUM(amount) FROM ledger_entries WHERE account_id = ? AND entry_type = 'CREDIT'`
	var total int64
	err := r.session.Query(query, accountID).WithContext(ctx).Scan(&total)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return total, err
}

func (r *cassandraLedgerRepository) GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT balance_after FROM ledger_entries WHERE account_id = ? LIMIT 1`
	var balance int64
	err := r.session.Query(query, accountID).WithContext(ctx).Scan(&balance)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return balance, err
}

func (r *cassandraLedgerRepository) SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT SUM(amount) FROM reservations WHERE account_id = ? AND status = 'ACTIVE'`
	var total int64
	err := r.session.Query(query, accountID).WithContext(ctx).Scan(&total)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return total, err
}
