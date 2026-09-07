package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/notification-service/internal/domain"
)

// CardAuthorizationEvent mirrors the payload published by card-service to
// nexora.card.authorization.* (see card-service events.AuthorizationEvent).
type CardAuthorizationEvent struct {
	AuthorizationID string  `json:"authorization_id"`
	CardID          string  `json:"card_id"`
	UserID          string  `json:"user_id"`
	AccountID       string  `json:"account_id"`
	Amount          int64   `json:"amount"`
	Currency        string  `json:"currency"`
	Merchant        string  `json:"merchant"`
	MerchantCity    string  `json:"merchant_city"`
	MerchantCountry string  `json:"merchant_country"`
	TerminalID      string  `json:"terminal_id"`
	Status          string  `json:"status"`
	Decision        string  `json:"decision"`
	DeclineReason   string  `json:"decline_reason"`
	RiskScore       float64 `json:"risk_score"`
	RiskReasons     string  `json:"risk_reasons"`
	ReservationID   string  `json:"reservation_id"`
	BalanceAfter    *int64  `json:"balance_after"`
	LatencyMs       int64   `json:"latency_ms"`
	CreatedAt       string  `json:"created_at"`
}

// PaymentLifecycleEvent mirrors the payment-service payload for
// payment.confirmed / payment.settled (inbound money in this demo topology).
type PaymentLifecycleEvent struct {
	PaymentID string `json:"payment_id"`
	UserID    string `json:"user_id"`
	AccountID string `json:"account_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference"`
}

// HandleRealtimeEvent is invoked by the Kafka consumer for every message on
// the subscribed topics. It maps the real event to a push/feed notification,
// persists it and fans it out over SSE.
func (s *NotificationService) HandleRealtimeEvent(ctx context.Context, topic, eventType string, value []byte) error {
	switch {
	case eventType == "card.authorization.approved" || eventType == "card.authorization.declined" ||
		eventType == "card.authorization.captured" || eventType == "card.authorization.voided":
		return s.handleCardEvent(ctx, eventType, value)
	case eventType == "payment.confirmed" || eventType == "payment.settled":
		return s.handleMoneyInEvent(ctx, eventType, value)
	case eventType == "insight.alert" || strings.HasSuffix(topic, "insights.alerts"):
		return s.handleInsightAlert(ctx, value)
	default:
		s.logger.Debug().Str("event_type", eventType).Msg("ignoring event type")
		return nil
	}
}

