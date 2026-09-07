package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/shared/outbox"
)

// OutboxEventPublisher implements EventPublisher by persisting events to the
// transactional outbox instead of publishing to Kafka directly. A relay
// (shared/outbox) drains the outbox and delivers to Kafka with retry + DLQ.
//
// This is the fallback path used when a repository cannot write the aggregate
// and its events atomically (e.g. in-memory test repositories). Production
// repositories implement repository.AtomicEventWriter so the payment saga
// writes state + events in a single logged Cassandra batch — see
// PaymentSaga.persistTransition in the service package.
type OutboxEventPublisher struct {
	store       outbox.Store
	producer    string
	topicPrefix string
}

func NewOutboxEventPublisher(store outbox.Store, producer, topicPrefix string) *OutboxEventPublisher {
	return &OutboxEventPublisher{
		store:       store,
		producer:    producer,
		topicPrefix: topicPrefix,
	}
}

func (p *OutboxEventPublisher) PublishPaymentEvent(ctx context.Context, eventType EventType, payment interface{}, correlationID string) error {
	event, err := BuildPaymentOutboxEvent(eventType, payment, correlationID, p.producer, p.topicPrefix)
	if err != nil {
		return err
	}
	return p.store.Enqueue(ctx, event)
}

func (p *OutboxEventPublisher) Close() error {
	return nil
}

// BuildPaymentOutboxEvent builds a fully-formed outbox.Event for a payment
// state transition. The payload is the same PaymentEventEnvelope JSON that the
// direct Kafka publisher emits, so consumers are unchanged.
func BuildPaymentOutboxEvent(eventType EventType, payment interface{}, correlationID, producer, topicPrefix string) (*outbox.Event, error) {
	payload, err := json.Marshal(payment)
	if err != nil {
		return nil, fmt.Errorf("marshaling event payload: %w", err)
	}

	eventID := uuid.New()
	envelope := &PaymentEventEnvelope{
		EventID:       eventID.String(),
		EventType:     eventType,
		AggregateID:   extractAggregateID(payment),
		CorrelationID: correlationID,
		CausationID:   uuid.New().String(),
		Producer:      producer,
		Timestamp:     time.Now().UTC(),
		Payload:       payload,
		EventVersion:  1,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshaling event envelope: %w", err)
	}

	return &outbox.Event{
		EventID:       eventID,
		AggregateID:   envelope.AggregateID,
		EventType:     string(eventType),
		Topic:         fmt.Sprintf("%s.%s", topicPrefix, string(eventType)),
		CorrelationID: correlationID,
		CausationID:   envelope.CausationID,
		Producer:      producer,
		Payload:       data,
		Status:        outbox.StatusPending,
		CreatedAt:     envelope.Timestamp,
	}, nil
}