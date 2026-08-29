package recovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type Severity string

const (
	SeverityHealthy   Severity = "HEALTHY"
	SeverityDegraded  Severity = "DEGRADED"
	SeverityCritical  Severity = "CRITICAL"
)

type RecoveryAction string

const (
	ActionPauseRetries    RecoveryAction = "PAUSE_RETRIES"
	ActionReduceLoad      RecoveryAction = "REDUCE_LOAD"
	ActionQueueNonCritical RecoveryAction = "QUEUE_NON_CRITICAL"
	ActionResumeOperations RecoveryAction = "RESUME_OPERATIONS"
	ActionCircuitBreak    RecoveryAction = "CIRCUIT_BREAK"
	ActionRateLimit       RecoveryAction = "RATE_LIMIT"
)

type FailureDetection struct {
	Service     string    `json:"service"`
	ErrorRate   float64   `json:"error_rate"`
	LatencyP99  float64   `json:"latency_p99"`
	Severity    Severity  `json:"severity"`
	DetectedAt  time.Time `json:"detected_at"`
}

type RecoveryEvent struct {
	ID        string         `json:"id"`
	Service   string         `json:"service"`
	Action    RecoveryAction `json:"action"`
	Severity  Severity       `json:"severity"`
	Message   string         `json:"message"`
	Timestamp time.Time      `json:"timestamp"`
}

type HealthChecker interface {
	IsHealthy(ctx context.Context, service string) (bool, error)
}

type LoadShedder interface {
	SetRateLimit(service string, rps float64)
	QueueNonCritical(service string, enable bool)
}

type RecoveryController struct {
	healthChecker  HealthChecker
	loadShedder    LoadShedder
	logger         zerolog.Logger
	auditLog       []RecoveryEvent
	serviceStates  map[string]*ServiceState
	mu             sync.RWMutex
 thresholds     *Thresholds
}

type Thresholds struct {
	ErrorRateDegraded  float64
	ErrorRateCritical  float64
	LatencyDegradedMs  float64
	LatencyCriticalMs float64
}

func DefaultThresholds() *Thresholds {
	return &Thresholds{
		ErrorRateDegraded:  0.05,
		ErrorRateCritical:  0.20,
		LatencyDegradedMs:  500,
		LatencyCriticalMs: 2000,
	}
}

func NewRecoveryController(healthChecker HealthChecker, loadShedder LoadShedder, logger zerolog.Logger) *RecoveryController {
	return &RecoveryController{
		healthChecker: healthChecker,
		loadShedder:   loadShedder,
		logger:        logger,
		auditLog:      make([]RecoveryEvent, 0),
		serviceStates: make(map[string]*ServiceState),
		thresholds:    DefaultThresholds(),
	}
}

func (rc *RecoveryController) NewRecoveryControllerWithThresholds(healthChecker HealthChecker, loadShedder LoadShedder, logger zerolog.Logger, thresholds *Thresholds) *RecoveryController {
	return &RecoveryController{
		healthChecker: healthChecker,
		loadShedder:   loadShedder,
		logger:        logger,
		auditLog:      make([]RecoveryEvent, 0),
		serviceStates: make(map[string]*ServiceState),
		thresholds:    thresholds,
	}
}

func (rc *RecoveryController) DetectFailure(ctx context.Context, service string, errorRate float64, latencyMs float64) *FailureDetection {
	severity := rc.ClassifySeverity(errorRate, latencyMs)

	detection := &FailureDetection{
		Service:    service,
		ErrorRate:  errorRate,
		LatencyP99: latencyMs,
		Severity:   severity,
		DetectedAt: time.Now().UTC(),
	}

	rc.mu.Lock()
	state, ok := rc.serviceStates[service]
	if !ok {
		state = &ServiceState{
			Service:  service,
			Severity: SeverityHealthy,
		}
		rc.serviceStates[service] = state
	}
	state.Severity = severity
	state.LastErrorRate = errorRate
	state.LastLatency = latencyMs
	state.LastCheckedAt = time.Now().UTC()
	rc.mu.Unlock()

	if severity != SeverityHealthy {
		rc.logger.Warn().
			Str("service", service).
			Str("severity", string(severity)).
			Float64("error_rate", errorRate).
			Float64("latency_ms", latencyMs).
			Msg("failure detected")
	}

	return detection
}

