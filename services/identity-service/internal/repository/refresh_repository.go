package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// RefreshTokenRecord is the stored form of a refresh token: only its SHA-256
// digest is persisted, so a DB leak never exposes usable credentials.
type RefreshTokenRecord struct {
	TokenHash string    `json:"token_hash"`
	UserID    uuid.UUID `json:"user_id"`
	DeviceID  string    `json:"device_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

type RefreshTokenRepository interface {
	Save(ctx context.Context, rec *RefreshTokenRecord) error
	GetByHash(ctx context.Context, tokenHash string) (*RefreshTokenRecord, error)
	Revoke(ctx context.Context, tokenHash string) error
	RevokeAllForUserAndDevice(ctx context.Context, userID uuid.UUID, deviceID string) error
}

type cassandraRefreshTokenRepository struct {
	session *gocql.Session
}

func NewCassandraRefreshTokenRepository(session *gocql.Session) RefreshTokenRepository {
	return &cassandraRefreshTokenRepository{session: session}
}

func (r *cassandraRefreshTokenRepository) Save(ctx context.Context, rec *RefreshTokenRecord) error {
	query := `INSERT INTO refresh_tokens (token_hash, user_id, device_id, expires_at, revoked, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		rec.TokenHash,
		gocql.UUID(rec.UserID),
		rec.DeviceID,
		rec.ExpiresAt,
		rec.Revoked,
		rec.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRefreshTokenRepository) GetByHash(ctx context.Context, tokenHash string) (*RefreshTokenRecord, error) {
	var rec RefreshTokenRecord
	var userIDCol gocql.UUID
	query := `SELECT token_hash, user_id, device_id, expires_at, revoked, created_at
		FROM refresh_tokens WHERE token_hash = ?`
	err := r.session.Query(query, tokenHash).WithContext(ctx).Scan(
		&rec.TokenHash,
		&userIDCol,
		&rec.DeviceID,
		&rec.ExpiresAt,
		&rec.Revoked,
		&rec.CreatedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.UserID = uuid.UUID(userIDCol)
	return &rec, nil
}

func (r *cassandraRefreshTokenRepository) Revoke(ctx context.Context, tokenHash string) error {
	query := `UPDATE refresh_tokens SET revoked = true WHERE token_hash = ?`
	return r.session.Query(query, tokenHash).WithContext(ctx).Exec()
}

func (r *cassandraRefreshTokenRepository) RevokeAllForUserAndDevice(ctx context.Context, userID uuid.UUID, deviceID string) error {
	// Scan the digests first, then revoke each row: Cassandra has no
	// update-by-index, so token_hash must be known to mutate the partition.
	iter := r.session.Query(
		`SELECT token_hash FROM refresh_tokens WHERE user_id = ? AND device_id = ? ALLOW FILTERING`,
		gocql.UUID(userID), deviceID,
	).WithContext(ctx).Iter()
	defer iter.Close()

	var hashes []string
	var h string
	for iter.Scan(&h) {
		hashes = append(hashes, h)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	for _, hash := range hashes {
		if err := r.Revoke(ctx, hash); err != nil {
			return err
		}
	}
	return nil
}
