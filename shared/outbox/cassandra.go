package outbox

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/shared/cassandra"
)

// CassandraStore is the Cassandra-backed outbox Store.
//
// The table is keyed by event_id so claim/mark operations are point updates.
// PENDING claims use the status secondary index with ALLOW FILTERING, which is
// acceptable at single-cluster development scale; a production deployment
// would shard the table by (status_bucket, event_id) so claims never scan.
type CassandraStore struct {
	session *gocql.Session
}

func NewCassandraStore(session *gocql.Session) *CassandraStore {
	return &CassandraStore{session: session}
}

const (
	outboxTable          = "outbox_events"
	outboxInsertFragment = `INSERT INTO outbox_events (event_id, aggregate_id, event_type, topic, correlation_id, causation_id, producer, payload, status, attempts, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
)

func (s *CassandraStore) Enqueue(ctx context.Context, events ...*Event) error {
	if len(events) == 0 {
		return nil
	}
	batch := s.session.NewBatch(gocql.LoggedBatch)
	for _, e := range events {
		batch.Query(outboxInsertFragment,
			cassandra.UID(e.EventID),
			e.AggregateID,
			e.EventType,
			e.Topic,
			e.CorrelationID,
			e.CausationID,
			e.Producer,
			e.Payload,
			string(StatusPending),
			0,
			e.CreatedAt,
		)
	}
	return s.session.ExecuteBatch(batch)
}

func (s *CassandraStore) ClaimPending(ctx context.Context, limit int) ([]*Event, error) {
	// Note: in CQL, LIMIT must precede ALLOW FILTERING.
	const q = `SELECT event_id, aggregate_id, event_type, topic, correlation_id, causation_id, producer, payload, status, attempts, last_error, created_at, published_at
		FROM outbox_events WHERE status = ? LIMIT ? ALLOW FILTERING`

	iter := s.session.Query(q, string(StatusPending), limit).WithContext(ctx).Iter()
	defer iter.Close()

	var events []*Event
	for {
		var e Event
		var status string
		// Scan the CQL uuid into gocql.UUID (gocql cannot scan into
		// google/uuid.UUID directly) and convert.
		var eid gocql.UUID
		if !iter.Scan(
			&eid,
			&e.AggregateID,
			&e.EventType,
			&e.Topic,
			&e.CorrelationID,
			&e.CausationID,
			&e.Producer,
			&e.Payload,
			&status,
			&e.Attempts,
			&e.LastError,
			&e.CreatedAt,
			&e.PublishedAt,
		) {
			break
		}
		e.EventID = uuid.UUID(eid)
		e.Status = Status(status)
		events = append(events, &e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *CassandraStore) MarkPublished(ctx context.Context, eventID uuid.UUID) error {
	const q = `UPDATE outbox_events SET status = ?, published_at = ? WHERE event_id = ?`
	return s.session.Query(q, string(StatusPublished), time.Now().UTC(), cassandra.UID(eventID)).WithContext(ctx).Exec()
}

func (s *CassandraStore) MarkFailed(ctx context.Context, eventID uuid.UUID, err error) error {
	const q = `UPDATE outbox_events SET attempts = attempts + 1, last_error = ? WHERE event_id = ?`
	return s.session.Query(q, err.Error(), cassandra.UID(eventID)).WithContext(ctx).Exec()
}

func (s *CassandraStore) MarkTerminal(ctx context.Context, eventID uuid.UUID) error {
	const q = `UPDATE outbox_events SET status = ?, published_at = ? WHERE event_id = ?`
	return s.session.Query(q, string(StatusFailed), time.Now().UTC(), cassandra.UID(eventID)).WithContext(ctx).Exec()
}

func (s *CassandraStore) CountPending(ctx context.Context) (int64, error) {
	var count int64
	const q = `SELECT COUNT(*) FROM outbox_events WHERE status = ? ALLOW FILTERING`
	if err := s.session.Query(q, string(StatusPending)).WithContext(ctx).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}