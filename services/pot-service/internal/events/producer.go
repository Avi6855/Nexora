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
	EventTypePotCreated   EventType = "pot.created"
	EventTypePotDeposit   EventType = "pot.deposit"
	EventTypePotWithdraw  EventType = "pot.withdraw"
	EventTypePotRenamed   EventType = "pot.renamed"
	EventTypePotClosed    EventType = "pot.closed"
	EventTypePotDeleted   EventType = "pot.deleted"
)

type PotEventEnvelope struct {
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
	PublishPotEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error
	Close() error
}

type KafkaEventPublisher struct {
	producerID  string
	topicPrefix string
	logger      zerolog.Logger
	published   []*PotEventEnvelope
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
		cfg.ProducerID = "pot-service"
	}
	if cfg.TopicPrefix == "" {
		cfg.TopicPrefix = "nexora"
	}
	return &KafkaEventPublisher{
		producerID:  cfg.ProducerID,
		topicPrefix: cfg.TopicPrefix,
		logger:      cfg.Logger,
		published:   make([]*PotEventEnvelope, 0),
	}
}

func (p *KafkaEventPublisher) PublishPotEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling event payload: %w", err)
	}

	causationID := uuid.New().String()
	if correlationID == "" {
		correlationID = causationID
	}

	event := &PotEventEnvelope{
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
		Msg("published pot event")

	return nil
}

func (p *KafkaEventPublisher) GetPublishedEvents() []*PotEventEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*PotEventEnvelope, len(p.published))
	copy(result, p.published)
	return result
}

func (p *KafkaEventPublisher) Close() error {
	return nil
}

type NoOpEventPublisher struct{}

func (n *NoOpEventPublisher) PublishPotEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error {
	return nil
}

func (n *NoOpEventPublisher) Close() error {
	return nil
}
