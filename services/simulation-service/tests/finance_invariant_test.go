package tests

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/simulation-service/internal/chaos"
)

func TestCheckDebitsEqualCredits_Balanced(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-1",
		EntryType:     "DEBIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-2",
		TransactionID: "tx-1",
		EntryType:     "CREDIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	result, err := checker.CheckDebitsEqualCredits(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Balanced {
		t.Error("expected debits to equal credits")
	}

	if result.TotalDebits != 1000 {
		t.Errorf("expected total debits 1000, got %d", result.TotalDebits)
	}

	if result.TotalCredits != 1000 {
		t.Errorf("expected total credits 1000, got %d", result.TotalCredits)
	}
}

func TestCheckDebitsEqualCredits_Unbalanced(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-1",
		EntryType:     "DEBIT",
		Amount:        1500,
		Currency:      "GBP",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-2",
		TransactionID: "tx-1",
		EntryType:     "CREDIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	result, err := checker.CheckDebitsEqualCredits(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Balanced {
		t.Error("expected debits to not equal credits")
	}

	if result.TotalDebits != 1500 {
		t.Errorf("expected total debits 1500, got %d", result.TotalDebits)
	}

	if result.TotalCredits != 1000 {
		t.Errorf("expected total credits 1000, got %d", result.TotalCredits)
	}
}

func TestCheckNoDuplicatePayments_NoDuplicates(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-1",
		IdempotencyKey: "key-1",
		TotalAmount:    1000,
		Currency:       "GBP",
	})

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-2",
		IdempotencyKey: "key-2",
		TotalAmount:    2000,
		Currency:       "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	result, err := checker.CheckNoDuplicatePayments(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasDuplicates {
		t.Error("expected no duplicates")
	}

	if result.DuplicateCount != 0 {
		t.Errorf("expected duplicate count 0, got %d", result.DuplicateCount)
	}
}

func TestCheckNoDuplicatePayments_WithDuplicates(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-1",
		IdempotencyKey: "key-1",
		TotalAmount:    1000,
		Currency:       "GBP",
	})

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-2",
		IdempotencyKey: "key-1",
		TotalAmount:    1000,
		Currency:       "GBP",
	})

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-3",
		IdempotencyKey: "key-1",
		TotalAmount:    1000,
		Currency:       "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	result, err := checker.CheckNoDuplicatePayments(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasDuplicates {
		t.Error("expected duplicates to be detected")
	}

	if result.DuplicateCount != 2 {
		t.Errorf("expected duplicate count 2, got %d", result.DuplicateCount)
	}
}

func TestCheckNoDoubleSpend_NoDoubleSpend(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-1",
		EntryType:     "DEBIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-2",
		EntryType:     "DEBIT",
		Amount:        500,
		Currency:      "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	result, err := checker.CheckNoDoubleSpend(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasDoubleSpend {
		t.Error("expected no double spend")
	}
}

func TestCheckReservationConsistency_Consistent(t *testing.T) {
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddReservation(&chaos.Reservation{
		ReservationID: "res-1",
		AccountID:     "account-1",
		TransactionID: "tx-1",
		Amount:        1000,
		Status:        "ACTIVE",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-1",
		EntryType:     "CREDIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	result, err := checker.CheckReservationConsistency(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Consistent {
		t.Errorf("expected consistency, got: %s", result.InconsistencyDetail)
	}
}

func TestChaosEngine_RunExperiment(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	experiment := &chaos.Experiment{
		Target:         "payment-service",
		FaultType:      chaos.FaultProviderTimeout,
		Duration:       100 * time.Millisecond,
		ExpectedResult: "HEALTHY",
		Metadata:       map[string]string{"test": "true"},
	}

	result, err := engine.RunExperiment(context.Background(), experiment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Error("expected experiment to succeed")
	}

	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}

	if len(result.AuditTrail) == 0 {
		t.Error("expected audit trail entries")
	}
}

