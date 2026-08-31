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
	EventTypePaymentCreated    EventType = "payment.created"
	EventTypePaymentAuthorized EventType = "payment.authorized"
	EventTypePaymentProcessing EventType = "payment.processing"
	EventTypePaymentConfirmed  EventType = "payment.confirmed"
	EventTypePaymentSettled    EventType = "payment.settled"
	EventTypePaymentFailed     EventType = "payment.failed"
	EventTypePaymentUnknown    EventType = "payment.unknown"
	EventTypePaymentReversed   EventType = "payment.reversed"
	EventTypePaymentCancelled  EventType = "payment.cancelled"
)

type PaymentEventEnvelope struct {
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

type PaymentEventPayload struct {
	PaymentID           string            `json:"payment_id"`
	IdempotencyKey      string            `json:"idempotency_key"`
	AccountID           string            `json:"account_id"`
	UserID              string            `json:"user_id"`
	PaymentType         string            `json:"payment_type"`
	Amount              int64             `json:"amount"`
	Currency            string            `json:"currency"`
	State               string            `json:"state"`
	PreviousState       string            `json:"previous_state,omitempty"`
	FailureReason       string            `json:"failure_reason,omitempty"`
	CounterpartyID      string            `json:"counterparty_id"`
	CounterpartyName    string            `json:"counterparty_name"`
	Reference           string            `json:"reference"`
	LedgerTransactionID string            `json:"ledger_transaction_id,omitempty"`
	ReservationID       string            `json:"reservation_id,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
}

type EventPublisher interface {
	PublishPaymentEvent(ctx context.Context, eventType EventType, payment interface{}, correlationID string) error
	Close() error
}

type KafkaEventPublisher struct {
	producerID   string
	topicPrefix  string
	logger       zerolog.Logger
	producer     sarama.AsyncProducer
}

type KafkaPublisherConfig struct {
	ProducerID  string
	Brokers     []string
	TopicPrefix string
	Logger      zerolog.Logger
	UseOutbox   bool
}

func NewKafkaEventPublisher(cfg KafkaPublisherConfig) *KafkaEventPublisher {
	if cfg.ProducerID == "" {
		cfg.ProducerID = "payment-service"
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
			producerID:  cfg.ProducerID,
			topicPrefix: cfg.TopicPrefix,
			logger:      cfg.Logger,
			producer:    nil,
		}
	}

	kp := &KafkaEventPublisher{
		producerID:  cfg.ProducerID,
		topicPrefix: cfg.TopicPrefix,
		logger:      cfg.Logger,
		producer:    producer,
	}

	go kp.handleSuccesses()
	go kp.handleErrors()

	return kp
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

func (p *KafkaEventPublisher) PublishPaymentEvent(ctx context.Context, eventType EventType, payment interface{}, correlationID string) error {
	payload, err := json.Marshal(payment)
	if err != nil {
		return fmt.Errorf("marshaling event payload: %w", err)
	}

	event := &PaymentEventEnvelope{
		EventID:       uuid.New().String(),
		EventType:     eventType,
		AggregateID:   extractAggregateID(payment),
		CorrelationID: correlationID,
		CausationID:   uuid.New().String(),
		Producer:      p.producerID,
		Timestamp:     time.Now().UTC(),
		Payload:       payload,
		EventVersion:  1,
	}

	topic := p.topicForEvent(eventType)

	if p.producer == nil {
		p.logger.Warn().Str("event_type", string(eventType)).Str("aggregate_id", event.AggregateID).Msg("kafka unavailable, event not published")
		return nil
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event envelope: %w", err)
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(event.AggregateID),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte(string(eventType))},
			{Key: []byte("timestamp"), Value: []byte(event.Timestamp.Format(time.RFC3339))},
		},
	}

	select {
	case p.producer.Input() <- msg:
		p.logger.Info().
			Str("event_id", event.EventID).
			Str("event_type", string(eventType)).
			Str("aggregate_id", event.AggregateID).
			Str("topic", topic).
			Msg("published payment event")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *KafkaEventPublisher) topicForEvent(eventType EventType) string {
	return fmt.Sprintf("%s.%s", p.topicPrefix, string(eventType))
}

func (p *KafkaEventPublisher) Close() error {
	return p.producer.Close()
}

func extractAggregateID(payment interface{}) string {
	type aggregateIDProvider interface {
		GetAggregateID() string
	}

	if provider, ok := payment.(aggregateIDProvider); ok {
		return provider.GetAggregateID()
	}

	type paymentIDProvider interface {
		GetPaymentID() string
	}

	if provider, ok := payment.(paymentIDProvider); ok {
		return provider.GetPaymentID()
	}

	type paymentID struct {
		PaymentID string `json:"payment_id"`
	}

	var p paymentID
	data, err := json.Marshal(payment)
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return ""
	}
	return p.PaymentID
}

type NoOpEventPublisher struct{}

func (n *NoOpEventPublisher) PublishPaymentEvent(ctx context.Context, eventType EventType, payment interface{}, correlationID string) error {
	return nil
}

func (n *NoOpEventPublisher) Close() error {
	return nil
}
