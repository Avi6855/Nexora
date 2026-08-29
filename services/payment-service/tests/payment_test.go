package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/service"
)

type InMemoryPaymentRepository struct {
	payments  map[uuid.UUID]*domain.Payment
	byKey     map[string]*domain.Payment
	byAccount map[uuid.UUID][]*domain.Payment
}

func NewInMemoryPaymentRepository() *InMemoryPaymentRepository {
	return &InMemoryPaymentRepository{
		payments:  make(map[uuid.UUID]*domain.Payment),
		byKey:     make(map[string]*domain.Payment),
		byAccount: make(map[uuid.UUID][]*domain.Payment),
	}
}

func (r *InMemoryPaymentRepository) Create(ctx context.Context, payment *domain.Payment) error {
	r.payments[payment.PaymentID] = payment
	r.byKey[payment.IdempotencyKey] = payment
	r.byAccount[payment.AccountID] = append(r.byAccount[payment.AccountID], payment)
	return nil
}

func (r *InMemoryPaymentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	p, ok := r.payments[id]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (r *InMemoryPaymentRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error) {
	p, ok := r.byKey[key]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (r *InMemoryPaymentRepository) GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error) {
	return r.byAccount[accountID], nil
}

func (r *InMemoryPaymentRepository) Update(ctx context.Context, payment *domain.Payment) error {
	r.payments[payment.PaymentID] = payment
	return nil
}

func setupTestService(pvd provider.Behavior) (*service.PaymentService, *InMemoryPaymentRepository, *events.KafkaEventPublisher) {
	repo := NewInMemoryPaymentRepository()
	logger := zerolog.Nop()
	mockProvider := provider.NewMockProvider(provider.MockProviderConfig{
		ProviderID:      "test-provider",
		DefaultBehavior: pvd,
		Delay:           10 * time.Millisecond,
	})
	eventPublisher := events.NewKafkaEventPublisher(events.KafkaPublisherConfig{
		ProducerID:  "test-service",
		Brokers:     []string{"localhost:9092"},
		TopicPrefix: "test",
		Logger:      logger,
	})
	paymentService := service.NewPaymentService(repo, mockProvider, eventPublisher, logger)
	return paymentService, repo, eventPublisher
}

func TestPaymentLifecycle(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "test-key-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           10000,
		Currency:         "GBP",
		CounterpartyID:   "cp-001",
		CounterpartyName: "Test Payee",
		Reference:        "Test payment",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateCreated {
		t.Errorf("expected state CREATED, got %s", payment.State)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateAuthorized {
		t.Errorf("expected state AUTHORIZED, got %s", payment.State)
	}

	payment, err = paymentService.ProcessPayment(ctx, payment.PaymentID, &domain.ProcessPaymentRequest{})
	if err != nil {
		t.Fatalf("ProcessPayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateSettled {
		t.Errorf("expected state SETTLED, got %s", payment.State)
	}

	if payment.SettledAt == nil {
		t.Error("expected settled_at to be set")
	}
}

func TestIdempotency(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "idempotent-key-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           5000,
		Currency:         "GBP",
		CounterpartyID:   "cp-002",
		CounterpartyName: "Idempotent Payee",
		Reference:        "Idempotent test",
	}

	payment1, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("first CreatePayment failed: %v", err)
	}

	payment2, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("second CreatePayment failed: %v", err)
	}

	if payment1.PaymentID != payment2.PaymentID {
		t.Errorf("expected same payment ID for idempotent request, got %s and %s", payment1.PaymentID, payment2.PaymentID)
	}

	if payment1.IdempotencyKey != payment2.IdempotencyKey {
		t.Errorf("expected same idempotency key, got %s and %s", payment1.IdempotencyKey, payment2.IdempotencyKey)
	}
}

func TestInvalidTransition(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "transition-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           3000,
		Currency:         "USD",
		CounterpartyID:   "cp-003",
		CounterpartyName: "Transition Test Payee",
		Reference:        "Transition test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	_, err = paymentService.ProcessPayment(ctx, payment.PaymentID, &domain.ProcessPaymentRequest{})
	if err == nil {
		t.Error("expected error when processing payment in CREATED state, got nil")
	}

	_, err = paymentService.SettlePayment(ctx, payment.PaymentID)
	if err == nil {
		t.Error("expected error when settling payment in CREATED state, got nil")
	}
}

