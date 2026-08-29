package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/incident-service/internal/domain"
	"github.com/nexora/nexora/services/incident-service/internal/events"
	"github.com/nexora/nexora/services/incident-service/internal/repository"
)

type IncidentService struct {
	incidentRepo repository.IncidentRepository
	producer     *events.KafkaProducer
	logger       zerolog.Logger
}

func NewIncidentService(incidentRepo repository.IncidentRepository, producer *events.KafkaProducer, logger zerolog.Logger) *IncidentService {
	return &IncidentService{incidentRepo: incidentRepo, producer: producer, logger: logger}
}

func (s *IncidentService) CreateIncident(ctx context.Context, req *domain.CreateIncidentRequest, createdBy uuid.UUID) (*domain.Incident, error) {
	s.logger.Info().Str("title", req.Title).Msg("creating incident")
	now := time.Now().UTC()
	incident := &domain.Incident{
		IncidentID:       uuid.New(),
		Title:            req.Title,
		Description:      req.Description,
		Severity:         req.Severity,
		Status:           domain.IncidentStatusOpen,
		AffectedServices: req.AffectedServices,
		CreatedBy:        createdBy,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.incidentRepo.Create(ctx, incident); err != nil {
		return nil, fmt.Errorf("storing incident: %w", err)
	}

	s.incidentRepo.AddTimelineEvent(ctx, &domain.IncidentEvent{
		EventID:     uuid.New(),
		IncidentID:  incident.IncidentID,
		EventType:   "INCIDENT_CREATED",
		Description: fmt.Sprintf("Incident created: %s", req.Title),
		Actor:       createdBy.String(),
		Timestamp:   now,
	})

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "incident.detected", incident)
	}

	return incident, nil
}

func (s *IncidentService) GetIncident(ctx context.Context, id uuid.UUID) (*domain.Incident, error) {
	return s.incidentRepo.GetByID(ctx, id)
}

func (s *IncidentService) GetOpenIncidents(ctx context.Context) ([]*domain.Incident, error) {
	return s.incidentRepo.GetByStatus(ctx, domain.IncidentStatusOpen)
}

func (s *IncidentService) UpdateIncident(ctx context.Context, id uuid.UUID, req *domain.UpdateIncidentRequest) (*domain.Incident, error) {
	incident, err := s.incidentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Severity != nil {
		incident.Severity = *req.Severity
		s.incidentRepo.AddTimelineEvent(ctx, &domain.IncidentEvent{
			EventID:     uuid.New(),
			IncidentID:  id,
			EventType:   "SEVERITY_CHANGED",
			Description: fmt.Sprintf("Severity changed to %s", string(*req.Severity)),
			Actor:       "system",
			Timestamp:   time.Now().UTC(),
		})
	}
	if req.Status != nil {
		oldStatus := incident.Status
		incident.Status = *req.Status
		if *req.Status == domain.IncidentStatusResolved {
			now := time.Now().UTC()
			incident.ResolvedAt = &now
		}
		s.incidentRepo.AddTimelineEvent(ctx, &domain.IncidentEvent{
			EventID:     uuid.New(),
			IncidentID:  id,
			EventType:   "STATUS_CHANGED",
			Description: fmt.Sprintf("Status changed from %s to %s", string(oldStatus), string(*req.Status)),
			Actor:       "system",
			Timestamp:   time.Now().UTC(),
		})
	}
	if req.Resolution != nil {
		incident.Resolution = *req.Resolution
		s.incidentRepo.AddTimelineEvent(ctx, &domain.IncidentEvent{
			EventID:     uuid.New(),
			IncidentID:  id,
			EventType:   "RESOLUTION_UPDATED",
			Description: *req.Resolution,
			Actor:       "system",
			Timestamp:   time.Now().UTC(),
		})
	}

	incident.UpdatedAt = time.Now().UTC()
	if err := s.incidentRepo.Update(ctx, incident); err != nil {
		return nil, fmt.Errorf("updating incident: %w", err)
	}
	return incident, nil
}