// InsightAlertEvent mirrors the insights-service alert payload published to
// nexora.insights.alerts (see insights-service events.AlertProducer).
type InsightAlertEvent struct {
	AccountID string `json:"account_id"`
	AlertID   string `json:"alert_id"`
	AlertType string `json:"alert_type"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Payload   string `json:"payload"`
	CreatedAt string `json:"created_at"`
}

// handleInsightAlert turns a Financial Intelligence Platform alert (new
// subscription, price hike, income detected, spending anomaly) into a push
// notification + SSE event so the user sees it in the app feed live.
func (s *NotificationService) handleInsightAlert(ctx context.Context, value []byte) error {
	var ev InsightAlertEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		return fmt.Errorf("invalid insight alert payload: %w", err)
	}
	if ev.Title == "" || ev.Body == "" {
		return fmt.Errorf("insight alert missing title or body")
	}
	var userID uuid.UUID
	// Alerts carry the owning user in metadata (insights includes it in the
	// payload document); fall back to the account UUID until user linkage
	// lands — feed reads are account-scoped in the app today.
	var meta map[string]interface{}
	if ev.Payload != "" {
		_ = json.Unmarshal([]byte(ev.Payload), &meta)
	}
	if uidStr, ok := meta["user_id"].(string); ok && uidStr != "" {
		userID, err := uuid.Parse(uidStr)
		if err == nil {
			md, _ := json.Marshal(map[string]interface{}{
				"alert_id":   ev.AlertID,
				"alert_type": ev.AlertType,
				"account_id": ev.AccountID,
			})
			notif := domain.NewNotificationWithMetadata(userID, domain.NotificationTypeSystem, domain.NotificationChannelPush, ev.Title, ev.Body, md)
			return s.persistAndPush(ctx, notif)
		}
	}
	_ = userID
	// Without a routable user the alert is still visible via the Insights
	// screen's own alert store; skip the feed fan-out.
	s.logger.Debug().Str("alert_type", ev.AlertType).Msg("insight alert without user_id; skipping feed push")
	return nil
}

func (s *NotificationService) handleCardEvent(ctx context.Context, eventType string, value []byte) error {
	var ev CardAuthorizationEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		return fmt.Errorf("invalid card event payload: %w", err)
	}
	userID, err := uuid.Parse(ev.UserID)
	if err != nil {
		return fmt.Errorf("invalid user_id in card event: %w", err)
	}
	if ev.Amount == 0 || ev.Merchant == "" {
		return fmt.Errorf("card event missing amount or merchant")
	}

	meta := map[string]interface{}{
		"authorization_id": ev.AuthorizationID,
		"account_id":       ev.AccountID,
		"card_id":          ev.CardID,
		"amount":           ev.Amount,
		"currency":         ev.Currency,
		"merchant":         ev.Merchant,
		"merchant_city":    ev.MerchantCity,
		"latency_ms":       ev.LatencyMs,
	}
	if ev.BalanceAfter != nil {
		meta["balance_after"] = *ev.BalanceAfter
	}

	location := ev.MerchantCity
	if location == "" {
		location = ev.MerchantCountry
	}
	amountStr := pounds(ev.Amount, ev.Currency)
	merchant := ev.Merchant
	if location != "" {
		merchant = merchant + " · " + location
	}

	title, body := "", ""
	nType := domain.NotificationTypeTransaction

	switch eventType {
	case "card.authorization.approved":
		title = "Card payment"
		body = fmt.Sprintf("%s at %s", amountStr, merchant)
	case "card.authorization.captured":
		title = "Payment settled at " + ev.Merchant
		if ev.BalanceAfter != nil {
			body = fmt.Sprintf("%s · New balance %s", amountStr, pounds(*ev.BalanceAfter, ev.Currency))
		} else {
			body = fmt.Sprintf("%s", amountStr)
		}
	case "card.authorization.voided":
		title = "Payment voided"
		body = fmt.Sprintf("%s returned to your account at %s", amountStr, ev.Merchant)
	case "card.authorization.declined":
		switch ev.DeclineReason {
		case "insufficient_funds":
			title = "Card payment declined"
			body = fmt.Sprintf("Couldn't authorise %s at %s — not enough money in this account", amountStr, merchant)
		case "card_not_active":
			title = "Card payment declined"
			body = fmt.Sprintf("Your card was declined at %s — the card is not active", merchant)
		default:
			// Risk declines are surfaced as security alerts, like Monzo.
			title = "Security alert"
			body = fmt.Sprintf("We declined %s at %s as a precaution. If this was you, check your card settings", amountStr, merchant)
			nType = domain.NotificationTypeSecurity
			meta["risk_score"] = ev.RiskScore
			meta["decline_reason"] = ev.DeclineReason
		}
	}

	md, _ := json.Marshal(meta)
	notif := domain.NewNotificationWithMetadata(userID, nType, domain.NotificationChannelPush, title, body, md)
	return s.persistAndPush(ctx, notif)
}

func (s *NotificationService) handleMoneyInEvent(ctx context.Context, eventType string, value []byte) error {
	var ev PaymentLifecycleEvent
	if err := json.Unmarshal(value, &ev); err != nil || ev.UserID == "" {
		// payment-service publishes an envelope with the domain payload nested
		// under `payload` (see BuildPaymentOutboxEvent) — unwrap it first.
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
	if ev.UserID == "" {
		// Payments without a user (older events) cannot be routed to a feed.
		s.logger.Debug().Str("event_type", eventType).Str("payment_id", ev.PaymentID).Msg("skipping payment event without user_id")
		return nil
	}
	userID, err := uuid.Parse(ev.UserID)
	if err != nil {
		return fmt.Errorf("invalid user_id in payment event: %w", err)
	}

	meta, _ := json.Marshal(map[string]interface{}{
		"payment_id":  ev.PaymentID,
		"account_id":  ev.AccountID,
		"amount":      ev.Amount,
		"currency":    ev.Currency,
		"merchant":    strings.TrimSpace(ev.Reference),
	})
	title := "Money in"
	body := fmt.Sprintf("%s credited to your account", pounds(ev.Amount, ev.Currency))
	notif := domain.NewNotificationWithMetadata(userID, domain.NotificationTypeTransaction, domain.NotificationChannelPush, title, body, meta)
	return s.persistAndPush(ctx, notif)
}

func pounds(minor int64, currency string) string {
	symbol := "£"
	if currency != "" && currency != "GBP" {
		symbol = currency + " "
	}
	return fmt.Sprintf("%s%.2f", symbol, float64(minor)/100.0)
}
