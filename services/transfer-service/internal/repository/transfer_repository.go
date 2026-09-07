package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
)

type TransferRepository interface {
	Create(ctx context.Context, transfer *domain.Transfer) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Transfer, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.Transfer, error)
	GetByFromAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Transfer, error)
	Update(ctx context.Context, transfer *domain.Transfer) error
}

type cassandraTransferRepository struct {
	session *gocql.Session
}

func NewCassandraTransferRepository(session *gocql.Session) TransferRepository {
	return &cassandraTransferRepository{session: session}
}

func (r *cassandraTransferRepository) Create(ctx context.Context, transfer *domain.Transfer) error {
	query := `INSERT INTO transfers (transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		gocql.UUID(transfer.TransferID),
		transfer.IdempotencyKey,
		gocql.UUID(transfer.FromAccountID),
		gocql.UUID(transfer.ToAccountID),
		transfer.Amount,
		transfer.Currency,
		string(transfer.Status),
		transfer.Description,
		transfer.CreatedAt,
		transfer.CompletedAt,
	).WithContext(ctx).Exec()
}

func scanTransfer(iter *gocql.Iter) ([]*domain.Transfer, error) {
	var transfers []*domain.Transfer
	var transfer domain.Transfer
	var transferID, fromAccountID, toAccountID gocql.UUID
	var status string

	for iter.Scan(
		&transferID,
		&transfer.IdempotencyKey,
		&fromAccountID,
		&toAccountID,
		&transfer.Amount,
		&transfer.Currency,
		&status,
		&transfer.Description,
		&transfer.CreatedAt,
		&transfer.CompletedAt,
	) {
		transfer.TransferID = uuid.UUID(transferID)
		transfer.FromAccountID = uuid.UUID(fromAccountID)
		transfer.ToAccountID = uuid.UUID(toAccountID)
		transfer.Status = domain.TransferStatus(status)
		t := transfer
		transfers = append(transfers, &t)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}
	return transfers, nil
}

func (r *cassandraTransferRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Transfer, error) {
	query := `SELECT transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at
		FROM transfers WHERE transfer_id = ?`

	iter := r.session.Query(query, gocql.UUID(id)).WithContext(ctx).Iter()
	defer iter.Close()

	transfers, err := scanTransfer(iter)
	if err != nil {
		return nil, err
	}
	if len(transfers) == 0 {
		return nil, fmt.Errorf("transfer not found")
	}
	return transfers[0], nil
}

func (r *cassandraTransferRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Transfer, error) {
	query := `SELECT transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at
		FROM transfers WHERE idempotency_key = ? ALLOW FILTERING`

	iter := r.session.Query(query, key).WithContext(ctx).Iter()
	defer iter.Close()

	transfers, err := scanTransfer(iter)
	if err != nil {
		return nil, err
	}
	if len(transfers) == 0 {
		return nil, nil
	}
	return transfers[0], nil
}

func (r *cassandraTransferRepository) GetByFromAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Transfer, error) {
	query := `SELECT transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at
		FROM transfers WHERE from_account_id = ?`

	iter := r.session.Query(query, gocql.UUID(accountID)).WithContext(ctx).Iter()
	defer iter.Close()

	return scanTransfer(iter)
}

func (r *cassandraTransferRepository) Update(ctx context.Context, transfer *domain.Transfer) error {
	query := `UPDATE transfers SET status = ?, completed_at = ? WHERE transfer_id = ?`
	return r.session.Query(query, string(transfer.Status), transfer.CompletedAt, gocql.UUID(transfer.TransferID)).WithContext(ctx).Exec()
}
