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
	MarkUsed(ctx context.Context, otpID uuid.UUID) error
}

type cassandraOTPRepository struct {
	session *gocql.Session
}

func NewCassandraOTPRepository(session *gocql.Session) OTPRepository {
	return &cassandraOTPRepository{session: session}
}

func (r *cassandraOTPRepository) Create(ctx context.Context, otp *domain.OTP) error {
	query := `INSERT INTO otps (user_id, code, purpose, created_at, expires_at, used)
		VALUES (?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		otp.UserID,
		otp.Code,
		string(otp.Purpose),
		otp.CreatedAt,
		otp.ExpiresAt,
		otp.Used,
	).WithContext(ctx).Exec()
}

func (r *cassandraOTPRepository) GetLatestByUserAndPurpose(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose) (*domain.OTP, error) {
	var otp domain.OTP

	query := `SELECT user_id, code, purpose, created_at, expires_at, used
		FROM otps WHERE user_id = ? AND purpose = ? LIMIT 1`

	err := r.session.Query(query, userID, string(purpose)).WithContext(ctx).Scan(
		&otp.UserID,
		&otp.Code,
		&otp.Purpose,
		&otp.CreatedAt,
		&otp.ExpiresAt,
		&otp.Used,
	)

	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &otp, nil
}

func (r *cassandraOTPRepository) MarkUsed(ctx context.Context, otpID uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE otps SET used = true WHERE user_id = ? AND created_at = ?`
	return r.session.Query(query, otpID, now).WithContext(ctx).Exec()
}
