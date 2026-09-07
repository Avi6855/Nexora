package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// RoundupRepository claims round-up work exactly once. The first consumer to
// claim an authorization gets true; redeliveries (at-least-once Kafka) get
// false and must skip, so a captured payment is never swept twice.
type RoundupRepository interface {
	ClaimAuthorization(ctx context.Context, authorizationID, potID uuid.UUID, amount int64) (bool, error)
}

type cassandraRoundupRepository struct {
	session *gocql.Session
}

func NewCassandraRoundupRepository(session *gocql.Session) RoundupRepository {
	return &cassandraRoundupRepository{session: session}
}

func (r *cassandraRoundupRepository) ClaimAuthorization(ctx context.Context, authorizationID, potID uuid.UUID, amount int64) (bool, error) {
	query := `INSERT INTO roundup_processed (authorization_id, pot_id, amount, processed_at)
		VALUES (?, ?, ?, ?) IF NOT EXISTS`
	applied, err := r.session.Query(query,
		gocql.UUID(authorizationID), gocql.UUID(potID), amount, time.Now().UTC(),
	).WithContext(ctx).ScanCAS()
	if err != nil {
		return false, fmt.Errorf("claiming roundup authorization: %w", err)
	}
	return applied, nil
}