func TestUnknownState(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorUnknown)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "unknown-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           7500,
		Currency:         "EUR",
		CounterpartyID:   "cp-004",
		CounterpartyName: "Unknown Test Payee",
		Reference:        "Unknown state test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	payment, err = paymentService.ProcessPayment(ctx, payment.PaymentID, &domain.ProcessPaymentRequest{})
	if err != nil {
		t.Fatalf("ProcessPayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateUnknown {
		t.Errorf("expected state UNKNOWN, got %s", payment.State)
	}
}

func TestProviderTimeout(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorTimeout)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "timeout-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           10000,
		Currency:         "GBP",
		CounterpartyID:   "cp-005",
		CounterpartyName: "Timeout Test Payee",
		Reference:        "Timeout test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	payment, err = paymentService.ProcessPayment(timeoutCtx, payment.PaymentID, &domain.ProcessPaymentRequest{})
	if err != nil {
		t.Fatalf("ProcessPayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateUnknown {
		t.Errorf("expected state UNKNOWN after timeout, got %s", payment.State)
	}
}

func TestCancelPayment(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "cancel-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeBankTransfer,
		Amount:           25000,
		Currency:         "GBP",
		CounterpartyID:   "cp-006",
		CounterpartyName: "Cancel Test Payee",
		Reference:        "Cancel test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.CancelPayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("CancelPayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateCancelled {
		t.Errorf("expected state CANCELLED, got %s", payment.State)
	}
}

func TestCancelAuthorizedPayment(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "cancel-auth-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           8000,
		Currency:         "USD",
		CounterpartyID:   "cp-007",
		CounterpartyName: "Cancel Auth Test Payee",
		Reference:        "Cancel authorized test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	payment, err = paymentService.CancelPayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("CancelPayment after authorize failed: %v", err)
	}

	if payment.State != domain.PaymentStateCancelled {
		t.Errorf("expected state CANCELLED after authorize, got %s", payment.State)
	}
}

func TestCannotCancelProcessingPayment(t *testing.T) {
	paymentService, repo := setupTestService(provider.BehaviorTimeout)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "cancel-processing-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           4000,
		Currency:         "GBP",
		CounterpartyID:   "cp-008",
		CounterpartyName: "Cancel Processing Test Payee",
		Reference:        "Cancel processing test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	payment, _ = repo.GetByID(ctx, payment.PaymentID)
	payment.TransitionTo(domain.PaymentStateProcessing)
	repo.Update(ctx, payment)

	_, err = paymentService.CancelPayment(ctx, payment.PaymentID)
	if err == nil {
		t.Error("expected error when cancelling payment in PROCESSING state, got nil")
	}
}

func TestPaymentStateTransitions(t *testing.T) {
	tests := []struct {
		name     string
		from     domain.PaymentState
		to       domain.PaymentState
		expected bool
	}{
		{"created to authorized", domain.PaymentStateCreated, domain.PaymentStateAuthorized, true},
		{"created to failed", domain.PaymentStateCreated, domain.PaymentStateFailed, true},
		{"created to reversed", domain.PaymentStateCreated, domain.PaymentStateReversed, true},
		{"created to cancelled", domain.PaymentStateCreated, domain.PaymentStateCancelled, true},
		{"created to processing", domain.PaymentStateCreated, domain.PaymentStateProcessing, false},
		{"authorized to processing", domain.PaymentStateAuthorized, domain.PaymentStateProcessing, true},
		{"authorized to cancelled", domain.PaymentStateAuthorized, domain.PaymentStateCancelled, true},
		{"processing to confirmed", domain.PaymentStateProcessing, domain.PaymentStateConfirmed, true},
		{"processing to failed", domain.PaymentStateProcessing, domain.PaymentStateFailed, true},
		{"processing to unknown", domain.PaymentStateProcessing, domain.PaymentStateUnknown, true},
		{"unknown to confirmed", domain.PaymentStateUnknown, domain.PaymentStateConfirmed, true},
		{"unknown to failed", domain.PaymentStateUnknown, domain.PaymentStateFailed, true},
		{"confirmed to settled", domain.PaymentStateConfirmed, domain.PaymentStateSettled, true},
		{"confirmed to reversed", domain.PaymentStateConfirmed, domain.PaymentStateReversed, true},
		{"settled to anything", domain.PaymentStateSettled, domain.PaymentStateCreated, false},
		{"failed to anything", domain.PaymentStateFailed, domain.PaymentStateCreated, false},
		{"reversed to anything", domain.PaymentStateReversed, domain.PaymentStateCreated, false},
		{"cancelled to anything", domain.PaymentStateCancelled, domain.PaymentStateCreated, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.from.CanTransitionTo(tt.to)
			if result != tt.expected {
				t.Errorf("CanTransitionTo(%s, %s) = %v, want %v", tt.from, tt.to, result, tt.expected)
			}
		})
	}
}

