package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/IBM/sarama"
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

	// Real-time authorisation lifecycle events. Payload is a flat
	// AuthorizationEvent (see authorization_event.go) so any consumer can
	// build push notifications / feed items without unwrapping an envelope.
	EventTypeAuthApproved EventType = "card.authorization.approved"
	EventTypeAuthDeclined EventType = "card.authorization.declined"
	EventTypeAuthCaptured EventType = "card.authorization.captured"
	EventTypeAuthVoided   EventType = "card.authorization.voided"
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
	producer  sarama.AsyncProducer
	producerID  string
	topicPrefix string
	logger      zerolog.Logger
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

	config := sarama.NewConfig()
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.RequiredAcks = sarama.WaitForLocal
	config.Producer.Timeout = 5 * time.Second

	producer, err := sarama.NewAsyncProducer(cfg.Brokers, config)
	if err != nil {
		cfg.Logger.Warn().Err(err).Msg("failed to create sarama producer, events will not be published")
		return &KafkaEventPublisher{
			producer:    nil,
			producerID:  cfg.ProducerID,
			topicPrefix: cfg.TopicPrefix,
			logger:      cfg.Logger,
		}
	}

	p := &KafkaEventPublisher{
		producer:    producer,
		producerID:  cfg.ProducerID,
		topicPrefix: cfg.TopicPrefix,
		logger:      cfg.Logger,
	}

	go p.handleSuccesses()
	go p.handleErrors()

	return p
}

func (p *KafkaEventPublisher) handleSuccesses() {
	for msg := range p.producer.Successes() {
		p.logger.Debug().Str("topic", msg.Topic).Int32("partition", msg.Partition).Int64("offset", msg.Offset).Msg("card event sent")
	}
}

func (p *KafkaEventPublisher) handleErrors() {
	for err := range p.producer.Errors() {
		p.logger.Error().Err(err).Msg("failed to send card event")
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

	if p.producer == nil {
		p.logger.Warn().Str("event_type", string(eventType)).Str("aggregate_id", aggregateID).Msg("kafka unavailable, event not published")
		return nil
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event envelope: %w", err)
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(string(eventType)),
		Value: sarama.ByteEncoder(eventData),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte(string(eventType))},
			{Key: []byte("event_id"), Value: []byte(event.EventID)},
			{Key: []byte("timestamp"), Value: []byte(event.Timestamp.Format(time.RFC3339))},
		},
	}

	select {
	case p.producer.Input() <- msg:
		p.logger.Info().
			Str("event_id", event.EventID).
			Str("event_type", string(eventType)).
			Str("aggregate_id", aggregateID).
			Str("topic", topic).
			Msg("card event queued")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PublishAuthorizationEvent sends a real-time authorization lifecycle event to
// the per-type topic (nexora.card.authorization.*). The value is the flat
// AuthorizationEvent payload; headers carry event_type + timestamp.
func (p *KafkaEventPublisher) PublishAuthorizationEvent(ctx context.Context, eventType EventType, ev *AuthorizationEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshaling authorization event: %w", err)
	}

	topic := fmt.Sprintf("%s.%s", p.topicPrefix, string(eventType))

	if p.producer == nil {
		p.logger.Warn().Str("event_type", string(eventType)).Str("authorization_id", ev.AuthorizationID).Msg("kafka unavailable, authorization event not published")
		return nil
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(ev.UserID),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte(string(eventType))},
			{Key: []byte("event_id"), Value: []byte(ev.AuthorizationID)},
			{Key: []byte("timestamp"), Value: []byte(time.Now().UTC().Format(time.RFC3339))},
		},
	}

	select {
	case p.producer.Input() <- msg:
		p.logger.Info().Str("event_type", string(eventType)).Str("authorization_id", ev.AuthorizationID).Str("topic", topic).Msg("authorization event queued")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *KafkaEventPublisher) Close() error {
	if p.producer == nil {
		return nil
	}
	return p.producer.Close()
}

type NoOpEventPublisher struct{}

func (n *NoOpEventPublisher) PublishCardEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error {
	return nil
}

func (n *NoOpEventPublisher) Close() error {
	return nil
}
