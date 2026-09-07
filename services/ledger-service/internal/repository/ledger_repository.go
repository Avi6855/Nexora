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
	GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, filter domain.EntryFilter) ([]*domain.LedgerEntry, error)
	GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error)
	GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error)

	// Transaction notes live in their own table so the ledger stays immutable.
	UpsertNote(ctx context.Context, note *domain.TransactionNote) error
	GetNote(ctx context.Context, entryID uuid.UUID) (*domain.TransactionNote, error)

	// IsAccountLocked reads the account's emergency-lockdown flag directly
	// from the accounts table (same keyspace) — defense-in-depth enforcement
	// at the ledger of record for money-OUT bookings.
	IsAccountLocked(ctx context.Context, accountID uuid.UUID) (bool, error)

	CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error)
	GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error)
	GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error)
	UpdateReservationStatus(ctx context.Context, id uuid.UUID, status domain.ReservationStatus) error

	SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error)
	SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error)
	GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error)
	SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error)
	GetAccountOwner(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error)

	// MarkPaymentBooked atomically claims the payment for booking (LWT): the
	// first caller gets true, duplicates false.
	MarkPaymentBooked(ctx context.Context, paymentID uuid.UUID, idempotencyKey, eventType string) (bool, error)

	// GetEntryByID resolves a single ledger entry by its id. Entries are
	// partitioned by account, so implementations may fall back to a scan at
	// dev scale (production would keep an entry_id lookup table).
	GetEntryByID(ctx context.Context, entryID uuid.UUID) (*domain.LedgerEntry, error)

	// ── Ledger invariant monitor ──
	// ListEntryAccounts returns every account that has at least one entry
	// (the sweep target set for the periodic integrity scan).
	ListEntryAccounts(ctx context.Context) ([]uuid.UUID, error)

	GetIntegrityGuard(ctx context.Context, accountID uuid.UUID) (*domain.IntegrityGuard, error)
	SetIntegrityGuard(ctx context.Context, g *domain.IntegrityGuard) error
	ClearIntegrityGuard(ctx context.Context, accountID uuid.UUID) error
	InsertIntegrityEvent(ctx context.Context, ev *domain.IntegrityEvent) error
	ListIntegrityEvents(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.IntegrityEvent, error)
}

type cassandraLedgerRepository struct {
	session *gocql.Session
}

func NewCassandraLedgerRepository(session *gocql.Session) LedgerRepository {
	return &cassandraLedgerRepository{session: session}
}

// toUUID converts google/uuid values (a named [16]byte array) to gocql.UUID,
// the only type gocql can marshal/unmarshal into CQL uuid columns.
func toUUID(id uuid.UUID) gocql.UUID {
	return gocql.UUID(id)
}

