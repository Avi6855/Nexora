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
	batch := gocql.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	batch.Query(`INSERT INTO users (user_id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		gocql.UUID(user.UserID), user.Email, user.Phone, user.PasswordHash,
		user.FirstName, user.LastName, string(user.Status),
		user.EmailVerified, user.PhoneVerified, user.CreatedAt, user.UpdatedAt)

	batch.Query(`INSERT INTO users_by_email (email, user_id, created_at) VALUES (?, ?, ?)`,
		user.Email, gocql.UUID(user.UserID), user.CreatedAt)

	if user.Phone != "" {
		batch.Query(`INSERT INTO users_by_phone (phone, user_id, created_at) VALUES (?, ?, ?)`,
			user.Phone, gocql.UUID(user.UserID), user.CreatedAt)
	}

	return r.session.ExecuteBatch(batch)
}

func (r *cassandraUserRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	var status string
	var userID gocql.UUID

	query := `SELECT user_id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified, created_at, updated_at
		FROM users WHERE user_id = ?`

	err := r.session.Query(query, gocql.UUID(id)).WithContext(ctx).Scan(
		&userID, &user.Email, &user.Phone, &user.PasswordHash,
		&user.FirstName, &user.LastName, &status,
		&user.EmailVerified, &user.PhoneVerified, &user.CreatedAt, &user.UpdatedAt,
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
	var userID gocql.UUID

	err := r.session.Query(`SELECT user_id FROM users_by_email WHERE email = ?`, email).
		WithContext(ctx).Scan(&userID)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, uuid.UUID(userID))
}

func (r *cassandraUserRepository) GetByPhone(ctx context.Context, phone string) (*domain.User, error) {
	var userID gocql.UUID

	err := r.session.Query(`SELECT user_id FROM users_by_phone WHERE phone = ?`, phone).
		WithContext(ctx).Scan(&userID)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, uuid.UUID(userID))
}

func (r *cassandraUserRepository) Update(ctx context.Context, user *domain.User) error {
	query := `UPDATE users SET first_name = ?, last_name = ?, phone = ?, status = ?, email_verified = ?, phone_verified = ?, updated_at = ?
		WHERE user_id = ?`

	return r.session.Query(query,
		user.FirstName, user.LastName, user.Phone, string(user.Status),
		user.EmailVerified, user.PhoneVerified, user.UpdatedAt,
		gocql.UUID(user.UserID),
	).WithContext(ctx).Exec()
}