func TestMockProviderBehaviors(t *testing.T) {
	ctx := context.Background()
	req := &provider.PaymentRequest{
		PaymentID:      uuid.New().String(),
		AccountID:      uuid.New().String(),
		Amount:         1000,
		Currency:       "GBP",
		CounterpartyID: "cp-test",
		Reference:      "test",
	}

	t.Run("always succeed", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorAlwaysSucceed,
			Delay:           10 * time.Millisecond,
		})
		result, err := p.ProcessPayment(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "SUCCESS" {
			t.Errorf("expected status SUCCESS, got %s", result.Status)
		}
		if p.GetCallCount() != 1 {
			t.Errorf("expected call count 1, got %d", p.GetCallCount())
		}
	})

	t.Run("always fail", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorAlwaysFail,
			Delay:           10 * time.Millisecond,
		})
		result, err := p.ProcessPayment(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "FAILED" {
			t.Errorf("expected status FAILED, got %s", result.Status)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorTimeout,
			Delay:           10 * time.Millisecond,
		})
		timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		_, err := p.ProcessPayment(timeoutCtx, req)
		if err == nil {
			t.Error("expected error for timeout, got nil")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorUnknown,
			Delay:           10 * time.Millisecond,
		})
		result, err := p.ProcessPayment(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "UNKNOWN" {
			t.Errorf("expected status UNKNOWN, got %s", result.Status)
		}
	})

	t.Run("503 error", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorError503,
			Delay:           10 * time.Millisecond,
		})
		_, err := p.ProcessPayment(ctx, req)
		if err == nil {
			t.Error("expected error for 503, got nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorNetworkError,
			Delay:           10 * time.Millisecond,
		})
		_, err := p.ProcessPayment(ctx, req)
		if err == nil {
			t.Error("expected error for network error, got nil")
		}
	})

	t.Run("reset", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorAlwaysSucceed,
			Delay:           10 * time.Millisecond,
		})
		p.ProcessPayment(ctx, req)
		p.ProcessPayment(ctx, req)
		if p.GetCallCount() != 2 {
			t.Errorf("expected call count 2, got %d", p.GetCallCount())
		}
		p.Reset()
		if p.GetCallCount() != 0 {
			t.Errorf("expected call count 0 after reset, got %d", p.GetCallCount())
		}
	})

	t.Run("set behavior", func(t *testing.T) {
		p := provider.NewMockProvider(provider.MockProviderConfig{
			DefaultBehavior: provider.BehaviorAlwaysSucceed,
			Delay:           10 * time.Millisecond,
		})
		p.SetDefaultBehavior(provider.BehaviorAlwaysFail)
		result, err := p.ProcessPayment(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "FAILED" {
			t.Errorf("expected status FAILED after behavior change, got %s", result.Status)
		}
	})
}

func TestPaymentMetadata(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "metadata-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           1000,
		Currency:         "GBP",
		CounterpartyID:   "cp-009",
		CounterpartyName: "Metadata Test Payee",
		Reference:        "Metadata test",
		Metadata: map[string]string{
			"source":  "mobile_app",
			"version": "1.0",
		},
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	if payment.Metadata == nil {
		t.Fatal("expected metadata to be set")
	}

	if payment.Metadata["source"] != "mobile_app" {
		t.Errorf("expected metadata source to be mobile_app, got %s", payment.Metadata["source"])
	}

	if payment.Metadata["version"] != "1.0" {
		t.Errorf("expected metadata version to be 1.0, got %s", payment.Metadata["version"])
	}
}

