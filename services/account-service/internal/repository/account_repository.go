package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/account-service/internal/domain"
)

type AccountRepository interface {
	Create(ctx context.Context, account *domain.Account) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Account, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error)
	Update(ctx context.Context, account *domain.Account) error
}

type cassandraAccountRepository struct {
	session *gocql.Session
}

func NewCassandraAccountRepository(session *gocql.Session) AccountRepository {
	return &cassandraAccountRepository{session: session}
}

func (r *cassandraAccountRepository) Create(ctx context.Context, account *domain.Account) error {
	query := `INSERT INTO accounts (account_id, user_id, account_type, currency, available_balance, current_balance, reserved_balance, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		account.AccountID,
		account.UserID,
		string(account.AccountType),
		account.Currency,
		account.AvailableBalance.Amount,
		account.CurrentBalance.Amount,
		account.ReservedBalance.Amount,
		string(account.Status),
		account.CreatedAt,
		account.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraAccountRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	var account domain.Account
	var accountType, status string
	var avail, cur, res int64

	query := `SELECT account_id, user_id, account_type, currency, available_balance, current_balance, reserved_balance, status, created_at, updated_at
		FROM accounts WHERE account_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&account.AccountID,
		&account.UserID,
		&accountType,
		&account.Currency,
		&avail,
		&cur,
		&res,
		&status,
		&account.CreatedAt,
		&account.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("account not found")
	}
	if err != nil {
		return nil, err
	}

	account.AccountType = domain.AccountType(accountType)
	account.Status = domain.AccountStatus(status)
	account.AvailableBalance.Amount = avail
	account.CurrentBalance.Amount = cur
	account.ReservedBalance.Amount = res

	return &account, nil
}

func (r *cassandraAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error) {
	var accounts []*domain.Account

	query := `SELECT account_id, user_id, account_type, currency, available_balance, current_balance, reserved_balance, status, created_at, updated_at
		FROM accounts WHERE user_id = ?`

	iter := r.session.Query(query, userID).WithContext(ctx).Iter()
	defer iter.Close()

	var account domain.Account
	var accountType, status string
	var avail, cur, res int64

	for iter.Scan(
		&account.AccountID,
		&account.UserID,
		&accountType,
		&account.Currency,
		&avail,
		&cur,
		&res,
		&status,
		&account.CreatedAt,
		&account.UpdatedAt,
	) {
		account.AccountType = domain.AccountType(accountType)
		account.Status = domain.AccountStatus(status)
		account.AvailableBalance.Amount = avail
		account.CurrentBalance.Amount = cur
		account.ReservedBalance.Amount = res
		a := account
		accounts = append(accounts, &a)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return accounts, nil
}

func (r *cassandraAccountRepository) Update(ctx context.Context, account *domain.Account) error {
	query := `UPDATE accounts SET available_balance = ?, current_balance = ?, reserved_balance = ?, status = ?, updated_at = ?
		WHERE account_id = ?`

	return r.session.Query(query,
		account.AvailableBalance.Amount,
		account.CurrentBalance.Amount,
		account.ReservedBalance.Amount,
		string(account.Status),
		account.UpdatedAt,
		account.AccountID,
	).WithContext(ctx).Exec()
}
