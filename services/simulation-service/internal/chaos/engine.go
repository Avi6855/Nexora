package chaos

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

type FaultType string

const (
	FaultServiceKill        FaultType = "SERVICE_KILL"
	FaultKafkaDelay         FaultType = "KAFKA_DELAY"
	FaultKafkaDuplicate     FaultType = "KAFKA_DUPLICATE"
	FaultCassandraDelay     FaultType = "CASSANDRA_DELAY"
	FaultCassandraUnavail   FaultType = "CASSANDRA_UNAVAILABLE"
	FaultProviderTimeout    FaultType = "PROVIDER_TIMEOUT"
	FaultProvider503        FaultType = "PROVIDER_503"
	FaultNetworkTimeout     FaultType = "NETWORK_TIMEOUT"
	FaultRandomPodKill      FaultType = "RANDOM_POD_KILL"
)

type ExperimentStatus string

const (
	ExperimentStatusPending   ExperimentStatus = "PENDING"
	ExperimentStatusRunning   ExperimentStatus = "RUNNING"
	ExperimentStatusCompleted ExperimentStatus = "COMPLETED"
	ExperimentStatusFailed    ExperimentStatus = "FAILED"
)

type Experiment struct {
	ID             string                 `json:"id"`
	Target         string                 `json:"target"`
	FaultType      FaultType              `json:"fault_type"`
	Duration       time.Duration          `json:"duration"`
	ExpectedResult string                 `json:"expected_result"`
	ActualResult   string                 `json:"actual_result"`
	RecoveryTime   time.Duration          `json:"recovery_time"`
	Status         ExperimentStatus       `json:"status"`
	Metadata       map[string]string      `json:"metadata"`
	CreatedAt      time.Time              `json:"created_at"`
	StartedAt      *time.Time             `json:"started_at,omitempty"`
	CompletedAt    *time.Time             `json:"completed_at,omitempty"`
}

type ExperimentResult struct {
	ExperimentID  string        `json:"experiment_id"`
	Success       bool          `json:"success"`
	Duration      time.Duration `json:"duration"`
	RecoveryTime  time.Duration `json:"recovery_time"`
	ErrorMessage  string        `json:"error_message,omitempty"`
	AuditTrail    []AuditEntry  `json:"audit_trail"`
}

type AuditEntry struct {
	Action    string    `json:"action"`
	Timestamp time.Time `json:"timestamp"`
	Detail    string    `json:"detail"`
}

type FaultInjector interface {
	Inject(ctx context.Context, faultType FaultType, target string, duration time.Duration) error
	Recover(ctx context.Context, faultType FaultType, target string) error
	IsHealthy(ctx context.Context, target string) (bool, error)
}

type ChaosEngine struct {
	injector  FaultInjector
	checker   FinancialInvariantChecker
	logger    zerolog.Logger
	experiments map[string]*Experiment
	mu        sync.RWMutex
	auditLog  []AuditEntry
}

func NewChaosEngine(injector FaultInjector, checker FinancialInvariantChecker, logger zerolog.Logger) *ChaosEngine {
	return &ChaosEngine{
		injector:    injector,
		checker:     checker,
		logger:      logger,
		experiments: make(map[string]*Experiment),
		auditLog:    make([]AuditEntry, 0),
	}
}

