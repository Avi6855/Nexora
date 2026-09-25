package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/shared/schemes"
)

// SCAChallengeRepository persists PSD2 step-ups.
//
// Every state change goes through a lightweight transaction so the challenge
// is single-use across replicas: the transition is guarded by the attempt
// count the caller computed from, which means a lost race can never overwrite
// a newer outcome (a second answer cannot re-open a completed challenge, and
// two concurrent wrong guesses cannot both write "1 attempt").
type SCAChallengeRepository interface {
	Create(ctx context.Context, ch *domain.SCAChallenge) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.SCAChallenge, error)
	// Transition persists a verification outcome. expectedAttempts is the
	// attempt count the caller read (and computed from); the write applies only
	// if the row still holds PENDING/expectedAttempts, otherwise it is lost
	// (applied == false) and the caller must retry or reload.
	Transition(ctx context.Context, ch *domain.SCAChallenge, expectedAttempts int) (bool, error)

	// ListDueExpiring returns the step-ups whose deadline has passed inside the
	// supplied hourly buckets. Create writes the index entry alongside the
	// challenge, so the sweep is a bounded range read per bucket (no table scan,
	// no ALLOW FILTERING).
	ListDueExpiring(ctx context.Context, buckets []string, now time.Time) ([]domain.SCAExpiring, error)
	// ClearExpiry drops an index entry once its challenge is settled, so a
	// completed or swept step-up is not re-read on every sweep.
	ClearExpiry(ctx context.Context, entry domain.SCAExpiring) error
}

type cassandraSCAChallengeRepository struct {
	session *gocql.Session
}

func NewCassandraSCAChallengeRepository(session *gocql.Session) SCAChallengeRepository {
	return &cassandraSCAChallengeRepository{session: session}
}

// expiryIndexTTL retires index rows on their own: if the sweeper never runs
// (single instance, restart, region outage) the bucket cleans itself up instead
// of growing forever. It comfortably exceeds the challenge TTL.
const expiryIndexTTL = 2 * 60 * 60

func (r *cassandraSCAChallengeRepository) Create(ctx context.Context, ch *domain.SCAChallenge) error {
	query := `INSERT INTO card_sca_challenges (
		challenge_id, authorization_id, card_id, user_id, account_id,
		amount, currency, merchant,
		otp_hash, attempts, max_attempts, status, failure_reason,
		expires_at, created_at, updated_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if err := r.session.Query(query,
		toUUID(ch.ChallengeID),
		toUUID(ch.AuthorizationID),
		toUUID(ch.CardID),
		toUUID(ch.UserID),
		toUUID(ch.AccountID),
		ch.Amount,
		ch.Currency,
		ch.Merchant,
		ch.OTPHash,
		ch.Attempts,
		ch.MaxAttempts,
		string(ch.Status),
		ch.FailureReason,
		ch.ExpiresAt,
		ch.CreatedAt,
		ch.UpdatedAt,
		ch.CompletedAt,
	).WithContext(ctx).Exec(); err != nil {
		return err
	}

	// The deadline goes into its hourly bucket too, so the sweeper can find
	// expired step-ups with a bounded range read instead of a table scan.
	return r.indexExpiry(ctx, ch)
}

// indexExpiry writes the challenge into the hourly bucket its deadline falls
// in. The challenge row stays the source of truth; this is the work queue the
// expiry sweep reads.
func (r *cassandraSCAChallengeRepository) indexExpiry(ctx context.Context, ch *domain.SCAChallenge) error {
	query := `INSERT INTO card_sca_challenges_by_expiry (bucket, expires_at, challenge_id)
		VALUES (?, ?, ?) USING TTL ?`
	return r.session.Query(query,
		domain.ExpiryBucket(ch.ExpiresAt),
		ch.ExpiresAt,
		toUUID(ch.ChallengeID),
		expiryIndexTTL,
	).WithContext(ctx).Exec()
}

func (r *cassandraSCAChallengeRepository) ListDueExpiring(ctx context.Context, buckets []string, now time.Time) ([]domain.SCAExpiring, error) {
	var due []domain.SCAExpiring
	for _, bucket := range buckets {
		iter := r.session.Query(
			`SELECT bucket, expires_at, challenge_id FROM card_sca_challenges_by_expiry WHERE bucket = ? AND expires_at <= ?`,
			bucket, now,
		).WithContext(ctx).Iter()

		var entry domain.SCAExpiring
		for iter.Scan(&entry.Bucket, &entry.ExpiresAt, &entry.ChallengeID) {
			due = append(due, entry)
			entry = domain.SCAExpiring{}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return due, nil
}

func (r *cassandraSCAChallengeRepository) ClearExpiry(ctx context.Context, entry domain.SCAExpiring) error {
	query := `DELETE FROM card_sca_challenges_by_expiry WHERE bucket = ? AND expires_at = ? AND challenge_id = ?`
	return r.session.Query(query, entry.Bucket, entry.ExpiresAt, toUUID(entry.ChallengeID)).WithContext(ctx).Exec()
}

func (r *cassandraSCAChallengeRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.SCAChallenge, error) {
	var ch domain.SCAChallenge
	var challengeID, authorizationID, cardID, userID, accountID gocql.UUID
	var status string
	var completedAt *time.Time

	query := `SELECT challenge_id, authorization_id, card_id, user_id, account_id,
		amount, currency, merchant,
		otp_hash, attempts, max_attempts, status, failure_reason,
		expires_at, created_at, updated_at, completed_at
		FROM card_sca_challenges WHERE challenge_id = ?`

	err := r.session.Query(query, toUUID(id)).WithContext(ctx).Scan(
		&challengeID, &authorizationID, &cardID, &userID, &accountID,
		&ch.Amount, &ch.Currency, &ch.Merchant,
		&ch.OTPHash, &ch.Attempts, &ch.MaxAttempts, &status, &ch.FailureReason,
		&ch.ExpiresAt, &ch.CreatedAt, &ch.UpdatedAt, &completedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, domain.ErrChallengeNotFound
	}
	if err != nil {
		return nil, err
	}

	ch.ChallengeID = uuid.UUID(challengeID)
	ch.AuthorizationID = uuid.UUID(authorizationID)
	ch.CardID = uuid.UUID(cardID)
	ch.UserID = uuid.UUID(userID)
	ch.AccountID = uuid.UUID(accountID)
	ch.Status = schemes.ChallengeStatus(status)
	ch.CompletedAt = completedAt
	return &ch, nil
}

func (r *cassandraSCAChallengeRepository) Transition(ctx context.Context, ch *domain.SCAChallenge, expectedAttempts int) (bool, error) {
	query := `UPDATE card_sca_challenges
		SET attempts = ?, status = ?, failure_reason = ?, updated_at = ?, completed_at = ?
		WHERE challenge_id = ? IF status = ? AND attempts = ?`

	applied, err := r.session.Query(query,
		ch.Attempts,
		string(ch.Status),
		ch.FailureReason,
		ch.UpdatedAt,
		ch.CompletedAt,
		toUUID(ch.ChallengeID),
		string(schemes.ChallengePending),
		expectedAttempts,
	).WithContext(ctx).ScanCAS()
	if err != nil {
		return false, err
	}
	return applied, nil
}
