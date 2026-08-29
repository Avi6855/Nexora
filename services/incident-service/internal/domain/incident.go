package domain

import (
	"time"

	"github.com/google/uuid"
)

type IncidentSeverity string

const (
	IncidentSeverityLow      IncidentSeverity = "LOW"
	IncidentSeverityMedium   IncidentSeverity = "MEDIUM"
	IncidentSeverityHigh     IncidentSeverity = "HIGH"
	IncidentSeverityCritical IncidentSeverity = "CRITICAL"
)

type IncidentStatus string

const (
	IncidentStatusOpen          IncidentStatus = "OPEN"
	IncidentStatusInvestigating IncidentStatus = "INVESTIGATING"
	IncidentStatusMitigated     IncidentStatus = "MITIGATED"
	IncidentStatusResolved      IncidentStatus = "RESOLVED"
)

type IncidentEvent struct {
	EventID     uuid.UUID `json:"event_id"`
	IncidentID  uuid.UUID `json:"incident_id"`
	EventType   string    `json:"event_type"`
	Description string    `json:"description"`
	Actor       string    `json:"actor"`
	Timestamp   time.Time `json:"timestamp"`
}

type Incident struct {
	IncidentID       uuid.UUID          `json:"incident_id"`
	Title            string             `json:"title"`
	Description      string             `json:"description"`
	Severity         IncidentSeverity   `json:"severity"`
	Status           IncidentStatus     `json:"status"`
	AffectedServices []string           `json:"affected_services"`
	CreatedBy        uuid.UUID          `json:"created_by"`
	AssignedTo       *uuid.UUID         `json:"assigned_to,omitempty"`
	Resolution       string             `json:"resolution,omitempty"`
	Events           []IncidentEvent    `json:"events,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	ResolvedAt       *time.Time         `json:"resolved_at,omitempty"`
}

type IncidentTimeline struct {
	IncidentID uuid.UUID       `json:"incident_id"`
	Title      string          `json:"title"`
	Status     IncidentStatus  `json:"status"`
	Timeline   []IncidentEvent `json:"timeline"`
}

type IncidentThreshold struct {
	ServiceName    string `json:"service_name"`
	ErrorRate      float64 `json:"error_rate"`
	LatencyP99     float64 `json:"latency_p99"`
	OpenIncidents  int    `json:"open_incidents"`
	ShouldAlert    bool   `json:"should_alert"`
}

type CreateIncidentRequest struct {
	Title            string           `json:"title"`
	Description      string           `json:"description"`
	Severity         IncidentSeverity `json:"severity"`
	AffectedServices []string         `json:"affected_services"`
}

type UpdateIncidentRequest struct {
	Severity   *IncidentSeverity `json:"severity,omitempty"`
	Status     *IncidentStatus   `json:"status,omitempty"`
	Resolution *string           `json:"resolution,omitempty"`
}

type UpdateIncidentStatusRequest struct {
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
}

type DetectIncidentsRequest struct {
	Thresholds []IncidentThreshold `json:"thresholds"`
}

type DetectIncidentsResponse struct {
	IncidentsCreated []*Incident `json:"incidents_created"`
	CheckedAt        time.Time   `json:"checked_at"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
