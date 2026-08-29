package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AuditAction string

const (
	AuditActionPaymentCreated      AuditAction = "PAYMENT_CREATED"
	AuditActionPaymentAuthorized   AuditAction = "PAYMENT_AUTHORIZED"
	AuditActionPaymentProcessing   AuditAction = "PAYMENT_PROCESSING"
	AuditActionPaymentConfirmed    AuditAction = "PAYMENT_CONFIRMED"
	AuditActionPaymentSettled      AuditAction = "PAYMENT_SETTLED"
	AuditActionPaymentFailed       AuditAction = "PAYMENT_FAILED"
	AuditActionPaymentReversed     AuditAction = "PAYMENT_REVERSED"
	AuditActionPaymentCancelled    AuditAction = "PAYMENT_CANCELLED"
	AuditActionAccountCreated      AuditAction = "ACCOUNT_CREATED"
	AuditActionAccountFunded       AuditAction = "ACCOUNT_FUNDED"
	AuditActionAccountFrozen       AuditAction = "ACCOUNT_FROZEN"
	AuditActionAccountUnfrozen     AuditAction = "ACCOUNT_UNFROZEN"
	AuditActionAccountClosed       AuditAction = "ACCOUNT_CLOSED"
	AuditActionUserLogin           AuditAction = "USER_LOGIN"
	AuditActionUserLogout          AuditAction = "USER_LOGOUT"
	AuditActionUserPasswordChange  AuditAction = "USER_PASSWORD_CHANGE"
	AuditActionFraudDetected       AuditAction = "FRAUD_DETECTED"
	AuditActionFraudBlocked        AuditAction = "FRAUD_BLOCKED"
	AuditActionPolicyActivated     AuditAction = "POLICY_ACTIVATED"
	AuditActionPolicyDisabled      AuditAction = "POLICY_DISABLED"
	AuditActionIncidentCreated     AuditAction = "INCIDENT_CREATED"
	AuditActionIncidentResolved    AuditAction = "INCIDENT_RESOLVED"
	AuditActionSystemConfigChange  AuditAction = "SYSTEM_CONFIG_CHANGE"
)

type AuditEvent struct {
	EventID      uuid.UUID         `json:"event_id"`
	Actor        string            `json:"actor"`
	Action       AuditAction       `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Details      json.RawMessage   `json:"details"`
	RequestID    string            `json:"request_id"`
	TraceID      string            `json:"trace_id"`
	IPAddress    string            `json:"ip_address"`
	UserAgent    string            `json:"user_agent"`
	CreatedAt    time.Time         `json:"created_at"`
}

func NewAuditEvent(actor string, action AuditAction, resourceType, resourceID string, details json.RawMessage, requestID, traceID, ipAddress, userAgent string) *AuditEvent {
	return &AuditEvent{
		EventID:      uuid.New(),
		Actor:        actor,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      details,
		RequestID:    requestID,
		TraceID:      traceID,
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
		CreatedAt:    time.Now().UTC(),
	}
}

type AuditLog struct {
	LogID        uuid.UUID         `json:"log_id"`
	UserID       uuid.UUID         `json:"user_id"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Details      map[string]string `json:"details"`
	IPAddress    string            `json:"ip_address"`
	UserAgent    string            `json:"user_agent"`
	CreatedAt    time.Time         `json:"created_at"`
}

func NewAuditLog(userID uuid.UUID, action, resourceType, resourceID string, details map[string]string, ipAddress, userAgent string) *AuditLog {
	return &AuditLog{
		LogID:        uuid.New(),
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      details,
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
		CreatedAt:    time.Now().UTC(),
	}
}

type RecordEventRequest struct {
	Actor        string          `json:"actor"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Details      json.RawMessage `json:"details"`
	RequestID    string          `json:"request_id"`
	TraceID      string          `json:"trace_id"`
	IPAddress    string          `json:"ip_address"`
	UserAgent    string          `json:"user_agent"`
}

type CreateAuditLogRequest struct {
	UserID       string            `json:"user_id"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Details      map[string]string `json:"details"`
	IPAddress    string            `json:"ip_address"`
	UserAgent    string            `json:"user_agent"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