func (s *IncidentService) UpdateIncidentStatus(ctx context.Context, incidentID uuid.UUID, newStatus domain.IncidentStatus, resolution string) error {
	s.logger.Info().Str("incident_id", incidentID.String()).Str("status", string(newStatus)).Msg("updating incident status")

	incident, err := s.incidentRepo.GetByID(ctx, incidentID)
	if err != nil {
		return err
	}

	oldStatus := incident.Status
	incident.Status = newStatus
	incident.UpdatedAt = time.Now().UTC()

	if newStatus == domain.IncidentStatusResolved {
		now := time.Now().UTC()
		incident.ResolvedAt = &now
		if resolution != "" {
			incident.Resolution = resolution
		}
	}

	if err := s.incidentRepo.Update(ctx, incident); err != nil {
		return fmt.Errorf("updating incident: %w", err)
	}

	eventType := "STATUS_CHANGED"
	if newStatus == domain.IncidentStatusResolved {
		eventType = "INCIDENT_RESOLVED"
	} else if newStatus == domain.IncidentStatusMitigated {
		eventType = "INCIDENT_MITIGATED"
	}

	s.incidentRepo.AddTimelineEvent(ctx, &domain.IncidentEvent{
		EventID:     uuid.New(),
		IncidentID:  incidentID,
		EventType:   eventType,
		Description: fmt.Sprintf("Status changed from %s to %s. %s", string(oldStatus), string(newStatus), resolution),
		Actor:       "system",
		Timestamp:   time.Now().UTC(),
	})

	return nil
}

func (s *IncidentService) GetTimeline(ctx context.Context, incidentID uuid.UUID) (*domain.IncidentTimeline, error) {
	s.logger.Info().Str("incident_id", incidentID.String()).Msg("getting incident timeline")

	incident, err := s.incidentRepo.GetByID(ctx, incidentID)
	if err != nil {
		return nil, err
	}

	events, err := s.incidentRepo.GetTimeline(ctx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("fetching timeline: %w", err)
	}

	timeline := make([]domain.IncidentEvent, len(events))
	for i, e := range events {
		timeline[i] = *e
	}

	return &domain.IncidentTimeline{
		IncidentID: incident.IncidentID,
		Title:      incident.Title,
		Status:     incident.Status,
		Timeline:   timeline,
	}, nil
}

func (s *IncidentService) ResolveIncident(ctx context.Context, id uuid.UUID, resolution string) error {
	return s.UpdateIncidentStatus(ctx, id, domain.IncidentStatusResolved, resolution)
}

func (s *IncidentService) DetectIncidents(ctx context.Context, thresholds []domain.IncidentThreshold) (*domain.DetectIncidentsResponse, error) {
	s.logger.Info().Msg("detecting incidents from thresholds")

	var created []*domain.Incident

	for _, threshold := range thresholds {
		if !threshold.ShouldAlert {
			continue
		}

		existing, _ := s.incidentRepo.GetRecentByService(ctx, threshold.ServiceName, time.Now().Add(-1*time.Hour))
		if len(existing) > 0 {
			s.logger.Info().Str("service", threshold.ServiceName).Msg("incident already exists for service, skipping")
			continue
		}

		severity := domain.IncidentSeverityLow
		if threshold.ErrorRate > 0.5 {
			severity = domain.IncidentSeverityCritical
		} else if threshold.ErrorRate > 0.1 {
			severity = domain.IncidentSeverityHigh
		} else if threshold.ErrorRate > 0.01 {
			severity = domain.IncidentSeverityMedium
		}

		title := fmt.Sprintf("Auto-detected incident for %s", threshold.ServiceName)
		description := fmt.Sprintf("Error rate: %.2f%%, P99 latency: %.0fms, Open incidents: %d",
			threshold.ErrorRate*100, threshold.LatencyP99, threshold.OpenIncidents)

		incident := &domain.Incident{
			IncidentID:       uuid.New(),
			Title:            title,
			Description:      description,
			Severity:         severity,
			Status:           domain.IncidentStatusOpen,
			AffectedServices: []string{threshold.ServiceName},
			CreatedBy:        uuid.Nil,
			CreatedAt:        time.Now().UTC(),
			UpdatedAt:        time.Now().UTC(),
		}

		if err := s.incidentRepo.Create(ctx, incident); err != nil {
			s.logger.Error().Err(err).Str("service", threshold.ServiceName).Msg("failed to create auto-detected incident")
			continue
		}

		s.incidentRepo.AddTimelineEvent(ctx, &domain.IncidentEvent{
			EventID:     uuid.New(),
			IncidentID:  incident.IncidentID,
			EventType:   "INCIDENT_AUTO_DETECTED",
			Description: description,
			Actor:       "auto-detector",
			Timestamp:   time.Now().UTC(),
		})

		created = append(created, incident)
		s.logger.Info().Str("service", threshold.ServiceName).Str("severity", string(severity)).Msg("auto-detected incident created")
	}

	return &domain.DetectIncidentsResponse{
		IncidentsCreated: created,
		CheckedAt:        time.Now().UTC(),
	}, nil
}
