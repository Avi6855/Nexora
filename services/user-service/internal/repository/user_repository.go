package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/user-service/internal/domain"
)

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
}

type cassandraUserRepository struct {
	session *gocql.Session
}

func NewCassandraUserRepository(session *gocql.Session) UserRepository {
	return &cassandraUserRepository{session: session}
}

func (r *cassandraUserRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	var status string
	var userID gocql.UUID

	query := `SELECT user_id, email, phone, first_name, last_name, status, created_at, updated_at
		FROM users WHERE user_id = ?`

	err := r.session.Query(query, gocql.UUID(id)).WithContext(ctx).Scan(
		&userID,
		&user.Email,
		&user.Phone,
		&user.FirstName,
		&user.LastName,
		&status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	user.UserID = uuid.UUID(userID)
	user.Status = domain.UserStatus(status)
	return &user, nil
}

func (r *cassandraUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	var status string
	var userID gocql.UUID

	query := `SELECT user_id, email, phone, first_name, last_name, status, created_at, updated_at
		FROM users WHERE email = ? ALLOW FILTERING`

	err := r.session.Query(query, email).WithContext(ctx).Scan(
		&userID,
		&user.Email,
		&user.Phone,
		&user.FirstName,
		&user.LastName,
		&status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	user.UserID = uuid.UUID(userID)
	user.Status = domain.UserStatus(status)
	return &user, nil
}

func (r *cassandraUserRepository) Update(ctx context.Context, user *domain.User) error {
	query := `UPDATE users SET first_name = ?, last_name = ?, phone = ?, updated_at = ?
		WHERE user_id = ?`

	return r.session.Query(query,
		user.FirstName,
		user.LastName,
		user.Phone,
		user.UpdatedAt,
		gocql.UUID(user.UserID),
	).WithContext(ctx).Exec()
}
