package domain

import (
	"time"

	"github.com/google/uuid"
)

type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "HEALTHY"
	HealthStatusDegraded  HealthStatus = "DEGRADED"
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
)

type SystemHealth struct {
	SystemID   uuid.UUID              `json:"system_id"`
	Status     HealthStatus           `json:"status"`
	Services   map[string]ServiceHealth `json:"services"`
	Uptime     string                 `json:"uptime"`
	CheckedAt  time.Time              `json:"checked_at"`
}

type ServiceHealth struct {
	ServiceName  string       `json:"service_name"`
	Status       HealthStatus `json:"status"`
	ResponseTime string       `json:"response_time"`
	LastError    string       `json:"last_error,omitempty"`
	CheckedAt    time.Time    `json:"checked_at"`
}
