package tests

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/nexora/nexora/shared/recovery"
)

func TestRecoveryController_DetectFailure_Degraded(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	detection := controller.DetectFailure(context.Background(), "payment-service", 0.10, 600)

	if detection.Severity != recovery.SeverityDegraded {
		t.Errorf("expected severity DEGRADED, got %s", detection.Severity)
	}

	if detection.Service != "payment-service" {
		t.Errorf("expected service payment-service, got %s", detection.Service)
	}

	if detection.ErrorRate != 0.10 {
		t.Errorf("expected error rate 0.10, got %f", detection.ErrorRate)
	}
}

func TestRecoveryController_DetectFailure_Critical(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	detection := controller.DetectFailure(context.Background(), "ledger-service", 0.25, 2500)

	if detection.Severity != recovery.SeverityCritical {
		t.Errorf("expected severity CRITICAL, got %s", detection.Severity)
	}
}

func TestRecoveryController_DetectFailure_Healthy(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	detection := controller.DetectFailure(context.Background(), "user-service", 0.01, 50)

	if detection.Severity != recovery.SeverityHealthy {
		t.Errorf("expected severity HEALTHY, got %s", detection.Severity)
	}
}

func TestRecoveryController_ClassifySeverity(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	tests := []struct {
		name      string
		errorRate float64
		latency   float64
		expected  recovery.Severity
	}{
		{"healthy low error rate", 0.01, 100, recovery.SeverityHealthy},
		{"healthy zero metrics", 0, 0, recovery.SeverityHealthy},
		{"degraded by error rate", 0.08, 100, recovery.SeverityDegraded},
		{"degraded by latency", 0.01, 600, recovery.SeverityDegraded},
		{"critical by error rate", 0.25, 100, recovery.SeverityCritical},
		{"critical by latency", 0.01, 2500, recovery.SeverityCritical},
		{"critical both high", 0.30, 3000, recovery.SeverityCritical},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := controller.ClassifySeverity(tt.errorRate, tt.latency)
			if result != tt.expected {
				t.Errorf("expected severity %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestRecoveryController_MitigateDegraded(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	err := controller.Mitigate(context.Background(), recovery.SeverityDegraded, "payment-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, ok := controller.GetServiceState("payment-service")
	if !ok {
		t.Fatal("expected service state to exist")
	}

	if !state.PausedRetries {
		t.Error("expected retries to be paused")
	}

	if !state.LoadReduced {
		t.Error("expected load to be reduced")
	}
}

func TestRecoveryController_MitigateCritical(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	err := controller.Mitigate(context.Background(), recovery.SeverityCritical, "ledger-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, ok := controller.GetServiceState("ledger-service")
	if !ok {
		t.Fatal("expected service state to exist")
	}

	if !state.PausedRetries {
		t.Error("expected retries to be paused")
	}

	if !state.CircuitBroken {
		t.Error("expected circuit breaker to be activated")
	}

	if !state.LoadReduced {
		t.Error("expected load to be reduced")
	}

	rateLimit := loadShedder.GetRateLimit("ledger-service")
	if rateLimit != 10 {
		t.Errorf("expected rate limit 10, got %f", rateLimit)
	}

	if !loadShedder.IsQueueNonCritical("ledger-service") {
		t.Error("expected non-critical to be queued")
	}
}

func TestRecoveryController_Recover(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	_ = controller.Mitigate(context.Background(), recovery.SeverityCritical, "payment-service")

	err := controller.Recover(context.Background(), "payment-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, ok := controller.GetServiceState("payment-service")
	if !ok {
		t.Fatal("expected service state to exist")
	}

	if state.PausedRetries {
		t.Error("expected retries to be resumed")
	}

	if state.CircuitBroken {
		t.Error("expected circuit breaker to be opened")
	}

	if state.LoadReduced {
		t.Error("expected load to be restored")
	}

	if state.Severity != recovery.SeverityHealthy {
		t.Errorf("expected severity HEALTHY, got %s", state.Severity)
	}
}

func TestRecoveryController_Verify(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	healthy, err := controller.Verify(context.Background(), "payment-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !healthy {
		t.Error("expected service to be healthy")
	}
}

func TestRecoveryController_AuditLog(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	_ = controller.Mitigate(context.Background(), recovery.SeverityCritical, "payment-service")
	_ = controller.Recover(context.Background(), "payment-service")

	auditLog := controller.GetAuditLog()
	if len(auditLog) == 0 {
		t.Error("expected audit log entries")
	}

	actions := make(map[string]bool)
	for _, event := range auditLog {
		actions[string(event.Action)] = true
	}

	if !actions[string(recovery.ActionPauseRetries)] {
		t.Error("expected PAUSE_RETRIES in audit log")
	}

	if !actions[string(recovery.ActionCircuitBreak)] {
		t.Error("expected CIRCUIT_BREAK in audit log")
	}

	if !actions[string(recovery.ActionResumeOperations)] {
		t.Error("expected RESUME_OPERATIONS in audit log")
	}
}

func TestRecoveryController_AllServiceStates(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	_ = controller.Mitigate(context.Background(), recovery.SeverityDegraded, "payment-service")
	_ = controller.Mitigate(context.Background(), recovery.SeverityCritical, "ledger-service")

	states := controller.GetAllServiceStates()
	if len(states) != 2 {
		t.Errorf("expected 2 service states, got %d", len(states))
	}

	if _, ok := states["payment-service"]; !ok {
		t.Error("expected payment-service state")
	}

	if _, ok := states["ledger-service"]; !ok {
		t.Error("expected ledger-service state")
	}
}

func TestSimpleLoadShedder(t *testing.T) {
	shedder := recovery.NewSimpleLoadShedder()

	shedder.SetRateLimit("payment-service", 500)
	shedder.QueueNonCritical("payment-service", true)

	if shedder.GetRateLimit("payment-service") != 500 {
		t.Errorf("expected rate limit 500, got %f", shedder.GetRateLimit("payment-service"))
	}

	if !shedder.IsQueueNonCritical("payment-service") {
		t.Error("expected non-critical to be queued")
	}

	shedder.SetRateLimit("payment-service", 1000)
	shedder.QueueNonCritical("payment-service", false)

	if shedder.GetRateLimit("payment-service") != 1000 {
		t.Errorf("expected rate limit 1000, got %f", shedder.GetRateLimit("payment-service"))
	}

	if shedder.IsQueueNonCritical("payment-service") {
		t.Error("expected non-critical to not be queued")
	}
}

func TestRecoveryController_MultipleServices(t *testing.T) {
	healthChecker := recovery.NewSimpleHealthChecker()
	loadShedder := recovery.NewSimpleLoadShedder()
	logger := zerolog.Nop()

	controller := recovery.NewRecoveryController(healthChecker, loadShedder, logger)

	services := []string{"payment-service", "ledger-service", "fraud-service"}
	for _, svc := range services {
		_ = controller.Mitigate(context.Background(), recovery.SeverityCritical, svc)
	}

	for _, svc := range services {
		state, ok := controller.GetServiceState(svc)
		if !ok {
			t.Errorf("expected state for %s", svc)
			continue
		}
		if state.Severity != recovery.SeverityCritical {
			t.Errorf("expected CRITICAL severity for %s, got %s", svc, state.Severity)
		}
	}

	for _, svc := range services {
		_ = controller.Recover(context.Background(), svc)
	}

	for _, svc := range services {
		state, ok := controller.GetServiceState(svc)
		if !ok {
			t.Errorf("expected state for %s", svc)
			continue
		}
		if state.Severity != recovery.SeverityHealthy {
			t.Errorf("expected HEALTHY severity for %s, got %s", svc, state.Severity)
		}
	}
}
