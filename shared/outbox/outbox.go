// Package outbox implements the transactional outbox pattern (ADR-005).
//
// Domain services never publish to Kafka directly. Instead they write their
// state transition and the corresponding outbox event in a single atomic
// operation (a logged Cassandra batch), which eliminates the dual-write
// problem: the aggregate can never change without its event being durably
// recorded, and events can never be lost because Kafka is unavailable.
//
// A Relay drains the outbox: it claims PENDING events, publishes them to
// Kafka, and marks them PUBLISHED. Failed publishes are retried on every poll
// with backoff-free retries; an event whose attempt count exceeds the maximum
// is moved to a dead-letter topic and marked FAILED (terminal), so a poison
// event can never wedge the relay.
//
// Delivery is at-least-once. Consumers MUST deduplicate on event_id /
// idempotency_key — the relay can deliver an event whose "published" marker
// was not written (crash between Kafka ack and Cassandra update).
package outbox

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle of an outbox event.
type Status string

const (
	// StatusPending means the event is waiting for the relay.
	StatusPending Status = "PENDING"
	// StatusPublished means the event was delivered to Kafka.
	StatusPublished Status = "PUBLISHED"
	// StatusFailed is terminal: the event exhausted its retries and was
	// moved to the dead-letter topic.
	StatusFailed Status = "FAILED"
)

// Event is a durable, Kafka-ready representation of a domain event.
type Event struct {
	EventID       uuid.UUID `json:"event_id"`
	AggregateID   string    `json:"aggregate_id"`
	EventType     string    `json:"event_type"`
	Topic         string    `json:"topic"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id"`
	Producer      string    `json:"producer"`
	Payload       []byte    `json:"payload"`
	Status        Status    `json:"status"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}

// Store is the durable backing store for the outbox.
type Store interface {
	// Enqueue durably records one or more events as PENDING.
	Enqueue(ctx context.Context, events ...*Event) error
	// ClaimPending returns up to limit PENDING events for the relay.
	ClaimPending(ctx context.Context, limit int) ([]*Event, error)
	// MarkPublished records successful delivery.
	MarkPublished(ctx context.Context, eventID uuid.UUID) error
	// MarkFailed records a delivery failure and increments the attempt count.
	// The event stays PENDING so the relay retries it on the next poll.
	MarkFailed(ctx context.Context, eventID uuid.UUID, err error) error
	// MarkTerminal records that the event exhausted retries and was DLQ'd.
	MarkTerminal(ctx context.Context, eventID uuid.UUID) error
	// CountPending returns the number of events awaiting delivery.
	CountPending(ctx context.Context) (int64, error)
}

// Publisher delivers an outbox event to the messaging system.
type Publisher interface {
	Publish(ctx context.Context, event *Event) error
	// PublishDLQ delivers an unrecoverable event to the dead-letter topic.
	PublishDLQ(ctx context.Context, event *Event) error
}