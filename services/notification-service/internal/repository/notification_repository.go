package repository

import (
	"context"
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
	MarkAsSent(ctx context.Context, id uuid.UUID) error
	MarkAsRead(ctx context.Context, id uuid.UUID) error
	MarkAsFailed(ctx context.Context, id uuid.UUID) error
}

type cassandraNotificationRepository struct {
	session *gocql.Session
}

func NewCassandraNotificationRepository(session *gocql.Session) NotificationRepository {
	return &cassandraNotificationRepository{session: session}
}

func (r *cassandraNotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	query := `INSERT INTO notifications (notification_id, user_id, notification_type, channel, title, body, metadata, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		n.NotificationID, n.UserID,
		string(n.NotificationType), string(n.Channel),
		n.Title, n.Body, n.Metadata,
		string(n.Status), n.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraNotificationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	var n domain.Notification
	var nType, channel, status string
	query := `SELECT notification_id, user_id, notification_type, channel, title, body, metadata, status, created_at, sent_at, read_at
		FROM notifications WHERE notification_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&n.NotificationID, &n.UserID, &nType, &channel,
		&n.Title, &n.Body, &n.Metadata, &status,
		&n.CreatedAt, &n.SentAt, &n.ReadAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("notification not found")
	}
	if err != nil {
		return nil, err
	}
	n.NotificationType = domain.NotificationType(nType)
	n.Channel = domain.NotificationChannel(channel)
	n.Status = domain.NotificationStatus(status)
	return &n, nil
}

func (r *cassandraNotificationRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	var notifications []*domain.Notification
	query := `SELECT notification_id, user_id, notification_type, channel, title, body, metadata, status, created_at, sent_at, read_at
		FROM notifications WHERE user_id = ? LIMIT ?`
	iter := r.session.Query(query, userID, limit).WithContext(ctx).Iter()
	defer iter.Close()
	var n domain.Notification
	var nType, channel, status string
	for iter.Scan(
		&n.NotificationID, &n.UserID, &nType, &channel,
		&n.Title, &n.Body, &n.Metadata, &status,
		&n.CreatedAt, &n.SentAt, &n.ReadAt,
	) {
		n.NotificationType = domain.NotificationType(nType)
		n.Channel = domain.NotificationChannel(channel)
		n.Status = domain.NotificationStatus(status)
		nn := n
		notifications = append(notifications, &nn)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return notifications, nil
}

func (r *cassandraNotificationRepository) MarkAsSent(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE notifications SET status = 'SENT', sent_at = ? WHERE notification_id = ?`
	return r.session.Query(query, now, id).WithContext(ctx).Exec()
}

func (r *cassandraNotificationRepository) MarkAsRead(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE notifications SET status = 'READ', read_at = ? WHERE notification_id = ?`
	return r.session.Query(query, now, id).WithContext(ctx).Exec()
}

func (r *cassandraNotificationRepository) MarkAsFailed(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE notifications SET status = 'FAILED' WHERE notification_id = ?`
	return r.session.Query(query, id).WithContext(ctx).Exec()
}
