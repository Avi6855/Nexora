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
	EventTypePotCreated  EventType = "pot.created"
	EventTypePotDeposit  EventType = "pot.deposit"
	EventTypePotWithdraw EventType = "pot.withdraw"
	EventTypePotRenamed  EventType = "pot.renamed"
	EventTypePotClosed   EventType = "pot.closed"
	EventTypePotDeleted  EventType = "pot.deleted"
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
	producer    sarama.AsyncProducer
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
		cfg.ProducerID = "pot-service"
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
		p.logger.Debug().Str("topic", msg.Topic).Int32("partition", msg.Partition).Int64("offset", msg.Offset).Msg("message sent")
	}
}

func (p *KafkaEventPublisher) handleErrors() {
	for err := range p.producer.Errors() {
		p.logger.Error().Err(err).Msg("failed to send message")
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

	envelopeData, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event envelope: %w", err)
	}

	topic := fmt.Sprintf("%s.%s", p.topicPrefix, string(eventType))

	if p.producer == nil {
		p.logger.Warn().Str("event_type", string(eventType)).Str("aggregate_id", aggregateID).Msg("kafka unavailable, event not published")
		return nil
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(eventType),
		Value: sarama.ByteEncoder(envelopeData),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte(eventType)},
			{Key: []byte("event_id"), Value: []byte(event.EventID)},
			{Key: []byte("aggregate_id"), Value: []byte(aggregateID)},
			{Key: []byte("timestamp"), Value: []byte(time.Now().UTC().Format(time.RFC3339))},
		},
	}

	select {
	case p.producer.Input() <- msg:
		p.logger.Info().
			Str("event_id", event.EventID).
			Str("event_type", string(eventType)).
			Str("aggregate_id", aggregateID).
			Str("topic", topic).
			Msg("published pot event")
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

func (n *NoOpEventPublisher) PublishPotEvent(ctx context.Context, eventType EventType, payload interface{}, aggregateID, correlationID string) error {
	return nil
}

func (n *NoOpEventPublisher) Close() error {
	return nil
}
