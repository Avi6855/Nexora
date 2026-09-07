package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/notification-service/internal/domain"
)

type NotificationRepository interface {
	Create(ctx context.Context, n *domain.Notification) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.Notification, error)
	MarkAsSent(ctx context.Context, n *domain.Notification) error
	MarkAsRead(ctx context.Context, n *domain.Notification) error
	MarkAsFailed(ctx context.Context, n *domain.Notification) error
}

type cassandraNotificationRepository struct {
	session *gocql.Session
}

func NewCassandraNotificationRepository(session *gocql.Session) NotificationRepository {
	return &cassandraNotificationRepository{session: session}
}

// toUUID converts google/uuid values (a named [16]byte array) to gocql.UUID,
// the only type gocql can marshal/unmarshal into CQL uuid columns.
func toUUID(id uuid.UUID) gocql.UUID {
	return gocql.UUID(id)
}

// metadataToText serialises the opaque metadata JSON document for the TEXT
// column. nil/empty raw messages are stored as NULL.
func metadataToText(md json.RawMessage) interface{} {
	if len(md) == 0 || string(md) == "null" {
		return nil
	}
	return string(md)
}

func metadataFromText(s *string) json.RawMessage {
	if s == nil || *s == "" {
		return nil
	}
	return json.RawMessage(*s)
}

// Create writes the notification to both the entity table (keyed by
// notification_id for point lookups and status updates) and the query
// projection notifications_by_user (keyed by (user_id, created_at DESC) for
// "recent notifications" reads). A logged batch keeps them consistent.
func (r *cassandraNotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	batch := gocql.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	batch.Query(`INSERT INTO notifications (notification_id, user_id, notification_type, channel, title, body, metadata, status, created_at, sent_at, read_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		toUUID(n.NotificationID), toUUID(n.UserID),
		string(n.NotificationType), string(n.Channel),
		n.Title, n.Body, metadataToText(n.Metadata),
		string(n.Status), n.CreatedAt, n.SentAt, n.ReadAt)

	batch.Query(`INSERT INTO notifications_by_user (user_id, created_at, notification_id, notification_type, channel, title, body, metadata, status, sent_at, read_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		toUUID(n.UserID), n.CreatedAt, toUUID(n.NotificationID),
		string(n.NotificationType), string(n.Channel),
		n.Title, n.Body, metadataToText(n.Metadata),
		string(n.Status), n.SentAt, n.ReadAt)

	return r.session.ExecuteBatch(batch)
}

func (r *cassandraNotificationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	var n domain.Notification
	var notificationID, userID gocql.UUID
	var nType, channel, status string
	var metadata *string
	var sentAt, readAt *time.Time
	query := `SELECT notification_id, user_id, notification_type, channel, title, body, metadata, status, created_at, sent_at, read_at
		FROM notifications WHERE notification_id = ?`
	err := r.session.Query(query, toUUID(id)).WithContext(ctx).Scan(
		&notificationID, &userID, &nType, &channel,
		&n.Title, &n.Body, &metadata, &status,
		&n.CreatedAt, &sentAt, &readAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("notification not found")
	}
	if err != nil {
		return nil, err
	}
	n.NotificationID = uuid.UUID(notificationID)
	n.UserID = uuid.UUID(userID)
	n.NotificationType = domain.NotificationType(nType)
	n.Channel = domain.NotificationChannel(channel)
	n.Status = domain.NotificationStatus(status)
	n.Metadata = metadataFromText(metadata)
	n.SentAt = sentAt
	n.ReadAt = readAt
	return &n, nil
}

func (r *cassandraNotificationRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	var notifications []*domain.Notification
	query := `SELECT notification_id, user_id, notification_type, channel, title, body, metadata, status, created_at, sent_at, read_at
		FROM notifications_by_user WHERE user_id = ? LIMIT ?`
	iter := r.session.Query(query, toUUID(userID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	var notificationID, userIDCol gocql.UUID
	var nType, channel, status, title, body string
	var metadata *string
	var createdAt time.Time
	var sentAt, readAt *time.Time

	for iter.Scan(
		&notificationID, &userIDCol, &nType, &channel,
		&title, &body, &metadata, &status,
		&createdAt, &sentAt, &readAt,
	) {
		n := &domain.Notification{
			NotificationID:   uuid.UUID(notificationID),
			UserID:           uuid.UUID(userIDCol),
			NotificationType: domain.NotificationType(nType),
			Channel:          domain.NotificationChannel(channel),
			Title:            title,
			Body:             body,
			Metadata:         metadataFromText(metadata),
			Status:           domain.NotificationStatus(status),
			CreatedAt:        createdAt,
			SentAt:           sentAt,
			ReadAt:           readAt,
		}
		notifications = append(notifications, n)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return notifications, nil
}

func (r *cassandraNotificationRepository) MarkAsSent(ctx context.Context, n *domain.Notification) error {
	now := time.Now().UTC()
	batch := gocql.NewBatch(gocql.LoggedBatch).WithContext(ctx)
	batch.Query(`UPDATE notifications SET status = 'SENT', sent_at = ? WHERE notification_id = ?`, now, toUUID(n.NotificationID))
	batch.Query(`UPDATE notifications_by_user SET status = 'SENT', sent_at = ?
		WHERE user_id = ? AND created_at = ? AND notification_id = ?`,
		now, toUUID(n.UserID), n.CreatedAt, toUUID(n.NotificationID))
	return r.session.ExecuteBatch(batch)
}

func (r *cassandraNotificationRepository) MarkAsRead(ctx context.Context, n *domain.Notification) error {
	now := time.Now().UTC()
	batch := gocql.NewBatch(gocql.LoggedBatch).WithContext(ctx)
	batch.Query(`UPDATE notifications SET status = 'READ', read_at = ? WHERE notification_id = ?`, now, toUUID(n.NotificationID))
	batch.Query(`UPDATE notifications_by_user SET status = 'READ', read_at = ?
		WHERE user_id = ? AND created_at = ? AND notification_id = ?`,
		now, toUUID(n.UserID), n.CreatedAt, toUUID(n.NotificationID))
	return r.session.ExecuteBatch(batch)
}

func (r *cassandraNotificationRepository) MarkAsFailed(ctx context.Context, n *domain.Notification) error {
	batch := gocql.NewBatch(gocql.LoggedBatch).WithContext(ctx)
	batch.Query(`UPDATE notifications SET status = 'FAILED' WHERE notification_id = ?`, toUUID(n.NotificationID))
	batch.Query(`UPDATE notifications_by_user SET status = 'FAILED'
		WHERE user_id = ? AND created_at = ? AND notification_id = ?`,
		toUUID(n.UserID), n.CreatedAt, toUUID(n.NotificationID))
	return r.session.ExecuteBatch(batch)
}
