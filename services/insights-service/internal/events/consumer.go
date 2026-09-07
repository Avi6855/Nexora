package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/insights-service/internal/domain"
)

// EventHandler receives each raw Kafka message with its topic and event_type
// header (same shape as the notification/pot-service consumers).
type EventHandler func(ctx context.Context, topic, eventType string, value []byte) error

// capturedAuthorization mirrors the card-service authorization payload
// published to nexora.card.authorization.captured.
type capturedAuthorization struct {
	AuthorizationID string `json:"authorization_id"`
	CardID          string `json:"card_id"`
	UserID          string `json:"user_id"`
	AccountID       string `json:"account_id"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	Merchant        string `json:"merchant"`
	Decision        string `json:"decision"`
	CreatedAt       string `json:"created_at"`
}

// paymentEvent mirrors the payment-service outbox envelope payload (see
// BuildPaymentOutboxEvent: the domain payload nests under "payload").
type paymentEvent struct {
	PaymentID string `json:"payment_id"`
	UserID    string `json:"user_id"`
	AccountID string `json:"account_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference"`
}

// InsightsConsumer turns captured card spend into RecordSpend calls and
// settled payments into RecordIncome calls.
type InsightsConsumer struct {
	onSpend   func(ctx context.Context, ev *domain.SpendEvent) error
	onIncome  func(ctx context.Context, ev *domain.SpendEvent) error
	onSpendCheck func(ctx context.Context, ev *domain.SpendEvent) (bool, error)
	logger    zerolog.Logger
}

// NewInsightsConsumer wires the consumer to the intelligence service.
func NewInsightsConsumer(
	onSpend func(ctx context.Context, ev *domain.SpendEvent) error,
	onIncome func(ctx context.Context, ev *domain.SpendEvent) error,
	onSpendCheck func(ctx context.Context, ev *domain.SpendEvent) (bool, error),
	logger zerolog.Logger,
) *InsightsConsumer {
	return &InsightsConsumer{onSpend: onSpend, onIncome: onIncome, onSpendCheck: onSpendCheck, logger: logger}
}

// HandleEvent is the Kafka handler for the subscribed topics.
func (c *InsightsConsumer) HandleEvent(ctx context.Context, topic, eventType string, value []byte) error {
	switch {
	case strings.HasSuffix(eventType, "card.authorization.captured") || strings.HasSuffix(topic, "card.authorization.captured"):
		return c.handleCaptured(ctx, value)
	case strings.HasSuffix(eventType, "payment.confirmed") || strings.HasSuffix(eventType, "payment.settled") ||
		strings.HasSuffix(topic, "payment.confirmed") || strings.HasSuffix(topic, "payment.settled"):
		return c.handlePayment(ctx, value)
	default:
		c.logger.Debug().Str("event_type", eventType).Msg("ignoring event")
		return nil
	}
}

func (c *InsightsConsumer) handleCaptured(ctx context.Context, value []byte) error {
	var ev capturedAuthorization
	if err := json.Unmarshal(value, &ev); err != nil {
		return fmt.Errorf("invalid captured authorization payload: %w", err)
	}
	if ev.Amount <= 0 || ev.AccountID == "" {
		return nil
	}
	accountID, err := uuid.Parse(ev.AccountID)
	if err != nil {
		return fmt.Errorf("invalid account_id: %w", err)
	}
	userID, _ := uuid.Parse(ev.UserID)
	occurred := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339, ev.CreatedAt); err == nil {
		occurred = t
	}

	spend := &domain.SpendEvent{
		AccountID:   accountID,
		UserID:      userID,
		Amount:      ev.Amount,
		Currency:    ev.Currency,
		Description: ev.Merchant,
		Category:    "",
		OccurredAt:  occurred,
	}
	if err := c.onSpend(ctx, spend); err != nil {
		return err
	}
	if _, err := c.onSpendCheck(ctx, spend); err != nil {
		// Anomaly checking must never fail the stream position.
		c.logger.Error().Err(err).Msg("anomaly check failed")
	}
	return nil
}

func (c *InsightsConsumer) handlePayment(ctx context.Context, value []byte) error {
	var ev paymentEvent
	if err := json.Unmarshal(value, &ev); err != nil || ev.UserID == "" {
		var env struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(value, &env); err != nil {
			return fmt.Errorf("invalid payment event payload: %w", err)
		}
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			return fmt.Errorf("invalid payment event payload: %w", err)
		}
	}
	if ev.Amount <= 0 || ev.UserID == "" {
		return nil
	}
	userID, err := uuid.Parse(ev.UserID)
	if err != nil {
		return fmt.Errorf("invalid user_id: %w", err)
	}
	accountID, err := uuid.Parse(ev.AccountID)
	if err != nil {
		return fmt.Errorf("invalid account_id: %w", err)
	}

	return c.onIncome(ctx, &domain.SpendEvent{
		AccountID:   accountID,
		UserID:      userID,
		Amount:      ev.Amount,
		Currency:    ev.Currency,
		Description: ev.Reference,
		OccurredAt:  time.Now().UTC(),
	})
}
