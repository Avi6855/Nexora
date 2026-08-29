package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

type EventType string

const (
	EventTypeCardCreated  EventType = "card.created"
	EventTypeCardFrozen   EventType = "card.frozen"
	EventTypeCardUnfrozen EventType = "card.unfrozen"
	EventTypeCardBlocked  EventType = "card.blocked"
	EventTypeCardUpdated  EventType = "card.updated"
)

type CardEventEnvelope struct {
	EventID       string          `json:"event_id"`
	EventType     EventType       `json:"event_type"`
	AggregateID   string          `json:"aggregate_id"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id"`
	Producer      string          `json:"producer"`
	Timestamp     time.Time       `json:"timestamp"`
	Payload       json.RawMessage `json:"payload"`
	EventVersion  int             `json:"event_version"`
}

type EventPublisher interface {
	PublishCardEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error
	Close() error
}

type KafkaEventPublisher struct {
	producerID  string
	topicPrefix string
	logger      zerolog.Logger
	published   []*CardEventEnvelope
	mu          sync.Mutex
}

type KafkaPublisherConfig struct {
	ProducerID  string
	Brokers     []string
	TopicPrefix string
	Logger      zerolog.Logger
}

func NewKafkaEventPublisher(cfg KafkaPublisherConfig) *KafkaEventPublisher {
	if cfg.ProducerID == "" {
		cfg.ProducerID = "card-service"
	}
	if cfg.TopicPrefix == "" {
		cfg.TopicPrefix = "nexora"
	}
	return &KafkaEventPublisher{
		producerID:  cfg.ProducerID,
		topicPrefix: cfg.TopicPrefix,
		logger:      cfg.Logger,
		published:   make([]*CardEventEnvelope, 0),
	}
}

func (p *KafkaEventPublisher) PublishCardEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling event payload: %w", err)
	}

	causationID := uuid.New().String()
	if correlationID == "" {
		correlationID = causationID
	}

	event := &CardEventEnvelope{
		EventID:       uuid.New().String(),
		EventType:     eventType,
		AggregateID:   aggregateID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		Producer:      p.producerID,
		Timestamp:     time.Now().UTC(),
		Payload:       data,
		EventVersion:  1,
	}

	topic := fmt.Sprintf("%s.%s", p.topicPrefix, string(eventType))

	p.mu.Lock()
	p.published = append(p.published, event)
	p.mu.Unlock()

	p.logger.Info().
		Str("event_id", event.EventID).
		Str("event_type", string(eventType)).
		Str("aggregate_id", aggregateID).
		Str("topic", topic).
		Msg("published card event")

	return nil
}

func (p *KafkaEventPublisher) GetPublishedEvents() []*CardEventEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*CardEventEnvelope, len(p.published))
	copy(result, p.published)
	return result
}

func (p *KafkaEventPublisher) Close() error {
	return nil
}

type NoOpEventPublisher struct{}

func (n *NoOpEventPublisher) PublishCardEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error {
	return nil
}

func (n *NoOpEventPublisher) Close() error {
	return nil
}
