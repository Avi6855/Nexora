package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/identity-service/internal/domain"
)

type OTPRepository interface {
	Create(ctx context.Context, otp *domain.OTP) error
	GetLatestByUserAndPurpose(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose) (*domain.OTP, error)
	MarkUsed(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, createdAt time.Time) error
	IncrementAttempts(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, createdAt time.Time) error
}

type cassandraOTPRepository struct {
	session *gocql.Session
}

func NewCassandraOTPRepository(session *gocql.Session) OTPRepository {
	return &cassandraOTPRepository{session: session}
}

func (r *cassandraOTPRepository) Create(ctx context.Context, otp *domain.OTP) error {
	query := `INSERT INTO otps (user_id, code, purpose, created_at, expires_at, used, attempts)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		gocql.UUID(otp.UserID),
		otp.Code,
		string(otp.Purpose),
		otp.CreatedAt,
		otp.ExpiresAt,
		otp.Used,
		otp.Attempts,
	).WithContext(ctx).Exec()
}

func (r *cassandraOTPRepository) GetLatestByUserAndPurpose(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose) (*domain.OTP, error) {
	var otp domain.OTP
	var userIDCol gocql.UUID
	var purposeStr string

	query := `SELECT user_id, code, purpose, created_at, expires_at, used, attempts
		FROM otps WHERE user_id = ? AND purpose = ? LIMIT 1`

	err := r.session.Query(query, gocql.UUID(userID), string(purpose)).WithContext(ctx).Scan(
		&userIDCol,
		&otp.Code,
		&purposeStr,
		&otp.CreatedAt,
		&otp.ExpiresAt,
		&otp.Used,
		&otp.Attempts,
	)

	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	otp.UserID = uuid.UUID(userIDCol)
	otp.Purpose = domain.OTPPurpose(purposeStr)
	return &otp, nil
}

func (r *cassandraOTPRepository) MarkUsed(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, createdAt time.Time) error {
	query := `UPDATE otps SET used = true WHERE user_id = ? AND purpose = ? AND created_at = ?`
	return r.session.Query(query, gocql.UUID(userID), string(purpose), createdAt).WithContext(ctx).Exec()
}

func (r *cassandraOTPRepository) IncrementAttempts(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose, createdAt time.Time) error {
	query := `UPDATE otps SET attempts = attempts + 1 WHERE user_id = ? AND purpose = ? AND created_at = ?`
	return r.session.Query(query, gocql.UUID(userID), string(purpose), createdAt).WithContext(ctx).Exec()
}
