package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/insights-service/internal/domain"
)

// AlertProducer publishes insight alerts to nexora.insights.alerts so the
// notification service can fan them out to the app feed + SSE stream.
type AlertProducer struct {
	producer sarama.AsyncProducer
	topic    string
	logger   zerolog.Logger
}

// NewAlertProducer connects to Kafka for alert publishing. A nil error with a
// nil producer is never returned: callers treat construction failure as
// fatal-or-optional depending on deployment mode (same as the other services).
func NewAlertProducer(brokers []string, topic string, logger zerolog.Logger) (*AlertProducer, error) {
	config := sarama.NewConfig()
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.RequiredAcks = sarama.WaitForLocal
	config.Producer.Timeout = 5 * time.Second

	producer, err := sarama.NewAsyncProducer(brokers, config)
	if err != nil {
		return nil, err
	}

	ap := &AlertProducer{producer: producer, topic: topic, logger: logger}
	go ap.handleSuccesses()
	go ap.handleErrors()
	return ap, nil
}

func (p *AlertProducer) handleSuccesses() {
	for msg := range p.producer.Successes() {
		p.logger.Debug().Str("topic", msg.Topic).Int64("offset", msg.Offset).Msg("alert sent")
	}
}

func (p *AlertProducer) handleErrors() {
	for err := range p.producer.Errors() {
		p.logger.Error().Err(err).Msg("failed to send insight alert")
	}
}

// PublishAlert implements service.AlertPublisher.
func (p *AlertProducer) PublishAlert(ctx context.Context, alert *domain.InsightAlert) error {
	data, err := json.Marshal(alert)
	if err != nil {
		return err
	}
	msg := &sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(alert.AccountID.String()),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte("insight.alert")},
			{Key: []byte("timestamp"), Value: []byte(time.Now().UTC().Format(time.RFC3339))},
		},
	}
	select {
	case p.producer.Input() <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close flushes and shuts the producer down.
func (p *AlertProducer) Close() error {
	return p.producer.Close()
}