func TestChaosEngine_ListExperiments(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	checker := chaos.NewDefaultFinancialInvariantChecker(chaos.NewInMemoryLedgerProvider())
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	experiment1 := &chaos.Experiment{
		Target:    "payment-service",
		FaultType: chaos.FaultProviderTimeout,
		Duration:  50 * time.Millisecond,
	}

	experiment2 := &chaos.Experiment{
		Target:    "kafka",
		FaultType: chaos.FaultKafkaDelay,
		Duration:  50 * time.Millisecond,
	}

	_, err := engine.RunExperiment(context.Background(), experiment1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = engine.RunExperiment(context.Background(), experiment2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	experiments := engine.ListExperiments()
	if len(experiments) != 2 {
		t.Errorf("expected 2 experiments, got %d", len(experiments))
	}
}

func TestChaosEngine_ValidateFinancialInvariants(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	provider := chaos.NewInMemoryLedgerProvider()

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-1",
		TransactionID: "tx-1",
		EntryType:     "DEBIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	provider.AddEntry(&chaos.LedgerEntry{
		AccountID:     "account-2",
		TransactionID: "tx-1",
		EntryType:     "CREDIT",
		Amount:        1000,
		Currency:      "GBP",
	})

	provider.AddTransaction(&chaos.LedgerTransaction{
		TransactionID:  "tx-1",
		IdempotencyKey: "key-1",
		TotalAmount:    1000,
		Currency:       "GBP",
	})

	checker := chaos.NewDefaultFinancialInvariantChecker(provider)
	logger := zerolog.Nop()

	engine := chaos.NewChaosEngine(injector, checker, logger)

	result, err := engine.ValidateFinancialInvariants(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.AllPassed {
		t.Errorf("expected all invariants to pass, errors: %v", result.Errors)
	}
}

func TestDefaultFaultInjector_AllFaultTypes(t *testing.T) {
	injector := chaos.DefaultFaultInjector()
	ctx := context.Background()

	faultTypes := []chaos.FaultType{
		chaos.FaultServiceKill,
		chaos.FaultKafkaDelay,
		chaos.FaultKafkaDuplicate,
		chaos.FaultCassandraDelay,
		chaos.FaultCassandraUnavail,
		chaos.FaultProviderTimeout,
		chaos.FaultProvider503,
		chaos.FaultNetworkTimeout,
		chaos.FaultRandomPodKill,
	}

	for _, ft := range faultTypes {
		t.Run(string(ft), func(t *testing.T) {
			err := injector.Inject(ctx, ft, "test-target", 10*time.Millisecond)
			if err != nil {
				t.Errorf("failed to inject fault %s: %v", ft, err)
			}

			err = injector.Recover(ctx, ft, "test-target")
			if err != nil {
				t.Errorf("failed to recover from fault %s: %v", ft, err)
			}

			healthy, err := injector.IsHealthy(ctx, "test-target")
			if err != nil {
				t.Errorf("health check failed for fault %s: %v", ft, err)
			}
			if !healthy {
				t.Errorf("expected healthy after recovery for fault %s", ft)
			}
		})
	}
}

func TestInvariantCheckResult_AllPassed(t *testing.T) {
	result := &chaos.InvariantCheckResult{
		LedgerBalanced:         true,
		NoDuplicates:           true,
		NoDoubleSpend:          true,
		ReservationsConsistent: true,
		AllPassed:              true,
		Errors:                 make([]string, 0),
		Timestamp:              time.Now().UTC(),
	}

	if !result.AllPassed {
		t.Error("expected all invariants to pass")
	}

	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %d", len(result.Errors))
	}
}

func TestInvariantCheckResult_SomeFailed(t *testing.T) {
	result := &chaos.InvariantCheckResult{
		LedgerBalanced:         false,
		NoDuplicates:           true,
		NoDoubleSpend:          false,
		ReservationsConsistent: true,
		AllPassed:              false,
		Errors: []string{
			"ledger unbalanced: debits=1500 credits=1000",
			"double spend detected: 1",
		},
		Timestamp: time.Now().UTC(),
	}

	if result.AllPassed {
		t.Error("expected some invariants to fail")
	}

	if len(result.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(result.Errors))
	}
}