func (rc *RecoveryController) ClassifySeverity(errorRate float64, latencyMs float64) Severity {
	if errorRate >= rc.thresholds.ErrorRateCritical || latencyMs >= rc.thresholds.LatencyCriticalMs {
		return SeverityCritical
	}
	if errorRate >= rc.thresholds.ErrorRateDegraded || latencyMs >= rc.thresholds.LatencyDegradedMs {
		return SeverityDegraded
	}
	return SeverityHealthy
}

func (rc *RecoveryController) Mitigate(ctx context.Context, severity Severity, service string) error {
	rc.mu.Lock()
	state, ok := rc.serviceStates[service]
	if !ok {
		state = &ServiceState{
			Service:  service,
			Severity: severity,
		}
		rc.serviceStates[service] = state
	}
	rc.mu.Unlock()

	switch severity {
	case SeverityDegraded:
		return rc.mitigateDegraded(ctx, service, state)
	case SeverityCritical:
		return rc.mitigateCritical(ctx, service, state)
	case SeverityHealthy:
		return rc.Recover(ctx, service)
	}

	return nil
}

func (rc *RecoveryController) mitigateDegraded(ctx context.Context, service string, state *ServiceState) error {
	if !state.PausedRetries {
		rc.recordAudit(service, ActionPauseRetries, SeverityDegraded, "pausing retries for degraded service")
		state.PausedRetries = true
	}

	if rc.loadShedder != nil {
		rc.loadShedder.SetRateLimit(service, 100)
	}

	rc.recordAudit(service, ActionReduceLoad, SeverityDegraded, "reducing load for degraded service")
	state.LoadReduced = true

	rc.logger.Info().
		Str("service", service).
		Str("severity", string(SeverityDegraded)).
		Msg("applied degraded mitigation")

	return nil
}

func (rc *RecoveryController) mitigateCritical(ctx context.Context, service string, state *ServiceState) error {
	if !state.PausedRetries {
		rc.recordAudit(service, ActionPauseRetries, SeverityCritical, "pausing retries for critical service")
		state.PausedRetries = true
	}

	if !state.CircuitBroken {
		rc.recordAudit(service, ActionCircuitBreak, SeverityCritical, "circuit breaker activated for critical service")
		state.CircuitBroken = true
	}

	if rc.loadShedder != nil {
		rc.loadShedder.SetRateLimit(service, 10)
		rc.loadShedder.QueueNonCritical(service, true)
	}

	rc.recordAudit(service, ActionQueueNonCritical, SeverityCritical, "queuing non-critical requests for critical service")
	rc.recordAudit(service, ActionRateLimit, SeverityCritical, "rate limiting critical service to 10 rps")

	state.LoadReduced = true

	rc.logger.Warn().
		Str("service", service).
		Str("severity", string(SeverityCritical)).
		Msg("applied critical mitigation")

	return nil
}

func (rc *RecoveryController) Recover(ctx context.Context, service string) error {
	rc.mu.Lock()
	state, ok := rc.serviceStates[service]
	if !ok {
		rc.mu.Unlock()
		return nil
	}

	if state.PausedRetries {
		state.PausedRetries = false
		rc.recordAudit(service, ActionResumeOperations, SeverityHealthy, "resuming retries")
	}

	if state.CircuitBroken {
		state.CircuitBroken = false
		rc.recordAudit(service, ActionResumeOperations, SeverityHealthy, "opening circuit breaker")
	}

	if state.LoadReduced {
		if rc.loadShedder != nil {
			rc.loadShedder.SetRateLimit(service, 1000)
			rc.loadShedder.QueueNonCritical(service, false)
		}
		state.LoadReduced = false
		rc.recordAudit(service, ActionResumeOperations, SeverityHealthy, "restoring full load")
	}

	state.Severity = SeverityHealthy
	state.RecoveryAttempts++
	rc.mu.Unlock()

	rc.logger.Info().
		Str("service", service).
		Int("recovery_attempts", state.RecoveryAttempts).
		Msg("service recovered")

	return nil
}

