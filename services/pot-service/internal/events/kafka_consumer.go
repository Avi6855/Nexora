package events

import (
	"context"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"
)

// RoundupEventHandler receives each raw message with its topic and event_type
// header (same shape as the notification-service consumer).
type RoundupEventHandler func(ctx context.Context, topic, eventType string, value []byte) error

// KafkaConsumer consumes the captured-authorization topic for roundups.
type KafkaConsumer struct {
	consumerGroup sarama.ConsumerGroup
	topics        []string
	handler       *roundupGroupHandler
	logger        zerolog.Logger
}

type roundupGroupHandler struct {
	eventHandler RoundupEventHandler
	logger       zerolog.Logger
}

func NewKafkaConsumer(brokers []string, groupID string, topics []string, eventHandler RoundupEventHandler, logger zerolog.Logger) (*KafkaConsumer, error) {
	config := sarama.NewConfig()
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	config.Consumer.Return.Errors = true

	consumerGroup, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, err
	}

	return &KafkaConsumer{
		consumerGroup: consumerGroup,
		topics:        topics,
		handler:       &roundupGroupHandler{eventHandler: eventHandler, logger: logger},
		logger:        logger,
	}, nil
}

func (c *KafkaConsumer) Start(ctx context.Context) {
	go func() {
		for {
			if err := c.consumerGroup.Consume(ctx, c.topics, c.handler); err != nil {
				c.logger.Error().Err(err).Msg("roundups consumer group error")
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()

	go func() {
		for err := range c.consumerGroup.Errors() {
			c.logger.Error().Err(err).Msg("roundups consumer error")
		}
	}()
}

func (c *KafkaConsumer) Close() error {
	return c.consumerGroup.Close()
}

func (h *roundupGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *roundupGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *roundupGroupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		eventType := getHeader(msg.Headers, "event_type")
		if err := h.eventHandler(sess.Context(), msg.Topic, eventType, msg.Value); err != nil {
			h.logger.Error().Err(err).Str("topic", msg.Topic).Str("event_type", eventType).Msg("failed to handle roundup event")
		}
		sess.MarkMessage(msg, "")
	}
	return nil
}

func getHeader(headers []*sarama.RecordHeader, key string) string {
	for _, h := range headers {
		if string(h.Key) == key {
			return string(h.Value)
		}
	}
	return ""
}
