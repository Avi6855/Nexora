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
		transfer.TransferID,
		transfer.IdempotencyKey,
		transfer.FromAccountID,
		transfer.ToAccountID,
		transfer.Amount,
		transfer.Currency,
		string(transfer.Status),
		transfer.Description,
		transfer.CreatedAt,
		transfer.CompletedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraTransferRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Transfer, error) {
	var transfer domain.Transfer
	var status string

	query := `SELECT transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at
		FROM transfers WHERE transfer_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&transfer.TransferID,
		&transfer.IdempotencyKey,
		&transfer.FromAccountID,
		&transfer.ToAccountID,
		&transfer.Amount,
		&transfer.Currency,
		&status,
		&transfer.Description,
		&transfer.CreatedAt,
		&transfer.CompletedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("transfer not found")
	}
	if err != nil {
		return nil, err
	}

	transfer.Status = domain.TransferStatus(status)
	return &transfer, nil
}

func (r *cassandraTransferRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Transfer, error) {
	var transfer domain.Transfer
	var status string

	query := `SELECT transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at
		FROM transfers WHERE idempotency_key = ? ALLOW FILTERING`

	err := r.session.Query(query, key).WithContext(ctx).Scan(
		&transfer.TransferID,
		&transfer.IdempotencyKey,
		&transfer.FromAccountID,
		&transfer.ToAccountID,
		&transfer.Amount,
		&transfer.Currency,
		&status,
		&transfer.Description,
		&transfer.CreatedAt,
		&transfer.CompletedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	transfer.Status = domain.TransferStatus(status)
	return &transfer, nil
}

func (r *cassandraTransferRepository) GetByFromAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Transfer, error) {
	var transfers []*domain.Transfer

	query := `SELECT transfer_id, idempotency_key, from_account_id, to_account_id, amount, currency, status, description, created_at, completed_at
		FROM transfers WHERE from_account_id = ?`

	iter := r.session.Query(query, accountID).WithContext(ctx).Iter()
	defer iter.Close()

	var transfer domain.Transfer
	var status string

	for iter.Scan(
		&transfer.TransferID,
		&transfer.IdempotencyKey,
		&transfer.FromAccountID,
		&transfer.ToAccountID,
		&transfer.Amount,
		&transfer.Currency,
		&status,
		&transfer.Description,
		&transfer.CreatedAt,
		&transfer.CompletedAt,
	) {
		transfer.Status = domain.TransferStatus(status)
		t := transfer
		transfers = append(transfers, &t)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return transfers, nil
}

func (r *cassandraTransferRepository) Update(ctx context.Context, transfer *domain.Transfer) error {
	query := `UPDATE transfers SET status = ?, completed_at = ? WHERE transfer_id = ?`
	return r.session.Query(query, string(transfer.Status), transfer.CompletedAt, transfer.TransferID).WithContext(ctx).Exec()
}
