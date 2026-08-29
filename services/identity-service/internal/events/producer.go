package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"
)

type KafkaProducer struct {
	producer sarama.AsyncProducer
	topic    string
	logger   zerolog.Logger
}

func NewKafkaProducer(brokers []string, topic string, logger zerolog.Logger) (*KafkaProducer, error) {
	config := sarama.NewConfig()
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.RequiredAcks = sarama.WaitForLocal
	config.Producer.Timeout = 5 * time.Second

	producer, err := sarama.NewAsyncProducer(brokers, config)
	if err != nil {
		return nil, err
	}

	kp := &KafkaProducer{
		producer: producer,
		topic:    topic,
		logger:   logger,
	}

	go kp.handleSuccesses()
	go kp.handleErrors()

	return kp, nil
}

func (p *KafkaProducer) handleSuccesses() {
	for msg := range p.producer.Successes() {
		p.logger.Debug().Str("topic", msg.Topic).Int32("partition", msg.Partition).Int64("offset", msg.Offset).Msg("message sent")
	}
}

func (p *KafkaProducer) handleErrors() {
	for err := range p.producer.Errors() {
		p.logger.Error().Err(err).Msg("failed to send message")
	}
}

func (p *KafkaProducer) Publish(ctx context.Context, eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg := &sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(eventType),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event_type"), Value: []byte(eventType)},
			{Key: []byte("timestamp"), Value: []byte(time.Now().UTC().Format(time.RFC3339))},
		},
	}

	select {
	case p.producer.Input() <- msg:
		p.logger.Info().Str("event_type", eventType).Msg("event queued")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *KafkaProducer) Close() error {
	return p.producer.Close()
}
