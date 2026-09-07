package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/shared/cassandra"
	"github.com/nexora/nexora/shared/outbox"
)

type PaymentRepository interface {
	Create(ctx context.Context, payment *domain.Payment) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error)
	GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Payment, error)
	Update(ctx context.Context, payment *domain.Payment) error
}

// AtomicEventWriter is implemented by repositories that can persist a state
// transition together with its outbox events in a single atomic operation.
// This is the transactional-outbox dual-write elimination (ADR-005): the
// aggregate can never change without its events being durably recorded.
//
// The payment row and the outbox rows are written in ONE logged Cassandra
// batch, so either all writes commit or none do. This is a cross-partition
// logged batch (payments is keyed by payment_id, outbox_events by event_id),
// which Cassandra supports — at lower throughput than a same-partition batch,
// an acceptable trade for correctness in a financial system.
type AtomicEventWriter interface {
	CreateWithEvents(ctx context.Context, payment *domain.Payment, events []*outbox.Event) error
	UpdateWithEvents(ctx context.Context, payment *domain.Payment, events []*outbox.Event) error
}

type cassandraPaymentRepository struct {
	session *gocql.Session
}

func NewCassandraPaymentRepository(session *gocql.Session) PaymentRepository {
	return &cassandraPaymentRepository{session: session}
}

func (r *cassandraPaymentRepository) Create(ctx context.Context, payment *domain.Payment) error {
	query := `INSERT INTO payments (payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, metadata, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		cassandra.UID(payment.PaymentID),
		payment.IdempotencyKey,
		cassandra.UID(payment.AccountID),
		cassandra.UIDOrNil(payment.UserID),
		string(payment.PaymentType),
		payment.Amount,
		payment.Currency,
		string(payment.State),
		payment.FailureReason,
		payment.CounterpartyID,
		payment.CounterpartyName,
		payment.Reference,
		payment.LedgerTransactionID,
		payment.ReservationID,
		payment.Metadata,
		payment.FraudScore,
		payment.FraudAction,
		payment.CreatedAt,
		payment.UpdatedAt,
		payment.AuthorizedAt,
		payment.SettledAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraPaymentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	var payment domain.Payment
	var paymentType, state string
	// gocql cannot scan CQL uuid into google/uuid.UUID; scan into gocql.UUID
	// (both are [16]byte) and convert (see shared/cassandra).
	var pid, aid, uid gocql.UUID

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE payment_id = ?`

	err := r.session.Query(query, cassandra.UID(id)).WithContext(ctx).Scan(
		&pid,
		&payment.IdempotencyKey,
		&aid,
		&uid,
		&paymentType,
		&payment.Amount,
		&payment.Currency,
		&state,
		&payment.FailureReason,
		&payment.CounterpartyID,
		&payment.CounterpartyName,
		&payment.Reference,
		&payment.LedgerTransactionID,
		&payment.ReservationID,
		&payment.FraudScore,
		&payment.FraudAction,
		&payment.CreatedAt,
		&payment.UpdatedAt,
		&payment.AuthorizedAt,
		&payment.SettledAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("payment not found")
	}
	if err != nil {
		return nil, err
	}

	payment.PaymentID = uuid.UUID(pid)
	payment.AccountID = uuid.UUID(aid)
	payment.UserID = uuid.UUID(uid)
	payment.PaymentType = domain.PaymentType(paymentType)
	payment.State = domain.PaymentState(state)
	return &payment, nil
}

