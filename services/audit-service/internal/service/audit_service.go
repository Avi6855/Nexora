package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/audit-service/internal/domain"
	"github.com/nexora/nexora/services/audit-service/internal/events"
	"github.com/nexora/nexora/services/audit-service/internal/repository"
)

type AuditService struct {
	auditRepo repository.AuditRepository
	producer  *events.KafkaProducer
	logger    zerolog.Logger
}

func NewAuditService(auditRepo repository.AuditRepository, producer *events.KafkaProducer, logger zerolog.Logger) *AuditService {
	return &AuditService{auditRepo: auditRepo, producer: producer, logger: logger}
}

func (s *AuditService) RecordEvent(ctx context.Context, actor string, action domain.AuditAction, resourceType, resourceID string, details []byte, requestID, traceID, ipAddress, userAgent string) error {
	s.logger.Info().Str("actor", actor).Str("action", string(action)).Str("resource", resourceType+"/"+resourceID).Msg("recording audit event")

	event := domain.NewAuditEvent(actor, action, resourceType, resourceID, details, requestID, traceID, ipAddress, userAgent)
	if err := s.auditRepo.RecordEvent(ctx, event); err != nil {
		return fmt.Errorf("storing audit event: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "audit.event.recorded", event)
	}

	s.logger.Info().Str("event_id", event.EventID.String()).Msg("audit event recorded")
	return nil
}

func (s *AuditService) RecordEventFromRequest(ctx context.Context, req *domain.RecordEventRequest) error {
	action := domain.AuditAction(req.Action)
	return s.RecordEvent(ctx, req.Actor, action, req.ResourceType, req.ResourceID, req.Details, req.RequestID, req.TraceID, req.IPAddress, req.UserAgent)
}

func (s *AuditService) QueryAuditTrail(ctx context.Context, resourceType, resourceID string, limit int) ([]*domain.AuditEvent, error) {
	s.logger.Info().Str("resource_type", resourceType).Str("resource_id", resourceID).Msg("querying audit trail")

	if limit <= 0 {
		limit = 100
	}

	events, err := s.auditRepo.GetByResource(ctx, resourceType, resourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying audit trail: %w", err)
	}

	return events, nil
}

func (s *AuditService) QueryUserAuditTrail(ctx context.Context, userID string, limit int) ([]*domain.AuditEvent, error) {
	s.logger.Info().Str("user_id", userID).Msg("querying user audit trail")

	if limit <= 0 {
		limit = 100
	}

	events, err := s.auditRepo.GetByUserIDLogs(ctx, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying user audit trail: %w", err)
	}

	return events, nil
}

func (s *AuditService) GetAuditEvent(ctx context.Context, id uuid.UUID) (*domain.AuditEvent, error) {
	return s.auditRepo.GetByID(ctx, id)
}

func (s *AuditService) CreateAuditLog(ctx context.Context, req *domain.CreateAuditLogRequest) (*domain.AuditLog, error) {
	s.logger.Info().Str("action", req.Action).Msg("creating audit log")
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}
	log := domain.NewAuditLog(userID, req.Action, req.ResourceType, req.ResourceID, req.Details, req.IPAddress, req.UserAgent)
	if err := s.auditRepo.Create(ctx, log); err != nil {
		return nil, fmt.Errorf("storing audit log: %w", err)
	}
	return log, nil
}

func (s *AuditService) GetAuditLog(ctx context.Context, id uuid.UUID) (*domain.AuditLog, error) {
	return s.auditRepo.GetLogByID(ctx, id)
}

func (s *AuditService) GetAuditLogsByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.auditRepo.GetLogsByUserID(ctx, userID, limit)
}

func (s *AuditService) GetAuditLogsByResource(ctx context.Context, resourceType, resourceID string) ([]*domain.AuditLog, error) {
	return s.auditRepo.GetLogsByResource(ctx, resourceType, resourceID)
}

func (s *AuditService) RecordPaymentCreated(ctx context.Context, paymentID, accountID, userID string) error {
	details := []byte(fmt.Sprintf(`{"payment_id":"%s","account_id":"%s","user_id":"%s"}`, paymentID, accountID, userID))
	return s.RecordEvent(ctx, userID, domain.AuditActionPaymentCreated, "payment", paymentID, details, "", "", "", "")
}

func (s *AuditService) RecordPaymentStateChange(ctx context.Context, paymentID, oldState, newState, userID string) error {
	details := []byte(fmt.Sprintf(`{"payment_id":"%s","old_state":"%s","new_state":"%s"}`, paymentID, oldState, newState))
	action := domain.AuditAction(fmt.Sprintf("PAYMENT_%s", newState))
	return s.RecordEvent(ctx, userID, action, "payment", paymentID, details, "", "", "", "")
}

func (s *AuditService) RecordFraudDetected(ctx context.Context, paymentID, userID, reason string) error {
	details := []byte(fmt.Sprintf(`{"payment_id":"%s","user_id":"%s","reason":"%s","detected_at":"%s"}`, paymentID, userID, reason, time.Now().UTC().Format(time.RFC3339)))
	return s.RecordEvent(ctx, userID, domain.AuditActionFraudDetected, "payment", paymentID, details, "", "", "", "")
}

func (s *AuditService) RecordIncidentCreated(ctx context.Context, incidentID, title, severity, createdBy string) error {
	details := []byte(fmt.Sprintf(`{"incident_id":"%s","title":"%s","severity":"%s","created_by":"%s"}`, incidentID, title, severity, createdBy))
	return s.RecordEvent(ctx, createdBy, domain.AuditActionIncidentCreated, "incident", incidentID, details, "", "", "", "")
}
