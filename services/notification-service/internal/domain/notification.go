package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type NotificationType string

const (
	NotificationTypeTransaction  NotificationType = "TRANSACTION"
	NotificationTypeSecurity     NotificationType = "SECURITY"
	NotificationTypeFraud        NotificationType = "FRAUD"
	NotificationTypePromo        NotificationType = "PROMO"
	NotificationTypeSystem       NotificationType = "SYSTEM"
	NotificationTypePayment      NotificationType = "PAYMENT"
	NotificationTypeIncident     NotificationType = "INCIDENT"
)

type NotificationChannel string

const (
	NotificationChannelPush  NotificationChannel = "PUSH"
	NotificationChannelEmail NotificationChannel = "EMAIL"
	NotificationChannelSMS   NotificationChannel = "SMS"
	NotificationChannelInApp NotificationChannel = "IN_APP"
)

type NotificationStatus string

const (
	NotificationStatusPending NotificationStatus = "PENDING"
	NotificationStatusSent    NotificationStatus = "SENT"
	NotificationStatusRead    NotificationStatus = "READ"
	NotificationStatusFailed  NotificationStatus = "FAILED"
)

type Notification struct {
	NotificationID   uuid.UUID           `json:"notification_id"`
	UserID           uuid.UUID           `json:"user_id"`
	NotificationType NotificationType    `json:"notification_type"`
	Channel          NotificationChannel `json:"channel"`
	Title            string              `json:"title"`
	Body             string              `json:"body"`
	Metadata         json.RawMessage     `json:"metadata,omitempty"`
	Status           NotificationStatus  `json:"status"`
	CreatedAt        time.Time           `json:"created_at"`
	SentAt           *time.Time          `json:"sent_at,omitempty"`
	ReadAt           *time.Time          `json:"read_at,omitempty"`
}

func NewNotification(userID uuid.UUID, nType NotificationType, channel NotificationChannel, title, body string) *Notification {
	now := time.Now().UTC()
	return &Notification{
		NotificationID:   uuid.New(),
		UserID:           userID,
		NotificationType: nType,
		Channel:          channel,
		Title:            title,
		Body:             body,
		Status:           NotificationStatusPending,
		CreatedAt:        now,
	}
}

func NewNotificationWithMetadata(userID uuid.UUID, nType NotificationType, channel NotificationChannel, title, body string, metadata json.RawMessage) *Notification {
	n := NewNotification(userID, nType, channel, title, body)
	n.Metadata = metadata
	return n
}

type SendNotificationRequest struct {
	UserID           string             `json:"user_id"`
	NotificationType NotificationType   `json:"notification_type"`
	Channel          NotificationChannel `json:"channel"`
	Title            string             `json:"title"`
	Body             string             `json:"body"`
	Metadata         json.RawMessage    `json:"metadata,omitempty"`
}

type NotificationEvent struct {
	EventType string          `json:"event_type"`
	UserID    string          `json:"user_id"`
	Payload   json.RawMessage `json:"payload"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
