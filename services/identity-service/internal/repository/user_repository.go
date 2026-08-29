package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/identity-service/internal/domain"
)

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByPhone(ctx context.Context, phone string) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
}

type cassandraUserRepository struct {
	session *gocql.Session
}

func NewCassandraUserRepository(session *gocql.Session) UserRepository {
	return &cassandraUserRepository{session: session}
}

func (r *cassandraUserRepository) Create(ctx context.Context, user *domain.User) error {
	query := `INSERT INTO users (user_id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	
	return r.session.Query(query,
		user.UserID,
		user.Email,
		user.Phone,
		user.PasswordHash,
		user.FirstName,
		user.LastName,
		string(user.Status),
		user.EmailVerified,
		user.PhoneVerified,
		user.CreatedAt,
		user.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraUserRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	var status string

	query := `SELECT user_id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified, created_at, updated_at
		FROM users WHERE user_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&user.UserID,
		&user.Email,
		&user.Phone,
		&user.PasswordHash,
		&user.FirstName,
		&user.LastName,
		&status,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	user.Status = domain.UserStatus(status)
	return &user, nil
}

func (r *cassandraUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	var status string

	query := `SELECT user_id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified, created_at, updated_at
		FROM users WHERE email = ? ALLOW FILTERING`

	err := r.session.Query(query, email).WithContext(ctx).Scan(
		&user.UserID,
		&user.Email,
		&user.Phone,
		&user.PasswordHash,
		&user.FirstName,
		&user.LastName,
		&status,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	user.Status = domain.UserStatus(status)
	return &user, nil
}

func (r *cassandraUserRepository) GetByPhone(ctx context.Context, phone string) (*domain.User, error) {
	var user domain.User
	var status string

	query := `SELECT user_id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified, created_at, updated_at
		FROM users WHERE phone = ? ALLOW FILTERING`

	err := r.session.Query(query, phone).WithContext(ctx).Scan(
		&user.UserID,
		&user.Email,
		&user.Phone,
		&user.PasswordHash,
		&user.FirstName,
		&user.LastName,
		&status,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	user.Status = domain.UserStatus(status)
	return &user, nil
}

func (r *cassandraUserRepository) Update(ctx context.Context, user *domain.User) error {
	query := `UPDATE users SET first_name = ?, last_name = ?, phone = ?, status = ?, email_verified = ?, phone_verified = ?, updated_at = ?
		WHERE user_id = ?`

	return r.session.Query(query,
		user.FirstName,
		user.LastName,
		user.Phone,
		string(user.Status),
		user.EmailVerified,
		user.PhoneVerified,
		user.UpdatedAt,
		user.UserID,
	).WithContext(ctx).Exec()
}
