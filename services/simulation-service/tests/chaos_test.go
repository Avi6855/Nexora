package tests

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/simulation-service/internal/chaos"
)

func TestChaosEngine_ExperimentLifecycle(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	experiment := &chaos.Experiment{
		Target:         "payment-service",
		FaultType:      chaos.FaultProvider503,
		Duration:       50 * time.Millisecond,
		ExpectedResult: "HEALTHY",
	}

	result, err := engine.RunExperiment(context.Background(), experiment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Error("expected experiment to succeed")
	}

	stored, ok := engine.GetExperiment(result.ExperimentID)
	if !ok {
		t.Error("expected to find experiment by ID")
	}

	if stored.Status != chaos.ExperimentStatusCompleted {
		t.Errorf("expected status COMPLETED, got %s", stored.Status)
	}

	if stored.ActualResult != "HEALTHY" {
		t.Errorf("expected actual result HEALTHY, got %s", stored.ActualResult)
	}
}

func TestChaosEngine_MultipleExperiments(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	faultTypes := []chaos.FaultType{
		chaos.FaultKafkaDelay,
		chaos.FaultCassandraDelay,
		chaos.FaultProviderTimeout,
	}

	for i, ft := range faultTypes {
		experiment := &chaos.Experiment{
			Target:    "test-service",
			FaultType: ft,
			Duration:  10 * time.Millisecond,
			Metadata:  map[string]string{"index": string(rune('0' + i))},
		}

		result, err := engine.RunExperiment(context.Background(), experiment)
		if err != nil {
			t.Fatalf("experiment %d failed: %v", i, err)
		}

		if !result.Success {
			t.Errorf("experiment %d did not succeed", i)
		}
	}

	experiments := engine.ListExperiments()
	if len(experiments) != len(faultTypes) {
		t.Errorf("expected %d experiments, got %d", len(faultTypes), len(experiments))
	}
}

func TestChaosEngine_AuditTrail(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	experiment := &chaos.Experiment{
		Target:    "test-service",
		FaultType: chaos.FaultServiceKill,
		Duration:  10 * time.Millisecond,
	}

	result, err := engine.RunExperiment(context.Background(), experiment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.AuditTrail) < 3 {
		t.Errorf("expected at least 3 audit entries, got %d", len(result.AuditTrail))
	}

	actions := make(map[string]bool)
	for _, entry := range result.AuditTrail {
		actions[entry.Action] = true
	}

	if !actions["EXPERIMENT_STARTED"] {
		t.Error("expected EXPERIMENT_STARTED in audit trail")
	}

	if !actions["FAULT_INJECTED"] {
		t.Error("expected FAULT_INJECTED in audit trail")
	}

	if !actions["FAULT_RECOVERED"] {
		t.Error("expected FAULT_RECOVERED in audit trail")
	}
}

func TestDefaultChaosInjector_InjectRecover(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	ctx := context.Background()

	tests := []struct {
		name      string
		faultType chaos.FaultType
	}{
		{"service kill", chaos.FaultServiceKill},
		{"kafka delay", chaos.FaultKafkaDelay},
		{"kafka duplicate", chaos.FaultKafkaDuplicate},
		{"cassandra delay", chaos.FaultCassandraDelay},
		{"cassandra unavailable", chaos.FaultCassandraUnavail},
		{"provider timeout", chaos.FaultProviderTimeout},
		{"provider 503", chaos.FaultProvider503},
		{"network timeout", chaos.FaultNetworkTimeout},
		{"random pod kill", chaos.FaultRandomPodKill},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := injector.Inject(ctx, tt.faultType, "test-target", 10*time.Millisecond)
			if err != nil {
				t.Fatalf("failed to inject: %v", err)
			}

			err = injector.Recover(ctx, tt.faultType, "test-target")
			if err != nil {
				t.Fatalf("failed to recover: %v", err)
			}

			healthy, err := injector.IsHealthy(ctx, "test-target")
			if err != nil {
				t.Fatalf("health check failed: %v", err)
			}

			if !healthy {
				t.Error("expected healthy after recovery")
			}
		})
	}
}

func TestChaosEngine_ExperimentNotFound(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	_, ok := engine.GetExperiment("non-existent-id")
	if ok {
		t.Error("expected not to find non-existent experiment")
	}
}

func TestFinancialInvariantChecker_Comprehensive(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-1",
		EntryType:     "DEBIT",
		Amount:        500,
		Currency:      "GBP",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-2",
		TransactionID: "tx-1",
		EntryType:     "CREDIT",
		Amount:        500,
		Currency:      "GBP",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-3",
		TransactionID: "tx-2",
		EntryType:     "DEBIT",
		Amount:        300,
		Currency:      "USD",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-4",
		TransactionID: "tx-2",
		EntryType:     "CREDIT",
		Amount:        300,
		Currency:      "USD",
	})

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-1",
		IdempotencyKey: "key-1",
		TotalAmount:    500,
		Currency:       "GBP",
	})

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-2",
		IdempotencyKey: "key-2",
		TotalAmount:    300,
		Currency:       "USD",
	})

	provider.AddReservation(&chaos.Reservation{
		ReservationID: "res-1",
		AccountID:     "account-1",
		TransactionID: "tx-1",
		Amount:        500,
		Status:        "ACTIVE",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)

	t.Run("ledger balance", func(t *testing.T) {
		result, err := checker.CheckDebitsEqualCredits(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Balanced {
			t.Error("expected ledger to be balanced")
		}
		if result.TotalDebits != 800 {
			t.Errorf("expected total debits 800, got %d", result.TotalDebits)
		}
		if result.TotalCredits != 800 {
			t.Errorf("expected total credits 800, got %d", result.TotalCredits)
		}
	})

	t.Run("no duplicates", func(t *testing.T) {
		result, err := checker.CheckNoDuplicatePayments(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.HasDuplicates {
			t.Error("expected no duplicates")
		}
	})

	t.Run("no double spend", func(t *testing.T) {
		result, err := checker.CheckNoDoubleSpend(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.HasDoubleSpend {
			t.Error("expected no double spend")
		}
	})

	t.Run("reservation consistency", func(t *testing.T) {
		result, err := checker.CheckReservationConsistency(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Consistent {
			t.Errorf("expected consistency, got: %s", result.InconsistencyDetail)
		}
	})
}

func TestExperimentResult_Duration(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	experiment := &chaos.Experiment{
		Target:    "test-service",
		FaultType: chaos.FaultNetworkTimeout,
		Duration:  25 * time.Millisecond,
	}

	start := time.Now()
	result, err := engine.RunExperiment(context.Background(), experiment)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}

	if result.Duration > elapsed {
		t.Error("experiment duration should not exceed total elapsed time")
	}
}