func (e *ChaosEngine) RunExperiment(ctx context.Context, experiment *Experiment) (*ExperimentResult, error) {
	e.mu.Lock()
	experiment.ID = uuid.New().String()
	experiment.Status = ExperimentStatusRunning
	now := time.Now().UTC()
	experiment.StartedAt = &now
	experiment.CreatedAt = now
	e.experiments[experiment.ID] = experiment
	e.mu.Unlock()

	e.audit("EXPERIMENT_STARTED", fmt.Sprintf("experiment_id=%s target=%s fault=%s", experiment.ID, experiment.Target, experiment.FaultType))

	e.logger.Info().
		Str("experiment_id", experiment.ID).
		Str("target", experiment.Target).
		Str("fault_type", string(experiment.FaultType)).
		Dur("duration", experiment.Duration).
		Msg("starting chaos experiment")

	injectStart := time.Now()
	err := e.injector.Inject(ctx, experiment.FaultType, experiment.Target, experiment.Duration)
	if err != nil {
		e.mu.Lock()
		experiment.Status = ExperimentStatusFailed
		e.mu.Unlock()
		e.audit("INJECT_FAILED", fmt.Sprintf("experiment_id=%s error=%v", experiment.ID, err))
		return &ExperimentResult{
			ExperimentID: experiment.ID,
			Success:      false,
			ErrorMessage: fmt.Sprintf("fault injection failed: %v", err),
			AuditTrail:   e.getAuditTrail(),
		}, fmt.Errorf("injecting fault: %w", err)
	}

	e.audit("FAULT_INJECTED", fmt.Sprintf("experiment_id=%s fault_type=%s target=%s", experiment.ID, experiment.FaultType, experiment.Target))

	time.Sleep(experiment.Duration)

	recoverStart := time.Now()
	err = e.injector.Recover(ctx, experiment.FaultType, experiment.Target)
	if err != nil {
		e.mu.Lock()
		experiment.Status = ExperimentStatusFailed
		e.mu.Unlock()
		e.audit("RECOVER_FAILED", fmt.Sprintf("experiment_id=%s error=%v", experiment.ID, err))
		return &ExperimentResult{
			ExperimentID: experiment.ID,
			Success:      false,
			ErrorMessage: fmt.Sprintf("recovery failed: %v", err),
			AuditTrail:   e.getAuditTrail(),
		}, fmt.Errorf("recovering from fault: %w", err)
	}

	recoveryTime := time.Since(recoverStart)
	e.audit("FAULT_RECOVERED", fmt.Sprintf("experiment_id=%s recovery_time=%v", experiment.ID, recoveryTime))

	healthy, err := e.injector.IsHealthy(ctx, experiment.Target)
	if err != nil || !healthy {
		experiment.ActualResult = "UNHEALTHY"
		experiment.Status = ExperimentStatusFailed
		e.audit("HEALTH_CHECK_FAILED", fmt.Sprintf("experiment_id=%s healthy=%v error=%v", experiment.ID, healthy, err))
	} else {
		experiment.ActualResult = "HEALTHY"
		experiment.Status = ExperimentStatusCompleted
	}

	completedAt := time.Now().UTC()
	experiment.CompletedAt = &completedAt
	experiment.RecoveryTime = recoveryTime

	result := &ExperimentResult{
		ExperimentID: experiment.ID,
		Success:      experiment.Status == ExperimentStatusCompleted,
		Duration:     completedAt.Sub(*experiment.StartedAt),
		RecoveryTime: recoveryTime,
		AuditTrail:   e.getAuditTrail(),
	}

	e.audit("EXPERIMENT_COMPLETED", fmt.Sprintf("experiment_id=%s success=%v recovery_time=%v", experiment.ID, result.Success, recoveryTime))

	e.logger.Info().
		Str("experiment_id", experiment.ID).
		Bool("success", result.Success).
		Dur("recovery_time", recoveryTime).
		Msg("chaos experiment completed")

	return result, nil
}

func (e *ChaosEngine) ValidateFinancialInvariants(ctx context.Context) (*InvariantCheckResult, error) {
	e.audit("INVARIANT_CHECK_STARTED", "validating all financial invariants")

	result := &InvariantCheckResult{
		Timestamp: time.Now().UTC(),
	}

	ledgerResult, err := e.checker.CheckDebitsEqualCredits(ctx)
	if err != nil {
		e.audit("LEDGER_CHECK_FAILED", fmt.Sprintf("error=%v", err))
		result.LedgerBalanced = false
		result.Errors = append(result.Errors, fmt.Sprintf("ledger check failed: %v", err))
	} else {
		result.LedgerBalanced = ledgerResult.Balanced
		if !ledgerResult.Balanced {
			result.Errors = append(result.Errors, fmt.Sprintf("ledger unbalanced: debits=%d credits=%d", ledgerResult.TotalDebits, ledgerResult.TotalCredits))
		}
	}

	dupResult, err := e.checker.CheckNoDuplicatePayments(ctx)
	if err != nil {
		e.audit("DUPLICATE_CHECK_FAILED", fmt.Sprintf("error=%v", err))
		result.NoDuplicates = false
		result.Errors = append(result.Errors, fmt.Sprintf("duplicate check failed: %v", err))
	} else {
		result.NoDuplicates = !dupResult.HasDuplicates
		if dupResult.HasDuplicates {
			result.Errors = append(result.Errors, fmt.Sprintf("duplicates found: %d", dupResult.DuplicateCount))
		}
	}

	doubleSpend, err := e.checker.CheckNoDoubleSpend(ctx)
	if err != nil {
		e.audit("DOUBLE_SPEND_CHECK_FAILED", fmt.Sprintf("error=%v", err))
		result.NoDoubleSpend = false
		result.Errors = append(result.Errors, fmt.Sprintf("double spend check failed: %v", err))
	} else {
		result.NoDoubleSpend = !doubleSpend.HasDoubleSpend
		if doubleSpend.HasDoubleSpend {
			result.Errors = append(result.Errors, fmt.Sprintf("double spend detected: %d", doubleSpend.DoubleSpendCount))
		}
	}

	resResult, err := e.checker.CheckReservationConsistency(ctx)
	if err != nil {
		e.audit("RESERVATION_CHECK_FAILED", fmt.Sprintf("error=%v", err))
		result.ReservationsConsistent = false
		result.Errors = append(result.Errors, fmt.Sprintf("reservation check failed: %v", err))
	} else {
		result.ReservationsConsistent = resResult.Consistent
		if !resResult.Consistent {
			result.Errors = append(result.Errors, fmt.Sprintf("reservation inconsistency: %s", resResult.InconsistencyDetail))
		}
	}

	result.AllPassed = result.LedgerBalanced && result.NoDuplicates && result.NoDoubleSpend && result.ReservationsConsistent

	e.audit("INVARIANT_CHECK_COMPLETED", fmt.Sprintf("all_passed=%v errors=%d", result.AllPassed, len(result.Errors)))

	return result, nil
}

