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

type OutboxEntry struct {
	ID          string
	Topic       string
	Key         string
	Payload     []byte
	Status      string
	CreatedAt   time.Time
	PublishedAt *time.Time
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
	brokers      []string
	topicPrefix  string
	logger       zerolog.Logger
	outbox       []*OutboxEntry
	mu           sync.Mutex
	useOutbox    bool
	published    []*PaymentEventEnvelope
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
	return &KafkaEventPublisher{
		producerID:  cfg.ProducerID,
		brokers:     cfg.Brokers,
		topicPrefix: cfg.TopicPrefix,
		logger:      cfg.Logger,
		useOutbox:   cfg.UseOutbox,
		published:   make([]*PaymentEventEnvelope, 0),
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
	key := event.AggregateID

	if p.useOutbox {
		p.addToOutbox(topic, key, event)
	}

	p.mu.Lock()
	p.published = append(p.published, event)
	p.mu.Unlock()

	p.logger.Info().
		Str("event_id", event.EventID).
		Str("event_type", string(eventType)).
		Str("aggregate_id", event.AggregateID).
		Str("topic", topic).
		Msg("published payment event")

	return nil
}

func (p *KafkaEventPublisher) topicForEvent(eventType EventType) string {
	return fmt.Sprintf("%s.%s", p.topicPrefix, string(eventType))
}

func (p *KafkaEventPublisher) addToOutbox(topic, key string, event *PaymentEventEnvelope) {
	data, err := json.Marshal(event)
	if err != nil {
		p.logger.Error().Err(err).Msg("failed to marshal event for outbox")
		return
	}

	entry := &OutboxEntry{
		ID:        uuid.New().String(),
		Topic:     topic,
		Key:       key,
		Payload:   data,
		Status:    "PENDING",
		CreatedAt: time.Now().UTC(),
	}

	p.mu.Lock()
	p.outbox = append(p.outbox, entry)
	p.mu.Unlock()
}

func (p *KafkaEventPublisher) GetOutboxEntries() []*OutboxEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*OutboxEntry, len(p.outbox))
	copy(result, p.outbox)
	return result
}

func (p *KafkaEventPublisher) GetPublishedEvents() []*PaymentEventEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*PaymentEventEnvelope, len(p.published))
	copy(result, p.published)
	return result
}

func (p *KafkaEventPublisher) MarkOutboxPublished(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	for _, entry := range p.outbox {
		if entry.ID == id {
			entry.Status = "PUBLISHED"
			entry.PublishedAt = &now
			break
		}
	}
}

func (p *KafkaEventPublisher) Close() error {
	return nil
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