func (rc *RecoveryController) Verify(ctx context.Context, service string) (bool, error) {
	healthy, err := rc.healthChecker.IsHealthy(ctx, service)
	if err != nil {
		return false, fmt.Errorf("health check for %s failed: %w", service, err)
	}

	rc.mu.RLock()
	state, ok := rc.serviceStates[service]
	rc.mu.RUnlock()

	if ok && healthy && state.Severity != SeverityHealthy {
		_ = rc.Recover(ctx, service)
	}

	return healthy, nil
}

func (rc *RecoveryController) GetServiceState(service string) (*ServiceState, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	state, ok := rc.serviceStates[service]
	return state, ok
}

func (rc *RecoveryController) GetAllServiceStates() map[string]*ServiceState {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	result := make(map[string]*ServiceState)
	for k, v := range rc.serviceStates {
		result[k] = v
	}
	return result
}

func (rc *RecoveryController) GetAuditLog() []RecoveryEvent {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	entries := make([]RecoveryEvent, len(rc.auditLog))
	copy(entries, rc.auditLog)
	return entries
}

func (rc *RecoveryController) recordAudit(service string, action RecoveryAction, severity Severity, message string) {
	event := RecoveryEvent{
		ID:        fmt.Sprintf("recovery-%s-%d", service, time.Now().UnixNano()),
		Service:   service,
		Action:    action,
		Severity:  severity,
		Message:   message,
		Timestamp: time.Now().UTC(),
	}

	rc.mu.Lock()
	rc.auditLog = append(rc.auditLog, event)
	rc.mu.Unlock()

	rc.logger.Info().
		Str("service", service).
		Str("action", string(action)).
		Str("severity", string(severity)).
		Str("message", message).
		Msg("recovery action recorded")
}

type ServiceState struct {
	Service          string        `json:"service"`
	Severity         Severity      `json:"severity"`
	LastErrorRate    float64       `json:"last_error_rate"`
	LastLatency      float64       `json:"last_latency"`
	LastCheckedAt    time.Time     `json:"last_checked_at"`
	RecoveryAttempts int           `json:"recovery_attempts"`
	PausedRetries    bool          `json:"paused_retries"`
	LoadReduced      bool          `json:"load_reduced"`
	CircuitBroken    bool          `json:"circuit_broken"`
	CreatedAt        time.Time     `json:"created_at"`
}

type SimpleHealthChecker struct{}

func NewSimpleHealthChecker() *SimpleHealthChecker {
	return &SimpleHealthChecker{}
}

func (h *SimpleHealthChecker) IsHealthy(ctx context.Context, service string) (bool, error) {
	return true, nil
}

type SimpleLoadShedder struct {
	mu            sync.RWMutex
	rateLimits    map[string]float64
	queueNonCrit  map[string]bool
}

func NewSimpleLoadShedder() *SimpleLoadShedder {
	return &SimpleLoadShedder{
		rateLimits:   make(map[string]float64),
		queueNonCrit: make(map[string]bool),
	}
}

func (s *SimpleLoadShedder) SetRateLimit(service string, rps float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimits[service] = rps
}

func (s *SimpleLoadShedder) QueueNonCritical(service string, enable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueNonCrit[service] = enable
}

func (s *SimpleLoadShedder) GetRateLimit(service string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rateLimits[service]
}

func (s *SimpleLoadShedder) IsQueueNonCritical(service string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queueNonCrit[service]
}
