package events

import (
	"context"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"
)

// KafkaGroup runs a sarama consumer group over the intelligence topics.
type KafkaGroup struct {
	consumerGroup sarama.ConsumerGroup
	topics        []string
	handler       *groupHandler
	logger        zerolog.Logger
}

type groupHandler struct {
	eventHandler EventHandler
	logger       zerolog.Logger
}

// NewKafkaGroup builds the consumer group.
func NewKafkaGroup(brokers []string, groupID string, topics []string, eventHandler EventHandler, logger zerolog.Logger) (*KafkaGroup, error) {
	config := sarama.NewConfig()
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	config.Consumer.Return.Errors = true

	consumerGroup, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, err
	}
	return &KafkaGroup{
		consumerGroup: consumerGroup,
		topics:        topics,
		handler:       &groupHandler{eventHandler: eventHandler, logger: logger},
		logger:        logger,
	}, nil
}

// Start consumes in the background until ctx is cancelled.
func (g *KafkaGroup) Start(ctx context.Context) {
	go func() {
		for {
			if err := g.consumerGroup.Consume(ctx, g.topics, g.handler); err != nil {
				g.logger.Error().Err(err).Msg("insights consumer group error")
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
	go func() {
		for err := range g.consumerGroup.Errors() {
			g.logger.Error().Err(err).Msg("insights consumer error")
		}
	}()
}

// Close shuts the consumer group down.
func (g *KafkaGroup) Close() error {
	return g.consumerGroup.Close()
}

func (h *groupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *groupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *groupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		eventType := getHeader(msg.Headers, "event_type")
		if err := h.eventHandler(sess.Context(), msg.Topic, eventType, msg.Value); err != nil {
			h.logger.Error().Err(err).Str("topic", msg.Topic).Str("event_type", eventType).Msg("failed to handle insights event")
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
