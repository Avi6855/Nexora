package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/notification-service/internal/domain"
	"github.com/nexora/nexora/services/notification-service/internal/events"
	"github.com/nexora/nexora/services/notification-service/internal/realtime"
	"github.com/nexora/nexora/services/notification-service/internal/repository"
)

type NotificationService struct {
	notifRepo repository.NotificationRepository
	producer  *events.KafkaProducer
	hub       *realtime.Hub
	logger    zerolog.Logger
}

func NewNotificationService(notifRepo repository.NotificationRepository, producer *events.KafkaProducer, hub *realtime.Hub, logger zerolog.Logger) *NotificationService {
	return &NotificationService{notifRepo: notifRepo, producer: producer, hub: hub, logger: logger}
}

func (s *NotificationService) SendNotification(ctx context.Context, req *domain.SendNotificationRequest) (*domain.Notification, error) {
	s.logger.Info().Str("user_id", req.UserID).Str("type", string(req.NotificationType)).Msg("sending notification")
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}
	notif := domain.NewNotification(userID, req.NotificationType, req.Channel, req.Title, req.Body)
	if req.Metadata != nil {
		notif.Metadata = req.Metadata
	}

	if err := s.persistAndPush(ctx, notif); err != nil {
		return nil, err
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "notification.sent", notif)
	}

	return notif, nil
}

// persistAndPush is the single write path for every notification: durable row
// in Cassandra first, then real-time fan-out to the user's SSE connections.
func (s *NotificationService) persistAndPush(ctx context.Context, notif *domain.Notification) error {
	if err := s.notifRepo.Create(ctx, notif); err != nil {
		return fmt.Errorf("storing notification: %w", err)
	}
	now := time.Now().UTC()
	notif.Status = domain.NotificationStatusSent
	notif.SentAt = &now
	if err := s.notifRepo.MarkAsSent(ctx, notif); err != nil {
		s.logger.Warn().Err(err).Str("notification_id", notif.NotificationID.String()).Msg("failed to mark notification as sent")
	}

	if s.hub != nil {
		payload, err := json.Marshal(map[string]interface{}{
			"kind":         "feed_item",
			"notification": notif,
		})
		if err == nil {
			s.hub.Publish(notif.UserID, payload)
		}
	}
	return nil
}

// GetFeed returns the mobile home-screen feed: real notification rows (each
// originated from a real event and was persisted) enriched from metadata.
func (s *NotificationService) GetFeed(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.FeedItem, error) {
	notifs, err := s.notifRepo.GetByUserID(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	items := make([]*domain.FeedItem, 0, len(notifs))
	for _, n := range notifs {
		items = append(items, domain.FeedItemFromNotification(n))
	}
	return items, nil
}

func (s *NotificationService) GetNotification(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	return s.notifRepo.GetByID(ctx, id)
}

// Subscribe exposes the realtime hub for SSE connections.
func (s *NotificationService) Subscribe(userID uuid.UUID) (<-chan []byte, func()) {
	if s.hub == nil {
		ch := make(chan []byte)
		return ch, func() {}
	}
	return s.hub.Subscribe(userID)
}

func (s *NotificationService) GetNotificationsByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.notifRepo.GetByUserID(ctx, userID, limit)
}

func (s *NotificationService) MarkAsRead(ctx context.Context, id uuid.UUID) error {
	n, err := s.notifRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	n.Status = domain.NotificationStatusRead
	n.ReadAt = &now
	return s.notifRepo.MarkAsRead(ctx, n)
}

func (s *NotificationService) ConsumePaymentEvent(ctx context.Context, event *domain.NotificationEvent) error {
	s.logger.Info().Str("event_type", event.EventType).Str("user_id", event.UserID).Msg("consuming payment event")

	userID, err := uuid.Parse(event.UserID)
	if err != nil {
		return fmt.Errorf("invalid user ID in event: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("invalid event payload: %w", err)
	}

	var title, body string
	nType := domain.NotificationTypePayment

	switch event.EventType {
	case "PAYMENT_CREATED":
		title = "Payment Initiated"
		body = fmt.Sprintf("Your payment of £%.2f is being processed", toPence(payload["amount"]))
	case "PAYMENT_CONFIRMED":
		title = "Payment Confirmed"
		body = fmt.Sprintf("Your payment of £%.2f has been confirmed", toPence(payload["amount"]))
	case "PAYMENT_SETTLED":
		title = "Payment Settled"
		body = fmt.Sprintf("Your payment of £%.2f has been settled", toPence(payload["amount"]))
	case "PAYMENT_FAILED":
		title = "Payment Failed"
		body = "Your payment could not be processed. Please try again."
	case "PAYMENT_REVERSED":
		title = "Payment Reversed"
		body = fmt.Sprintf("Your payment of £%.2f has been reversed", toPence(payload["amount"]))
	default:
		return nil
	}

	metadata, _ := json.Marshal(payload)
	notif := domain.NewNotificationWithMetadata(userID, nType, domain.NotificationChannelPush, title, body, metadata)
	return s.persistAndPush(ctx, notif)
}

func (s *NotificationService) ConsumeFraudEvent(ctx context.Context, event *domain.NotificationEvent) error {
	s.logger.Info().Str("event_type", event.EventType).Str("user_id", event.UserID).Msg("consuming fraud event")

	userID, err := uuid.Parse(event.UserID)
	if err != nil {
		return fmt.Errorf("invalid user ID in event: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("invalid event payload: %w", err)
	}

	title := "Security Alert"
	body := "Unusual activity detected on your account. Please review."
	nType := domain.NotificationTypeFraud

	metadata, _ := json.Marshal(payload)
	notif := domain.NewNotificationWithMetadata(userID, nType, domain.NotificationChannelPush, title, body, metadata)
	if err := s.persistAndPush(ctx, notif); err != nil {
		return err
	}

	// Also fan out an EMAIL channel copy for the webhook-style API.
	emailNotif := domain.NewNotificationWithMetadata(userID, nType, domain.NotificationChannelEmail, title, body, metadata)
	if err := s.persistAndPush(ctx, emailNotif); err != nil {
		s.logger.Warn().Err(err).Msg("failed to store email notification")
	}

	return nil
}

func (s *NotificationService) ConsumeSecurityEvent(ctx context.Context, event *domain.NotificationEvent) error {
	s.logger.Info().Str("event_type", event.EventType).Str("user_id", event.UserID).Msg("consuming security event")

	userID, err := uuid.Parse(event.UserID)
	if err != nil {
		return fmt.Errorf("invalid user ID in event: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("invalid event payload: %w", err)
	}

	title := "Security Notification"
	nType := domain.NotificationTypeSecurity

	var body string
	switch event.EventType {
	case "LOGIN_NEW_DEVICE":
		title = "New Device Login"
		body = "A new device was used to sign in to your account."
	case "PASSWORD_CHANGED":
		title = "Password Changed"
		body = "Your password was recently changed."
	case "ACCOUNT_LOCKED":
		title = "Account Locked"
		body = "Your account has been locked due to suspicious activity."
	default:
		body = "A security event occurred on your account."
	}

	metadata, _ := json.Marshal(payload)
	notif := domain.NewNotificationWithMetadata(userID, nType, domain.NotificationChannelPush, title, body, metadata)
	return s.persistAndPush(ctx, notif)
}

func toPence(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val / 100.0
	case int64:
		return float64(val) / 100.0
	case int:
		return float64(val) / 100.0
	default:
		return 0
	}
}
