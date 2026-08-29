package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/replay-service/internal/domain"
)

type cassandraReplayRepository struct {
	session *gocql.Session
}

func NewCassandraReplayRepository(session *gocql.Session) ReplayRepository {
	return &cassandraReplayRepository{session: session}
}

func (r *cassandraReplayRepository) StoreReplayResult(ctx context.Context, result *domain.ReplayResult) error {
	query := `INSERT INTO replay_results (
		replay_id, original_result, replayed_result, differences,
		total_events, deterministic, replayed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		result.ReplayID,
		result.OriginalResult,
		result.ReplayedResult,
		"",
		result.TotalEvents,
		result.Deterministic,
		result.ReplayedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraReplayRepository) GetReplayResult(ctx context.Context, id uuid.UUID) (*domain.ReplayResult, error) {
	var result domain.ReplayResult
	var differences string

	query := `SELECT replay_id, original_result, replayed_result, differences,
		total_events, deterministic, replayed_at
		FROM replay_results WHERE replay_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&result.ReplayID,
		&result.OriginalResult,
		&result.ReplayedResult,
		&differences,
		&result.TotalEvents,
		&result.Deterministic,
		&result.ReplayedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("replay result not found")
	}
	if err != nil {
		return nil, err
	}

	return &result, nil
}
