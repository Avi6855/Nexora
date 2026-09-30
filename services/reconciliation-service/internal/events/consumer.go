package events

import (
	"context"
	"encoding/json"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"
)

// PaymentEventPayload is the subset of payment-service's event payload
// reconciliation needs: which payment went UNKNOWN, and for how much.
type PaymentEventPayload struct {
	PaymentID string `json:"payment_id"`
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	State     string `json:"state"`
}

// PaymentEventHandler is called for every payment event this service consumes.
type PaymentEventHandler func(ctx context.Context, payload *PaymentEventPayload, eventType string) error

// KafkaConsumer consumes the payment.unknown topic so a payment that goes
// UNKNOWN is filed for reconciliation the moment it happens. Without it the
// UNKNOWN bucket only fills; nothing works it until somebody remembers to call
// the sweep by hand.
type KafkaConsumer struct {
	consumerGroup sarama.ConsumerGroup
	topics        []string
	handler       *consumerGroupHandler
	logger        zerolog.Logger
}

type consumerGroupHandler struct {
	eventHandler PaymentEventHandler
	logger       zerolog.Logger
}

func NewKafkaConsumer(brokers []string, groupID string, topics []string, eventHandler PaymentEventHandler, logger zerolog.Logger) (*KafkaConsumer, error) {
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
		handler: &consumerGroupHandler{
			eventHandler: eventHandler,
			logger:       logger,
		},
		logger: logger,
	}, nil
}

func (c *KafkaConsumer) Start(ctx context.Context) {
	go func() {
		for {
			if err := c.consumerGroup.Consume(ctx, c.topics, c.handler); err != nil {
				c.logger.Error().Err(err).Msg("consumer group error")
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()

	go func() {
		for err := range c.consumerGroup.Errors() {
			c.logger.Error().Err(err).Msg("consumer error")
		}
	}()
}

func (c *KafkaConsumer) Close() error {
	return c.consumerGroup.Close()
}

func (h *consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

// envelope is the outer shape payment-service publishes: the domain payload
// lives in the nested `payload` field (see BuildPaymentOutboxEvent).
type envelope struct {
	Payload json.RawMessage `json:"payload"`
}

func (h *consumerGroupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		eventType := getHeader(msg.Headers, "event_type")

		var payload PaymentEventPayload
		if err := json.Unmarshal(msg.Value, &payload); err != nil || payload.PaymentID == "" {
			// Direct payload decode failed (or it is an envelope): unwrap and
			// decode the inner payload.
			var env envelope
			if err := json.Unmarshal(msg.Value, &env); err != nil {
				h.logger.Error().Err(err).Str("topic", msg.Topic).Msg("failed to unmarshal event")
				sess.MarkMessage(msg, "")
				continue
			}
			if err := json.Unmarshal(env.Payload, &payload); err != nil {
				h.logger.Error().Err(err).Str("topic", msg.Topic).Msg("failed to unmarshal event payload")
				sess.MarkMessage(msg, "")
				continue
			}
		}

		if err := h.eventHandler(sess.Context(), &payload, eventType); err != nil {
			h.logger.Error().Err(err).Str("event_type", eventType).Str("payment_id", payload.PaymentID).Msg("failed to handle event")
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
