package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/payment-service/internal/domain"
)

type PaymentRepository interface {
	Create(ctx context.Context, payment *domain.Payment) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error)
	GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error)
	Update(ctx context.Context, payment *domain.Payment) error
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
		payment.PaymentID,
		payment.IdempotencyKey,
		payment.AccountID,
		payment.UserID,
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

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE payment_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&payment.PaymentID,
		&payment.IdempotencyKey,
		&payment.AccountID,
		&payment.UserID,
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

	payment.PaymentType = domain.PaymentType(paymentType)
	payment.State = domain.PaymentState(state)
	return &payment, nil
}

func (r *cassandraPaymentRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error) {
	var payment domain.Payment
	var paymentType, state string

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE idempotency_key = ? ALLOW FILTERING`

	err := r.session.Query(query, key).WithContext(ctx).Scan(
		&payment.PaymentID,
		&payment.IdempotencyKey,
		&payment.AccountID,
		&payment.UserID,
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

	payment.PaymentType = domain.PaymentType(paymentType)
	payment.State = domain.PaymentState(state)
	return &payment, nil
}

func (r *cassandraPaymentRepository) GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error) {
	var payments []*domain.Payment

	query := `SELECT payment_id, idempotency_key, account_id, user_id, payment_type, amount, currency, status, failure_reason, counterparty_id, counterparty_name, reference, ledger_transaction_id, reservation_id, fraud_score, fraud_action, created_at, updated_at, authorized_at, settled_at
		FROM payments WHERE account_id = ?`

	iter := r.session.Query(query, accountID).WithContext(ctx).Iter()
	defer iter.Close()

	var payment domain.Payment
	var paymentType, state string

	for iter.Scan(
		&payment.PaymentID,
		&payment.IdempotencyKey,
		&payment.AccountID,
		&payment.UserID,
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
		payment.PaymentID,
	).WithContext(ctx).Exec()
}