func (r *cassandraLedgerRepository) CreateTransaction(ctx context.Context, tx *domain.LedgerTransaction) error {
	query := `INSERT INTO ledger_transactions (transaction_id, idempotency_key, transaction_type, status, total_amount, currency, description, correlation_id, causation_id, event_version, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

	applied, err := r.session.Query(query,
		toUUID(tx.TransactionID),
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
	var txID gocql.UUID
	var txType, status string

	query := `SELECT transaction_id, idempotency_key, transaction_type, status, total_amount, currency, description, correlation_id, causation_id, event_version, created_at, completed_at
		FROM ledger_transactions WHERE transaction_id = ?`

	err := r.session.Query(query, toUUID(id)).WithContext(ctx).Scan(
		&txID,
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

	tx.TransactionID = uuid.UUID(txID)
	tx.TransactionType = domain.TransactionType(txType)
	tx.Status = domain.TransactionStatus(status)
	return &tx, nil
}

func (r *cassandraLedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	var tx domain.LedgerTransaction
	var txID gocql.UUID
	var txType, status string

	query := `SELECT transaction_id, idempotency_key, transaction_type, status, total_amount, currency, description, correlation_id, causation_id, event_version, created_at, completed_at
		FROM ledger_transactions WHERE idempotency_key = ?`

	err := r.session.Query(query, key).WithContext(ctx).Scan(
		&txID,
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

	tx.TransactionID = uuid.UUID(txID)
	tx.TransactionType = domain.TransactionType(txType)
	tx.Status = domain.TransactionStatus(status)
	return &tx, nil
}

func (r *cassandraLedgerRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus) error {
	now := time.Now().UTC()
	query := `UPDATE ledger_transactions SET status = ?, completed_at = ? WHERE transaction_id = ?`
	return r.session.Query(query, string(status), now, toUUID(id)).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) CreateEntry(ctx context.Context, entry *domain.LedgerEntry) error {
	query := `INSERT INTO ledger_entries (entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, category, correlation_id, causation_id, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		toUUID(entry.EntryID),
		toUUID(entry.AccountID),
		toUUID(entry.TransactionID),
		string(entry.EntryType),
		string(entry.EntryDirection),
		entry.Amount,
		entry.Currency,
		entry.BalanceBefore,
		entry.BalanceAfter,
		entry.Description,
		string(entry.Category),
		entry.CorrelationID,
		entry.CausationID,
		entry.EventVersion,
		entry.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) CreateEntryIfNotExists(ctx context.Context, entry *domain.LedgerEntry) (bool, error) {
	query := `INSERT INTO ledger_entries (entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, category, correlation_id, causation_id, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

	applied, err := r.session.Query(query,
		toUUID(entry.EntryID),
		toUUID(entry.AccountID),
		toUUID(entry.TransactionID),
		string(entry.EntryType),
		string(entry.EntryDirection),
		entry.Amount,
		entry.Currency,
		entry.BalanceBefore,
		entry.BalanceAfter,
		entry.Description,
		string(entry.Category),
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

const fullEntryCols = `entry_id, account_id, transaction_id, entry_type, entry_direction, amount, currency, balance_before, balance_after, description, category, correlation_id, causation_id, event_version, created_at`

func scanEntries(iter *gocql.Iter) ([]*domain.LedgerEntry, error) {
	var entries []*domain.LedgerEntry
	var entryID, accountID, transactionID gocql.UUID
	var entryType, entryDirection string
	var amount, balanceBefore, balanceAfter int64
	var currency, description, correlationID, causationID string
	var category string
	var eventVersion int
	var createdAt time.Time

	for iter.Scan(
		&entryID,
		&accountID,
		&transactionID,
		&entryType,
		&entryDirection,
		&amount,
		&currency,
		&balanceBefore,
		&balanceAfter,
		&description,
		&category,
		&correlationID,
		&causationID,
		&eventVersion,
		&createdAt,
	) {
		e := &domain.LedgerEntry{
			EntryID:        uuid.UUID(entryID),
			AccountID:      uuid.UUID(accountID),
			TransactionID:  uuid.UUID(transactionID),
			EntryType:      domain.EntryType(entryType),
			EntryDirection: domain.EntryDirection(entryDirection),
			Amount:         amount,
			Currency:       currency,
			BalanceBefore:  balanceBefore,
			BalanceAfter:   balanceAfter,
			Description:    description,
			Category:       domain.Category(category),
			CorrelationID:  correlationID,
			CausationID:    causationID,
			EventVersion:   eventVersion,
			CreatedAt:      createdAt,
		}
		entries = append(entries, e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *cassandraLedgerRepository) GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, filter domain.EntryFilter) ([]*domain.LedgerEntry, error) {
	// Unfiltered reads map straight to a bounded partition query.
	if filter.Query == "" && filter.Category == "" && filter.Type == "" {
		limit := filter.Limit
		if limit <= 0 {
			limit = 100
		}
		query := `SELECT ` + fullEntryCols + `
			FROM ledger_entries WHERE account_id = ? LIMIT ?`
		iter := r.session.Query(query, toUUID(accountID), limit).WithContext(ctx).Iter()
		defer iter.Close()
		return scanEntries(iter)
	}

	// Filtered reads scan the account partition and filter in memory (the
	// note lives in transaction_notes and is joined by the service layer). At
	// dev scale this is fine; production would use explicit lookup tables.
	iter := r.session.Query(`SELECT `+fullEntryCols+` FROM ledger_entries WHERE account_id = ?`, toUUID(accountID)).WithContext(ctx).Iter()
	entries, err := scanEntries(iter)
	if err != nil {
		return nil, err
	}
	filtered := make([]*domain.LedgerEntry, 0, len(entries))
	for _, e := range entries {
		if filter.Matches(e) {
			filtered = append(filtered, e)
		}
		if filter.Limit > 0 && len(filtered) >= filter.Limit {
			break
		}
	}
	return filtered, nil
}

func (r *cassandraLedgerRepository) GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error) {
	query := `SELECT ` + fullEntryCols + `
		FROM ledger_entries WHERE transaction_id = ?`

	iter := r.session.Query(query, toUUID(txID)).WithContext(ctx).Iter()
	defer iter.Close()

	return scanEntries(iter)
}

func (r *cassandraLedgerRepository) GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error) {
	query := `SELECT ` + fullEntryCols + `
		FROM ledger_entries WHERE account_id = ?`

	iter := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Iter()
	defer iter.Close()

	return scanEntries(iter)
}

func (r *cassandraLedgerRepository) CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error) {
	query := `INSERT INTO reservations (reservation_id, account_id, transaction_id, amount, currency, status, expires_at, created_at, released_at, settled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

	applied, err := r.session.Query(query,
		toUUID(reservation.ReservationID),
		toUUID(reservation.AccountID),
		toUUID(reservation.TransactionID),
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
	var reservationID, accountID, transactionID gocql.UUID
	var status string

	query := `SELECT reservation_id, account_id, transaction_id, amount, currency, status, expires_at, created_at, released_at, settled_at
		FROM reservations WHERE reservation_id = ?`

	err := r.session.Query(query, toUUID(id)).WithContext(ctx).Scan(
		&reservationID,
		&accountID,
		&transactionID,
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

	res.ReservationID = uuid.UUID(reservationID)
	res.AccountID = uuid.UUID(accountID)
	res.TransactionID = uuid.UUID(transactionID)
	res.Status = domain.ReservationStatus(status)
	return &res, nil
}

func (r *cassandraLedgerRepository) GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error) {
	var reservations []*domain.Reservation

	query := `SELECT reservation_id, account_id, transaction_id, amount, currency, status, expires_at, created_at, released_at, settled_at
		FROM reservations WHERE account_id = ? AND status = 'ACTIVE' ALLOW FILTERING`

	iter := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Iter()
	defer iter.Close()

	var reservationID, accountIDCol, transactionID gocql.UUID
	var status string
	var amount int64
	var currency string
	var expiresAt, createdAt time.Time
	var releasedAt, settledAt *time.Time

	for iter.Scan(
		&reservationID,
		&accountIDCol,
		&transactionID,
		&amount,
		&currency,
		&status,
		&expiresAt,
		&createdAt,
		&releasedAt,
		&settledAt,
	) {
		res := &domain.Reservation{
			ReservationID: uuid.UUID(reservationID),
			AccountID:     uuid.UUID(accountIDCol),
			TransactionID: uuid.UUID(transactionID),
			Amount:        amount,
			Currency:      currency,
			Status:        domain.ReservationStatus(status),
			ExpiresAt:     expiresAt,
			CreatedAt:     createdAt,
			ReleasedAt:    releasedAt,
			SettledAt:     settledAt,
		}
		reservations = append(reservations, res)
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
		applied, updateErr := r.session.Query(query, string(status), toUUID(id)).WithContext(ctx).ScanCAS()
		err = updateErr
		if !applied && err == nil {
			return domain.ErrReservationNotActive
		}
	} else {
		applied, updateErr := r.session.Query(query, string(status), now, toUUID(id)).WithContext(ctx).ScanCAS()
		err = updateErr
		if !applied && err == nil {
			return domain.ErrReservationNotActive
		}
	}

	return err
}

func (r *cassandraLedgerRepository) SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT SUM(amount) FROM ledger_entries WHERE account_id = ? AND entry_type = 'DEBIT' ALLOW FILTERING`
	var total int64
	err := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Scan(&total)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return total, err
}

func (r *cassandraLedgerRepository) SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT SUM(amount) FROM ledger_entries WHERE account_id = ? AND entry_type = 'CREDIT' ALLOW FILTERING`
	var total int64
	err := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Scan(&total)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return total, err
}

func (r *cassandraLedgerRepository) GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT balance_after FROM ledger_entries WHERE account_id = ? LIMIT 1`
	var balance int64
	err := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Scan(&balance)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return balance, err
}

func (r *cassandraLedgerRepository) SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `SELECT SUM(amount) FROM reservations WHERE account_id = ? AND status = 'ACTIVE' ALLOW FILTERING`
	var total int64
	err := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Scan(&total)
	if err == gocql.ErrNotFound {
		return 0, nil
	}
	return total, err
}

func (r *cassandraLedgerRepository) UpsertNote(ctx context.Context, note *domain.TransactionNote) error {
	query := `INSERT INTO transaction_notes (entry_id, user_id, note, updated_at) VALUES (?, ?, ?, ?)`
	return r.session.Query(query,
		toUUID(note.EntryID),
		toUUID(note.UserID),
		note.Note,
		note.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) GetNote(ctx context.Context, entryID uuid.UUID) (*domain.TransactionNote, error) {
	var note domain.TransactionNote
	var fetchedEntryID, userID gocql.UUID
	query := `SELECT entry_id, user_id, note, updated_at FROM transaction_notes WHERE entry_id = ?`
	err := r.session.Query(query, toUUID(entryID)).WithContext(ctx).Scan(&fetchedEntryID, &userID, &note.Note, &note.UpdatedAt)
	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	note.EntryID = uuid.UUID(fetchedEntryID)
	note.UserID = uuid.UUID(userID)
	return &note, nil
}

// IsAccountLocked reads lockdown_enabled straight from the accounts row. The
// accounts table lives in the same keyspace so no cross-service call is
// needed; legacy rows without the column read as false (Cassandra returns
// NULL -> false for missing non-key columns).
func (r *cassandraLedgerRepository) IsAccountLocked(ctx context.Context, accountID uuid.UUID) (bool, error) {
	var locked bool
	query := `SELECT lockdown_enabled FROM accounts WHERE account_id = ?`
	err := r.session.Query(query, toUUID(accountID)).WithContext(ctx).Scan(&locked)
	if err == gocql.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return locked, nil
}

func (r *cassandraLedgerRepository) GetEntryByID(ctx context.Context, entryID uuid.UUID) (*domain.LedgerEntry, error) {
	// Entries are partitioned by account_id; a global entry-id index would be
	// the production answer. At dev scale a partition scan is correct and
	// simple: find the entry, then return it via the standard scanner.
	iter := r.session.Query(`SELECT ` + fullEntryCols + ` FROM ledger_entries WHERE entry_id = ? ALLOW FILTERING`, toUUID(entryID)).WithContext(ctx).Iter()
	defer iter.Close()
	entries, err := scanEntries(iter)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, domain.ErrTransactionNotFound
	}
	return entries[0], nil
}

// GetAccountOwner resolves which user owns an account by reading the accounts
// table in the shared keyspace. Ownership checks on ledger reads keep other
// users' transaction history private without an extra network hop.
func (r *cassandraLedgerRepository) GetAccountOwner(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error) {
	var userID gocql.UUID
	err := r.session.Query(`SELECT user_id FROM accounts WHERE account_id = ?`, toUUID(accountID)).WithContext(ctx).Scan(&userID)
	if err == gocql.ErrNotFound {
		return uuid.Nil, domain.ErrAccountNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.UUID(userID), nil
}

// ── Ledger invariant monitor ────────────────────────────────────────────────

func (r *cassandraLedgerRepository) ListEntryAccounts(ctx context.Context) ([]uuid.UUID, error) {
	iter := r.session.Query(`SELECT DISTINCT account_id FROM ledger_entries`).WithContext(ctx).Iter()
	defer iter.Close()
	out := make([]uuid.UUID, 0)
	var accountIDCol gocql.UUID
	for iter.Scan(&accountIDCol) {
		out = append(out, uuid.UUID(accountIDCol))
	}
	return out, iter.Close()
}

func (r *cassandraLedgerRepository) GetIntegrityGuard(ctx context.Context, accountID uuid.UUID) (*domain.IntegrityGuard, error) {
	var frozen bool
	var reason string
	var incidentIDCol *gocql.UUID
	var detectedAt time.Time
	err := r.session.Query(`SELECT frozen, reason, incident_id, detected_at FROM ledger_integrity_guards WHERE account_id = ?`, toUUID(accountID)).WithContext(ctx).Scan(&frozen, &reason, &incidentIDCol, &detectedAt)
	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g := &domain.IntegrityGuard{
		AccountID:  accountID,
		Frozen:     frozen,
		Reason:     reason,
		DetectedAt: detectedAt,
	}
	if incidentIDCol != nil {
		g.IncidentID = uuid.UUID(*incidentIDCol)
	}
	return g, nil
}

func (r *cassandraLedgerRepository) SetIntegrityGuard(ctx context.Context, g *domain.IntegrityGuard) error {
	query := `INSERT INTO ledger_integrity_guards (account_id, frozen, reason, incident_id, detected_at) VALUES (?, ?, ?, ?, ?)`
	return r.session.Query(query, toUUID(g.AccountID), g.Frozen, g.Reason, toUUID(g.IncidentID), g.DetectedAt).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) ClearIntegrityGuard(ctx context.Context, accountID uuid.UUID) error {
	return r.session.Query(`DELETE FROM ledger_integrity_guards WHERE account_id = ?`, toUUID(accountID)).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) InsertIntegrityEvent(ctx context.Context, ev *domain.IntegrityEvent) error {
	query := `INSERT INTO ledger_integrity_events (account_id, event_id, event_type, message, detail, detected_at, cleared_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		toUUID(ev.AccountID),
		toUUID(ev.EventID),
		string(ev.EventType),
		ev.Message,
		ev.Detail,
		ev.DetectedAt,
		ev.ClearedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraLedgerRepository) ListIntegrityEvents(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.IntegrityEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	iter := r.session.Query(`SELECT account_id, event_id, event_type, message, detail, detected_at, cleared_at
		FROM ledger_integrity_events WHERE account_id = ? LIMIT ?`, toUUID(accountID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.IntegrityEvent, 0)
	var accountIDCol, eventIDCol gocql.UUID
	var eventType, message, detail string
	var detectedAt time.Time
	var clearedAt *time.Time
	for iter.Scan(&accountIDCol, &eventIDCol, &eventType, &message, &detail, &detectedAt, &clearedAt) {
		out = append(out, &domain.IntegrityEvent{
			AccountID:  uuid.UUID(accountIDCol),
			EventID:    uuid.UUID(eventIDCol),
			EventType:  domain.IntegrityEventType(eventType),
			Message:    message,
			Detail:     detail,
			DetectedAt: detectedAt,
			ClearedAt:  clearedAt,
		})
	}
	return out, iter.Close()
}

func (r *cassandraLedgerRepository) MarkPaymentBooked(ctx context.Context, paymentID uuid.UUID, idempotencyKey, eventType string) (bool, error) {
	query := `INSERT INTO ledger_payment_marks (payment_id, idempotency_key, event_type, booked_at)
		VALUES (?, ?, ?, ?) IF NOT EXISTS`
	// On conflict the LWT returns [applied] plus the full existing row; nil dests
	// skip the payload columns — we only need the applied flag.
	applied, err := r.session.Query(query, toUUID(paymentID), idempotencyKey, eventType, time.Now().UTC()).WithContext(ctx).ScanCAS(nil, nil, nil, nil)
	if err != nil {
		return false, fmt.Errorf("claiming payment booking: %w", err)
	}
	return applied, nil
}