func (e *ChaosEngine) GetExperiment(id string) (*Experiment, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	exp, ok := e.experiments[id]
	return exp, ok
}

func (e *ChaosEngine) ListExperiments() []*Experiment {
	e.mu.RLock()
	defer e.mu.RUnlock()
	experiments := make([]*Experiment, 0, len(e.experiments))
	for _, exp := range e.experiments {
		experiments = append(experiments, exp)
	}
	return experiments
}

func (e *ChaosEngine) audit(action, detail string) {
	entry := AuditEntry{
		Action:    action,
		Timestamp: time.Now().UTC(),
		Detail:    detail,
	}
	e.mu.Lock()
	e.auditLog = append(e.auditLog, entry)
	e.mu.Unlock()
}

func (e *ChaosEngine) getAuditTrail() []AuditEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	entries := make([]AuditEntry, len(e.auditLog))
	copy(entries, e.auditLog)
	return entries
}

func DefaultFaultInjector() *DefaultChaosInjector {
	return &DefaultChaosInjector{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

type DefaultChaosInjector struct {
	rng *rand.Rand
}

func (d *DefaultChaosInjector) Inject(ctx context.Context, faultType FaultType, target string, duration time.Duration) error {
	switch faultType {
	case FaultServiceKill:
		return d.simulateServiceKill(ctx, target, duration)
	case FaultKafkaDelay:
		return d.simulateKafkaDelay(ctx, target, duration)
	case FaultKafkaDuplicate:
		return d.simulateKafkaDuplicate(ctx, target, duration)
	case FaultCassandraDelay:
		return d.simulateCassandraDelay(ctx, target, duration)
	case FaultCassandraUnavail:
		return d.simulateCassandraUnavailable(ctx, target, duration)
	case FaultProviderTimeout:
		return d.simulateProviderTimeout(ctx, target, duration)
	case FaultProvider503:
		return d.simulateProvider503(ctx, target, duration)
	case FaultNetworkTimeout:
		return d.simulateNetworkTimeout(ctx, target, duration)
	case FaultRandomPodKill:
		return d.simulateRandomPodKill(ctx, target, duration)
	default:
		return fmt.Errorf("unknown fault type: %s", faultType)
	}
}

func (d *DefaultChaosInjector) Recover(ctx context.Context, faultType FaultType, target string) error {
	return nil
}

func (d *DefaultChaosInjector) IsHealthy(ctx context.Context, target string) (bool, error) {
	return true, nil
}

func (d *DefaultChaosInjector) simulateServiceKill(_ context.Context, target string, _ time.Duration) error {
	time.Sleep(time.Duration(d.rng.Intn(100)) * time.Millisecond)
	return nil
}

func (d *DefaultChaosInjector) simulateKafkaDelay(_ context.Context, _ string, duration time.Duration) error {
	time.Sleep(duration / 2)
	return nil
}

func (d *DefaultChaosInjector) simulateKafkaDuplicate(_ context.Context, _ string, _ time.Duration) error {
	time.Sleep(time.Duration(d.rng.Intn(50)) * time.Millisecond)
	return nil
}

func (d *DefaultChaosInjector) simulateCassandraDelay(_ context.Context, _ string, duration time.Duration) error {
	time.Sleep(duration / 4)
	return nil
}

func (d *DefaultChaosInjector) simulateCassandraUnavailable(_ context.Context, _ string, duration time.Duration) error {
	time.Sleep(duration)
	return nil
}

func (d *DefaultChaosInjector) simulateProviderTimeout(_ context.Context, _ string, _ time.Duration) error {
	time.Sleep(time.Duration(d.rng.Intn(200)) * time.Millisecond)
	return nil
}

func (d *DefaultChaosInjector) simulateProvider503(_ context.Context, _ string, _ time.Duration) error {
	time.Sleep(time.Duration(d.rng.Intn(100)) * time.Millisecond)
	return nil
}

func (d *DefaultChaosInjector) simulateNetworkTimeout(_ context.Context, _ string, duration time.Duration) error {
	time.Sleep(duration / 3)
	return nil
}

func (d *DefaultChaosInjector) simulateRandomPodKill(_ context.Context, _ string, _ time.Duration) error {
	time.Sleep(time.Duration(d.rng.Intn(50)) * time.Millisecond)
	return nil
}

type InvariantCheckResult struct {
	LedgerBalanced         bool      `json:"ledger_balanced"`
	NoDuplicates           bool      `json:"no_duplicates"`
	NoDoubleSpend          bool      `json:"no_double_spend"`
	ReservationsConsistent bool      `json:"reservations_consistent"`
	AllPassed              bool      `json:"all_passed"`
	Errors                 []string  `json:"errors"`
	Timestamp              time.Time `json:"timestamp"`
}
