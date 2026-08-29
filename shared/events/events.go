package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	AggregateID   string          `json:"aggregate_id"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id"`
	Producer      string          `json:"producer"`
	Timestamp     time.Time       `json:"timestamp"`
	Payload       json.RawMessage `json:"payload"`
	EventVersion  int             `json:"event_version"`
}

func NewEvent(eventType, aggregateID, correlationID, causationID, producer string, payload interface{}) (*Event, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &Event{
		EventID:       uuid.New().String(),
		EventType:     eventType,
		AggregateID:   aggregateID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		Producer:      producer,
		Timestamp:     time.Now().UTC(),
		Payload:       p,
		EventVersion:  1,
	}, nil
}

func (e *Event) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

func UnmarshalEvent(data []byte) (*Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

type EventPublisher interface {
	Publish(ctx context.Context, topic string, event *Event) error
	Close() error
}

type EventConsumer interface {
	Subscribe(ctx context.Context, topic string, handler EventHandler) error
	Close() error
}

type EventHandler func(ctx context.Context, event *Event) error
