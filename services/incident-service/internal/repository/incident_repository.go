package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/incident-service/internal/domain"
)

type IncidentRepository interface {
	Create(ctx context.Context, incident *domain.Incident) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Incident, error)
	GetByStatus(ctx context.Context, status domain.IncidentStatus) ([]*domain.Incident, error)
	Update(ctx context.Context, incident *domain.Incident) error
	AddTimelineEvent(ctx context.Context, event *domain.IncidentEvent) error
	GetTimeline(ctx context.Context, incidentID uuid.UUID) ([]*domain.IncidentEvent, error)
	GetRecentByService(ctx context.Context, serviceName string, since time.Time) ([]*domain.Incident, error)
}

type cassandraIncidentRepository struct {
	session *gocql.Session
}

func NewCassandraIncidentRepository(session *gocql.Session) IncidentRepository {
	return &cassandraIncidentRepository{session: session}
}

func (r *cassandraIncidentRepository) Create(ctx context.Context, incident *domain.Incident) error {
	query := `INSERT INTO incidents (incident_id, title, description, severity, status, affected_services, created_by, assigned_to, resolution, created_at, updated_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		incident.IncidentID, incident.Title, incident.Description,
		string(incident.Severity), string(incident.Status),
		incident.AffectedServices, incident.CreatedBy,
		incident.AssignedTo, incident.Resolution,
		incident.CreatedAt, incident.UpdatedAt, incident.ResolvedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraIncidentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Incident, error) {
	var incident domain.Incident
	var severity, status string
	query := `SELECT incident_id, title, description, severity, status, affected_services, created_by, assigned_to, resolution, created_at, updated_at, resolved_at
		FROM incidents WHERE incident_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&incident.IncidentID, &incident.Title, &incident.Description,
		&severity, &status, &incident.AffectedServices,
		&incident.CreatedBy, &incident.AssignedTo, &incident.Resolution,
		&incident.CreatedAt, &incident.UpdatedAt, &incident.ResolvedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("incident not found")
	}
	if err != nil {
		return nil, err
	}
	incident.Severity = domain.IncidentSeverity(severity)
	incident.Status = domain.IncidentStatus(status)
	return &incident, nil
}

func (r *cassandraIncidentRepository) GetByStatus(ctx context.Context, status domain.IncidentStatus) ([]*domain.Incident, error) {
	var incidents []*domain.Incident
	query := `SELECT incident_id, title, description, severity, status, affected_services, created_by, assigned_to, resolution, created_at, updated_at, resolved_at
		FROM incidents WHERE status = ? ALLOW FILTERING`
	iter := r.session.Query(query, string(status)).WithContext(ctx).Iter()
	defer iter.Close()
	var incident domain.Incident
	var severity, st string
	for iter.Scan(
		&incident.IncidentID, &incident.Title, &incident.Description,
		&severity, &st, &incident.AffectedServices,
		&incident.CreatedBy, &incident.AssignedTo, &incident.Resolution,
		&incident.CreatedAt, &incident.UpdatedAt, &incident.ResolvedAt,
	) {
		incident.Severity = domain.IncidentSeverity(severity)
		incident.Status = domain.IncidentStatus(st)
		ii := incident
		incidents = append(incidents, &ii)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return incidents, nil
}

func (r *cassandraIncidentRepository) Update(ctx context.Context, incident *domain.Incident) error {
	now := time.Now().UTC()
	query := `UPDATE incidents SET severity = ?, status = ?, resolution = ?, updated_at = ?, resolved_at = ? WHERE incident_id = ?`
	return r.session.Query(query,
		string(incident.Severity), string(incident.Status),
		incident.Resolution, now, incident.ResolvedAt,
		incident.IncidentID,
	).WithContext(ctx).Exec()
}

func (r *cassandraIncidentRepository) AddTimelineEvent(ctx context.Context, event *domain.IncidentEvent) error {
	query := `INSERT INTO incident_timeline (incident_id, event_id, event_type, description, actor, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		event.IncidentID, event.EventID, event.EventType,
		event.Description, event.Actor, event.Timestamp,
	).WithContext(ctx).Exec()
}

func (r *cassandraIncidentRepository) GetTimeline(ctx context.Context, incidentID uuid.UUID) ([]*domain.IncidentEvent, error) {
	var events []*domain.IncidentEvent
	query := `SELECT incident_id, event_id, event_type, description, actor, timestamp
		FROM incident_timeline WHERE incident_id = ?`
	iter := r.session.Query(query, incidentID).WithContext(ctx).Iter()
	defer iter.Close()
	var event domain.IncidentEvent
	for iter.Scan(
		&event.IncidentID, &event.EventID, &event.EventType,
		&event.Description, &event.Actor, &event.Timestamp,
	) {
		e := event
		events = append(events, &e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *cassandraIncidentRepository) GetRecentByService(ctx context.Context, serviceName string, since time.Time) ([]*domain.Incident, error) {
	var incidents []*domain.Incident
	query := `SELECT incident_id, title, description, severity, status, affected_services, created_by, assigned_to, resolution, created_at, updated_at, resolved_at
		FROM incidents WHERE created_at >= ? ALLOW FILTERING`
	iter := r.session.Query(query, since).WithContext(ctx).Iter()
	defer iter.Close()
	var incident domain.Incident
	var severity, status string
	for iter.Scan(
		&incident.IncidentID, &incident.Title, &incident.Description,
		&severity, &status, &incident.AffectedServices,
		&incident.CreatedBy, &incident.AssignedTo, &incident.Resolution,
		&incident.CreatedAt, &incident.UpdatedAt, &incident.ResolvedAt,
	) {
		incident.Severity = domain.IncidentSeverity(severity)
		incident.Status = domain.IncidentStatus(status)
		for _, svc := range incident.AffectedServices {
			if svc == serviceName {
				ii := incident
				incidents = append(incidents, &ii)
				break
			}
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return incidents, nil
}
