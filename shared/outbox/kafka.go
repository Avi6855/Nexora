package outbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"
)

// KafkaPublisherConfig configures the relay's Kafka producer.
type KafkaPublisherConfig struct {
	Brokers []string
	Logger  zerolog.Logger
	// DLQTopics maps a topic prefix to its dead-letter topic, e.g.
	// "nexora.payment." -> "nexora.payment.dlq". When nil, DefaultDLQTopics
	// is used; unknown topics fall back to <topic>.dlq.
	DLQTopics map[string]string
}

// DefaultDLQTopics mirrors the DLQ topic conventions in kafka/topics.go.
func DefaultDLQTopics() map[string]string {
	return map[string]string{
		"nexora.payment.":      "nexora.payment.dlq",
		"nexora.transfer.":     "nexora.transfer.dlq",
		"nexora.card.":         "nexora.card.dlq",
		"nexora.pot.":          "nexora.pot.dlq",
		"nexora.ledger.":       "nexora.ledger.dlq",
		"nexora.notification.": "nexora.notification.dlq",
		"nexora.fraud.":        "nexora.fraud.dlq",
		"nexora.audit.":        "nexora.audit.dlq",
	}
}

// KafkaPublisher publishes outbox events to Kafka using a synchronous
// producer, so a successful Publish means the broker acknowledged the write.
type KafkaPublisher struct {
	producer sarama.SyncProducer
	logger   zerolog.Logger
	dlq      map[string]string
}

func NewKafkaPublisher(cfg KafkaPublisherConfig) (*KafkaPublisher, error) {
	config := sarama.NewConfig()
	config.Producer.Return.Successes = true
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 3
	config.Producer.Retry.Backoff = 500 * time.Millisecond
	config.Producer.Timeout = 10 * time.Second
	config.ClientID = "outbox-relay"

	producer, err := sarama.NewSyncProducer(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("creating outbox kafka producer: %w", err)
	}

	return &KafkaPublisher{
		producer: producer,
		logger:   cfg.Logger,
		dlq:      dlqTopicsOrDefault(cfg.DLQTopics),
	}, nil
}

func (p *KafkaPublisher) Publish(ctx context.Context, event *Event) error {
	msg := messageFor(event, event.Topic)
	msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte("attempts"), Value: []byte(strconv.Itoa(event.Attempts))})

	partition, offset, err := p.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("publishing event %s to %s: %w", event.EventID, event.Topic, err)
	}
	p.logger.Debug().
		Str("event_id", event.EventID.String()).
		Str("topic", event.Topic).
		Int32("partition", partition).
		Int64("offset", offset).
		Msg("outbox relay: kafka acknowledged message")
	return nil
}

func (p *KafkaPublisher) PublishDLQ(ctx context.Context, event *Event) error {
	topic := p.dlqTopicFor(event)
	msg := messageFor(event, topic)
	msg.Headers = append(msg.Headers,
		sarama.RecordHeader{Key: []byte("dlq_reason"), Value: []byte(event.LastError)},
		sarama.RecordHeader{Key: []byte("attempts"), Value: []byte(strconv.Itoa(event.Attempts))},
	)

	if _, _, err := p.producer.SendMessage(msg); err != nil {
		return fmt.Errorf("publishing event %s to DLQ %s: %w", event.EventID, topic, err)
	}
	p.logger.Warn().
		Str("event_id", event.EventID.String()).
		Str("topic", topic).
		Str("reason", event.LastError).
		Msg("outbox relay: event moved to dead-letter topic")
	return nil
}

func (p *KafkaPublisher) Close() error {
	return p.producer.Close()
}

func (p *KafkaPublisher) dlqTopicFor(event *Event) string {
	for prefix, topic := range p.dlq {
		if strings.HasPrefix(event.Topic, prefix) {
			return topic
		}
	}
	return event.Topic + ".dlq"
}

func messageFor(event *Event, topic string) *sarama.ProducerMessage {
	return &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(event.AggregateID),
		Value: sarama.ByteEncoder(event.Payload),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte(event.EventType)},
			{Key: []byte("event_id"), Value: []byte(event.EventID.String())},
			{Key: []byte("correlation_id"), Value: []byte(event.CorrelationID)},
			{Key: []byte("causation_id"), Value: []byte(event.CausationID)},
			{Key: []byte("producer"), Value: []byte(event.Producer)},
			{Key: []byte("timestamp"), Value: []byte(event.CreatedAt.Format(time.RFC3339))},
		},
	}
}

func dlqTopicsOrDefault(configured map[string]string) map[string]string {
	if configured != nil {
		return configured
	}
	return DefaultDLQTopics()
}