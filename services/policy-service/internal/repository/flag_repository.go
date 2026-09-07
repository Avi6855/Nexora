package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/services/policy-service/internal/domain"
)

// FlagRepository persists feature flags.
type FlagRepository interface {
	UpsertFlag(ctx context.Context, f *domain.FeatureFlag) error
	GetFlag(ctx context.Context, flagID uuid.UUID) (*domain.FeatureFlag, error)
	GetFlagByKey(ctx context.Context, key string) (*domain.FeatureFlag, error)
	ListFlags(ctx context.Context, limit int) ([]*domain.FeatureFlag, error)
	DeleteFlag(ctx context.Context, flagID uuid.UUID) error
}

type cassandraFlagRepository struct {
	session *gocql.Session
}

// NewCassandraFlagRepository builds the flag repository.
func NewCassandraFlagRepository(session *gocql.Session) FlagRepository {
	return &cassandraFlagRepository{session: session}
}

const flagCols = `flag_id, key, description, enabled, rollout_pct, cohort_constraint,
	baseline_error_pct, max_error_pct, max_latency_ms, error_pct, latency_ms,
	rolled_back, rollback_reason, created_at, updated_at`

func scanFlag(row func(dest ...interface{}) bool) (*domain.FeatureFlag, error) {
	var f domain.FeatureFlag
	var fid gocql.UUID
	var key, description, cohort, rollbackReason string
	var enabled, rolledBack bool
	var rollout int
	var baselineErr, maxErr, maxLatency, errPct, latency float64
	var createdAt, updatedAt time.Time
	ok := row(&fid, &key, &description, &enabled, &rollout, &cohort,
		&baselineErr, &maxErr, &maxLatency, &errPct, &latency,
		&rolledBack, &rollbackReason, &createdAt, &updatedAt)
	if !ok {
		return nil, nil
	}
	f.FlagID = uuid.UUID(fid)
	f.Key = key
	f.Description = description
	f.Enabled = enabled
	f.RolloutPct = rollout
	f.CohortConstraint = cohort
	f.BaselineErrorPct = baselineErr
	f.MaxErrorPct = maxErr
	f.MaxLatencyMs = maxLatency
	f.ErrorPct = errPct
	f.LatencyMs = latency
	f.RolledBack = rolledBack
	f.RollbackReason = rollbackReason
	f.CreatedAt = createdAt
	f.UpdatedAt = updatedAt
	return &f, nil
}

func (r *cassandraFlagRepository) UpsertFlag(ctx context.Context, f *domain.FeatureFlag) error {
	q := `INSERT INTO feature_flags (` + flagCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(f.FlagID), f.Key, f.Description, f.Enabled, f.RolloutPct, f.CohortConstraint,
		f.BaselineErrorPct, f.MaxErrorPct, f.MaxLatencyMs, f.ErrorPct, f.LatencyMs,
		f.RolledBack, f.RollbackReason, f.CreatedAt, f.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraFlagRepository) GetFlag(ctx context.Context, flagID uuid.UUID) (*domain.FeatureFlag, error) {
	q := `SELECT ` + flagCols + ` FROM feature_flags WHERE flag_id = ?`
	iter := r.session.Query(q, gocql.UUID(flagID)).WithContext(ctx).Iter()
	defer iter.Close()
	f, err := scanFlag(iter.Scan)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, domain.ErrFlagNotFound
	}
	return f, nil
}

func (r *cassandraFlagRepository) GetFlagByKey(ctx context.Context, key string) (*domain.FeatureFlag, error) {
	q := `SELECT ` + flagCols + ` FROM feature_flags WHERE key = ? LIMIT 1`
	iter := r.session.Query(q, key).WithContext(ctx).Iter()
	defer iter.Close()
	f, err := scanFlag(iter.Scan)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, domain.ErrFlagNotFound
	}
	return f, nil
}

func (r *cassandraFlagRepository) ListFlags(ctx context.Context, limit int) ([]*domain.FeatureFlag, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT ` + flagCols + ` FROM feature_flags LIMIT ?`
	iter := r.session.Query(q, limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.FeatureFlag, 0)
	for {
		f, err := scanFlag(iter.Scan)
		if err != nil {
			return nil, err
		}
		if f == nil {
			break
		}
		out = append(out, f)
	}
	return out, nil
}

func (r *cassandraFlagRepository) DeleteFlag(ctx context.Context, flagID uuid.UUID) error {
	q := `DELETE FROM feature_flags WHERE flag_id = ?`
	return r.session.Query(q, gocql.UUID(flagID)).WithContext(ctx).Exec()
}
