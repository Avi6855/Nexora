package repository

import (
	"context"
	"fmt"
	"time"

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
	query := `INSERT INTO accounts (account_id, user_id, account_type, currency, available_balance, current_balance, reserved_balance, status, lockdown_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		gocql.UUID(account.AccountID),
		gocql.UUID(account.UserID),
		string(account.AccountType),
		account.Currency,
		account.AvailableBalance.Amount,
		account.CurrentBalance.Amount,
		account.ReservedBalance.Amount,
		string(account.Status),
		account.LockdownEnabled,
		account.CreatedAt,
		account.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraAccountRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	var account domain.Account
	var accountID, userID gocql.UUID
	var accountType, status string
	var avail, cur, res int64
	var lockdown bool

	query := `SELECT account_id, user_id, account_type, currency, available_balance, current_balance, reserved_balance, status, lockdown_enabled, created_at, updated_at
		FROM accounts WHERE account_id = ?`

	err := r.session.Query(query, gocql.UUID(id)).WithContext(ctx).Scan(
		&accountID,
		&userID,
		&accountType,
		&account.Currency,
		&avail,
		&cur,
		&res,
		&status,
		&lockdown,
		&account.CreatedAt,
		&account.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("account not found")
	}
	if err != nil {
		return nil, err
	}

	account.AccountID = uuid.UUID(accountID)
	account.UserID = uuid.UUID(userID)
	account.AccountType = domain.AccountType(accountType)
	account.Status = domain.AccountStatus(status)
	account.LockdownEnabled = lockdown
	account.AvailableBalance.Amount = avail
	account.AvailableBalance.Currency = account.Currency
	account.CurrentBalance.Amount = cur
	account.CurrentBalance.Currency = account.Currency
	account.ReservedBalance.Amount = res
	account.ReservedBalance.Currency = account.Currency

	return &account, nil
}

func (r *cassandraAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error) {
	accounts := make([]*domain.Account, 0)

	query := `SELECT account_id, user_id, account_type, currency, available_balance, current_balance, reserved_balance, status, lockdown_enabled, created_at, updated_at
		FROM accounts WHERE user_id = ? ALLOW FILTERING`

	iter := r.session.Query(query, gocql.UUID(userID)).WithContext(ctx).Iter()
	defer iter.Close()

	var accountID, userIDCol gocql.UUID
	var accountType, currency, status string
	var avail, cur, res int64
	var lockdown bool
	var createdAt, updatedAt time.Time

	for iter.Scan(
		&accountID,
		&userIDCol,
		&accountType,
		&currency,
		&avail,
		&cur,
		&res,
		&status,
		&lockdown,
		&createdAt,
		&updatedAt,
	) {
		account := &domain.Account{
			AccountID:       uuid.UUID(accountID),
			UserID:          uuid.UUID(userIDCol),
			Currency:        currency,
			LockdownEnabled: lockdown,
			CreatedAt:       createdAt,
			UpdatedAt:       updatedAt,
		}
		account.AccountType = domain.AccountType(accountType)
		account.Status = domain.AccountStatus(status)
		account.AvailableBalance.Amount = avail
		account.AvailableBalance.Currency = currency
		account.CurrentBalance.Amount = cur
		account.CurrentBalance.Currency = currency
		account.ReservedBalance.Amount = res
		account.ReservedBalance.Currency = currency
		accounts = append(accounts, account)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return accounts, nil
}

func (r *cassandraAccountRepository) Update(ctx context.Context, account *domain.Account) error {
	query := `UPDATE accounts SET available_balance = ?, current_balance = ?, reserved_balance = ?, status = ?, lockdown_enabled = ?, updated_at = ?
		WHERE account_id = ?`

	return r.session.Query(query,
		account.AvailableBalance.Amount,
		account.CurrentBalance.Amount,
		account.ReservedBalance.Amount,
		string(account.Status),
		account.LockdownEnabled,
		account.UpdatedAt,
		gocql.UUID(account.AccountID),
	).WithContext(ctx).Exec()
}