func TestPaymentEventsPublished(t *testing.T) {
	paymentService, _, eventPublisher := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "events-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           10000,
		Currency:         "GBP",
		CounterpartyID:   "cp-010",
		CounterpartyName: "Events Test Payee",
		Reference:        "Events test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	publishedEvents := eventPublisher.GetPublishedEvents()
	if len(publishedEvents) != 1 {
		t.Fatalf("expected 1 event after create, got %d", len(publishedEvents))
	}

	if publishedEvents[0].EventType != events.EventTypePaymentCreated {
		t.Errorf("expected event type payment.created, got %s", publishedEvents[0].EventType)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	publishedEvents = eventPublisher.GetPublishedEvents()
	if len(publishedEvents) != 2 {
		t.Fatalf("expected 2 events after authorize, got %d", len(publishedEvents))
	}

	if publishedEvents[1].EventType != events.EventTypePaymentAuthorized {
		t.Errorf("expected event type payment.authorized, got %s", publishedEvents[1].EventType)
	}

	payment, err = paymentService.ProcessPayment(ctx, payment.PaymentID, &domain.ProcessPaymentRequest{})
	if err != nil {
		t.Fatalf("ProcessPayment failed: %v", err)
	}

	publishedEvents = eventPublisher.GetPublishedEvents()
	if len(publishedEvents) < 4 {
		t.Fatalf("expected at least 4 events after process, got %d", len(publishedEvents))
	}

	eventTypes := make([]events.EventType, len(publishedEvents))
	for i, e := range publishedEvents {
		eventTypes[i] = e.EventType
	}

	hasProcessing := false
	hasConfirmed := false
	hasSettled := false
	for _, et := range eventTypes {
		if et == events.EventTypePaymentProcessing {
			hasProcessing = true
		}
		if et == events.EventTypePaymentConfirmed {
			hasConfirmed = true
		}
		if et == events.EventTypePaymentSettled {
			hasSettled = true
		}
	}

	if !hasProcessing {
		t.Error("expected payment.processing event")
	}
	if !hasConfirmed {
		t.Error("expected payment.confirmed event")
	}
	if !hasSettled {
		t.Error("expected payment.settled event")
	}
}

func TestProviderCallback(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "callback-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           5000,
		Currency:         "GBP",
		CounterpartyID:   "cp-011",
		CounterpartyName: "Callback Test Payee",
		Reference:        "Callback test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	payment, _ = paymentService.GetPayment(ctx, payment.PaymentID)
	payment.TransitionTo(domain.PaymentStateProcessing)
	_ = paymentService.SettlePayment(ctx, payment.PaymentID)

	callback := &domain.ProviderCallbackRequest{
		PaymentID:      payment.PaymentID.String(),
		ProviderID:     "test-provider",
		TransactionRef: "TXN-123",
		Status:         "SUCCESS",
		Message:        "Payment confirmed",
	}

	payment, err = paymentService.HandleProviderCallback(ctx, callback)
	if err != nil {
		t.Fatalf("HandleProviderCallback failed: %v", err)
	}

	if payment.State != domain.PaymentStateSettled {
		t.Errorf("expected state SETTLED after callback, got %s", payment.State)
	}
}

func TestPaymentFailedProvider(t *testing.T) {
	paymentService, _, _ := setupTestService(provider.BehaviorAlwaysFail)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "fail-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           3000,
		Currency:         "USD",
		CounterpartyID:   "cp-012",
		CounterpartyName: "Fail Test Payee",
		Reference:        "Fail test",
	}

	payment, err := paymentService.CreatePayment(ctx, createReq)
	if err != nil {
		t.Fatalf("CreatePayment failed: %v", err)
	}

	payment, err = paymentService.AuthorizePayment(ctx, payment.PaymentID)
	if err != nil {
		t.Fatalf("AuthorizePayment failed: %v", err)
	}

	payment, err = paymentService.ProcessPayment(ctx, payment.PaymentID, &domain.ProcessPaymentRequest{})
	if err != nil {
		t.Fatalf("ProcessPayment failed: %v", err)
	}

	if payment.State != domain.PaymentStateFailed {
		t.Errorf("expected state FAILED, got %s", payment.State)
	}

	if payment.FailureReason == "" {
		t.Error("expected failure reason to be set")
	}
}
