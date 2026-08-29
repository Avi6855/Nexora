package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/audit-service/internal/domain"
)

type AuditRepository interface {
	RecordEvent(ctx context.Context, event *domain.AuditEvent) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.AuditEvent, error)
	GetByResource(ctx context.Context, resourceType, resourceID string, limit int) ([]*domain.AuditEvent, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.AuditEvent, error)
	GetByUserIDLogs(ctx context.Context, userID string, limit int) ([]*domain.AuditEvent, error)
	Create(ctx context.Context, log *domain.AuditLog) error
	GetLogByID(ctx context.Context, id uuid.UUID) (*domain.AuditLog, error)
	GetLogsByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.AuditLog, error)
	GetLogsByResource(ctx context.Context, resourceType, resourceID string) ([]*domain.AuditLog, error)
}

type cassandraAuditRepository struct {
	session *gocql.Session
}

func NewCassandraAuditRepository(session *gocql.Session) AuditRepository {
	return &cassandraAuditRepository{session: session}
}

func (r *cassandraAuditRepository) RecordEvent(ctx context.Context, event *domain.AuditEvent) error {
	query := `INSERT INTO audit_events (event_id, actor, action, resource_type, resource_id, details, request_id, trace_id, ip_address, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		event.EventID, event.Actor, string(event.Action),
		event.ResourceType, event.ResourceID, event.Details,
		event.RequestID, event.TraceID,
		event.IPAddress, event.UserAgent, event.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraAuditRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.AuditEvent, error) {
	var event domain.AuditEvent
	var action string
	query := `SELECT event_id, actor, action, resource_type, resource_id, details, request_id, trace_id, ip_address, user_agent, created_at
		FROM audit_events WHERE event_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&event.EventID, &event.Actor, &action,
		&event.ResourceType, &event.ResourceID, &event.Details,
		&event.RequestID, &event.TraceID,
		&event.IPAddress, &event.UserAgent, &event.CreatedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("audit event not found")
	}
	if err != nil {
		return nil, err
	}
	event.Action = domain.AuditAction(action)
	return &event, nil
}

func (r *cassandraAuditRepository) GetByResource(ctx context.Context, resourceType, resourceID string, limit int) ([]*domain.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var events []*domain.AuditEvent
	query := `SELECT event_id, actor, action, resource_type, resource_id, details, request_id, trace_id, ip_address, user_agent, created_at
		FROM audit_events WHERE resource_type = ? AND resource_id = ? LIMIT ? ALLOW FILTERING`
	iter := r.session.Query(query, resourceType, resourceID, limit).WithContext(ctx).Iter()
	defer iter.Close()
	var event domain.AuditEvent
	var action string
	for iter.Scan(
		&event.EventID, &event.Actor, &action,
		&event.ResourceType, &event.ResourceID, &event.Details,
		&event.RequestID, &event.TraceID,
		&event.IPAddress, &event.UserAgent, &event.CreatedAt,
	) {
		event.Action = domain.AuditAction(action)
		e := event
		events = append(events, &e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *cassandraAuditRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var events []*domain.AuditEvent
	query := `SELECT event_id, actor, action, resource_type, resource_id, details, request_id, trace_id, ip_address, user_agent, created_at
		FROM audit_events WHERE actor = ? LIMIT ? ALLOW FILTERING`
	iter := r.session.Query(query, userID.String(), limit).WithContext(ctx).Iter()
	defer iter.Close()
	var event domain.AuditEvent
	var action string
	for iter.Scan(
		&event.EventID, &event.Actor, &action,
		&event.ResourceType, &event.ResourceID, &event.Details,
		&event.RequestID, &event.TraceID,
		&event.IPAddress, &event.UserAgent, &event.CreatedAt,
	) {
		event.Action = domain.AuditAction(action)
		e := event
		events = append(events, &e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *cassandraAuditRepository) Create(ctx context.Context, log *domain.AuditLog) error {
	query := `INSERT INTO audit_logs (log_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query, log.LogID, log.UserID, log.Action, log.ResourceType, log.ResourceID, log.Details, log.IPAddress, log.UserAgent, log.CreatedAt).WithContext(ctx).Exec()
}

func (r *cassandraAuditRepository) GetLogByID(ctx context.Context, id uuid.UUID) (*domain.AuditLog, error) {
	var log domain.AuditLog
	query := `SELECT log_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at FROM audit_logs WHERE log_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(&log.LogID, &log.UserID, &log.Action, &log.ResourceType, &log.ResourceID, &log.Details, &log.IPAddress, &log.UserAgent, &log.CreatedAt)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("audit log not found")
	}
	if err != nil {
		return nil, err
	}
	return &log, nil
}

func (r *cassandraAuditRepository) GetLogsByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	var logs []*domain.AuditLog
	query := `SELECT log_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at FROM audit_logs WHERE user_id = ? LIMIT ?`
	iter := r.session.Query(query, userID, limit).WithContext(ctx).Iter()
	defer iter.Close()
	var log domain.AuditLog
	for iter.Scan(&log.LogID, &log.UserID, &log.Action, &log.ResourceType, &log.ResourceID, &log.Details, &log.IPAddress, &log.UserAgent, &log.CreatedAt) {
		l := log
		logs = append(logs, &l)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (r *cassandraAuditRepository) GetLogsByResource(ctx context.Context, resourceType, resourceID string) ([]*domain.AuditLog, error) {
	var logs []*domain.AuditLog
	query := `SELECT log_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at FROM audit_logs WHERE resource_type = ? AND resource_id = ? ALLOW FILTERING`
	iter := r.session.Query(query, resourceType, resourceID).WithContext(ctx).Iter()
	defer iter.Close()
	var log domain.AuditLog
	for iter.Scan(&log.LogID, &log.UserID, &log.Action, &log.ResourceType, &log.ResourceID, &log.Details, &log.IPAddress, &log.UserAgent, &log.CreatedAt) {
		l := log
		logs = append(logs, &l)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (r *cassandraAuditRepository) GetByUserIDLogs(ctx context.Context, userID string, limit int) ([]*domain.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var events []*domain.AuditEvent
	query := `SELECT event_id, actor, action, resource_type, resource_id, details, request_id, trace_id, ip_address, user_agent, created_at
		FROM audit_events WHERE actor = ? LIMIT ? ALLOW FILTERING`
	iter := r.session.Query(query, userID, limit).WithContext(ctx).Iter()
	defer iter.Close()
	var event domain.AuditEvent
	var action string
	for iter.Scan(
		&event.EventID, &event.Actor, &action,
		&event.ResourceType, &event.ResourceID, &event.Details,
		&event.RequestID, &event.TraceID,
		&event.IPAddress, &event.UserAgent, &event.CreatedAt,
	) {
		event.Action = domain.AuditAction(action)
		e := event
		events = append(events, &e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return events, nil
}