func (r *cassandraPaymentRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error) {
	var payment domain.Payment
	var paymentType, state string
	var pid, aid, uid gocql.UUID

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE idempotency_key = ? ALLOW FILTERING`

	err := r.session.Query(query, key).WithContext(ctx).Scan(
		&pid,
		&payment.IdempotencyKey,
		&aid,
		&uid,
		&paymentType,
		&payment.Amount,
		&payment.Currency,
		&state,
		&payment.FailureReason,
		&payment.CounterpartyID,
		&payment.CounterpartyName,
		&payment.Reference,
		&payment.LedgerTransactionID,
		&payment.ReservationID,
		&payment.FraudScore,
		&payment.FraudAction,
		&payment.CreatedAt,
		&payment.UpdatedAt,
		&payment.AuthorizedAt,
		&payment.SettledAt,
	)

	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	payment.PaymentID = uuid.UUID(pid)
	payment.AccountID = uuid.UUID(aid)
	payment.UserID = uuid.UUID(uid)
	payment.PaymentType = domain.PaymentType(paymentType)
	payment.State = domain.PaymentState(state)
	return &payment, nil
}

func (r *cassandraPaymentRepository) GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error) {
	var payments []*domain.Payment

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE account_id = ?`

	iter := r.session.Query(query, cassandra.UID(accountID)).WithContext(ctx).Iter()
	defer iter.Close()

	var payment domain.Payment
	var paymentType, state string
	var pid, aid, uid gocql.UUID

	for iter.Scan(
		&pid,
		&payment.IdempotencyKey,
		&aid,
		&uid,
		&paymentType,
		&payment.Amount,
		&payment.Currency,
		&state,
		&payment.FailureReason,
		&payment.CounterpartyID,
		&payment.CounterpartyName,
		&payment.Reference,
		&payment.LedgerTransactionID,
		&payment.ReservationID,
		&payment.FraudScore,
		&payment.FraudAction,
		&payment.CreatedAt,
		&payment.UpdatedAt,
		&payment.AuthorizedAt,
		&payment.SettledAt,
	) {
		payment.PaymentID = uuid.UUID(pid)
		payment.AccountID = uuid.UUID(aid)
		payment.UserID = uuid.UUID(uid)
		payment.PaymentType = domain.PaymentType(paymentType)
		payment.State = domain.PaymentState(state)
		p := payment
		payments = append(payments, &p)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return payments, nil
}

func (r *cassandraPaymentRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Payment, error) {
	var payments []*domain.Payment

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE user_id = ? ALLOW FILTERING`

	iter := r.session.Query(query, cassandra.UID(userID)).WithContext(ctx).Iter()
	defer iter.Close()

	var payment domain.Payment
	var paymentType, state string
	var pid, aid, uid gocql.UUID

	for iter.Scan(
		&pid,
		&payment.IdempotencyKey,
		&aid,
		&uid,
		&paymentType,
		&payment.Amount,
		&payment.Currency,
		&state,
		&payment.FailureReason,
		&payment.CounterpartyID,
		&payment.CounterpartyName,
		&payment.Reference,
		&payment.LedgerTransactionID,
		&payment.ReservationID,
		&payment.FraudScore,
		&payment.FraudAction,
		&payment.CreatedAt,
		&payment.UpdatedAt,
		&payment.AuthorizedAt,
		&payment.SettledAt,
	) {
		payment.PaymentID = uuid.UUID(pid)
		payment.AccountID = uuid.UUID(aid)
		payment.UserID = uuid.UUID(uid)
		payment.PaymentType = domain.PaymentType(paymentType)
		payment.State = domain.PaymentState(state)
		p := payment
		payments = append(payments, &p)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return payments, nil
}

func (r *cassandraPaymentRepository) Update(ctx context.Context, payment *domain.Payment) error {
	query := `UPDATE payments SET status = ?, failure_reason = ?, fraud_score = ?, fraud_action = ?, ledger_transaction_id = ?, reservation_id = ?, updated_at = ?, authorized_at = ?, settled_at = ?
		WHERE payment_id = ?`

	return r.session.Query(query,
		string(payment.State),
		payment.FailureReason,
		payment.FraudScore,
		payment.FraudAction,
		payment.LedgerTransactionID,
		payment.ReservationID,
		payment.UpdatedAt,
		payment.AuthorizedAt,
		payment.SettledAt,
		cassandra.UID(payment.PaymentID),
	).WithContext(ctx).Exec()
}

// ── Atomic dual-write (transactional outbox) ────────────────────────────────

const outboxInsert = `INSERT INTO outbox_events (event_id, aggregate_id, event_type, topic, correlation_id, causation_id, producer, payload, status, attempts, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (r *cassandraPaymentRepository) CreateWithEvents(ctx context.Context, payment *domain.Payment, events []*outbox.Event) error {
	batch := r.session.NewBatch(gocql.LoggedBatch)
	batch.Query(`INSERT INTO payments (payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, metadata, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cassandra.UID(payment.PaymentID),
		payment.IdempotencyKey,
		cassandra.UID(payment.AccountID),
		cassandra.UIDOrNil(payment.UserID),
		string(payment.PaymentType),
		payment.Amount,
		payment.Currency,
		string(payment.State),
		payment.FailureReason,
		payment.CounterpartyID,
		payment.CounterpartyName,
		payment.Reference,
		payment.LedgerTransactionID,
		payment.ReservationID,
		payment.Metadata,
		payment.FraudScore,
		payment.FraudAction,
		payment.CreatedAt,
		payment.UpdatedAt,
		payment.AuthorizedAt,
		payment.SettledAt,
	)
	addOutboxQueries(batch, events)
	return r.session.ExecuteBatch(batch)
}

func (r *cassandraPaymentRepository) UpdateWithEvents(ctx context.Context, payment *domain.Payment, events []*outbox.Event) error {
	batch := r.session.NewBatch(gocql.LoggedBatch)
	batch.Query(`UPDATE payments SET status = ?, failure_reason = ?, fraud_score = ?, fraud_action = ?, ledger_transaction_id = ?, reservation_id = ?, updated_at = ?, authorized_at = ?, settled_at = ?
		WHERE payment_id = ?`,
		string(payment.State),
		payment.FailureReason,
		payment.FraudScore,
		payment.FraudAction,
		payment.LedgerTransactionID,
		payment.ReservationID,
		payment.UpdatedAt,
		payment.AuthorizedAt,
		payment.SettledAt,
		cassandra.UID(payment.PaymentID),
	)
	addOutboxQueries(batch, events)
	return r.session.ExecuteBatch(batch)
}

func addOutboxQueries(batch *gocql.Batch, events []*outbox.Event) {
	for _, e := range events {
		batch.Query(outboxInsert,
			cassandra.UID(e.EventID),
			e.AggregateID,
			e.EventType,
			e.Topic,
			e.CorrelationID,
			e.CausationID,
			e.Producer,
			e.Payload,
			string(outbox.StatusPending),
			e.Attempts,
			e.CreatedAt,
		)
	}
}
