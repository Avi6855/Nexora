package integration

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

type InMemoryPaymentRepo struct {
	payments  map[uuid.UUID]*domain.Payment
	byKey     map[string]*domain.Payment
	byAccount map[uuid.UUID][]*domain.Payment
}

func NewInMemoryPaymentRepo() *InMemoryPaymentRepo {
	return &InMemoryPaymentRepo{
		payments:  make(map[uuid.UUID]*domain.Payment),
		byKey:     make(map[string]*domain.Payment),
		byAccount: make(map[uuid.UUID][]*domain.Payment),
	}
}

func (r *InMemoryPaymentRepo) Create(ctx context.Context, payment *domain.Payment) error {
	r.payments[payment.PaymentID] = payment
	r.byKey[payment.IdempotencyKey] = payment
	r.byAccount[payment.AccountID] = append(r.byAccount[payment.AccountID], payment)
	return nil
}

func (r *InMemoryPaymentRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	p, ok := r.payments[id]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (r *InMemoryPaymentRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error) {
	p, ok := r.byKey[key]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (r *InMemoryPaymentRepo) GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error) {
	return r.byAccount[accountID], nil
}

func (r *InMemoryPaymentRepo) Update(ctx context.Context, payment *domain.Payment) error {
	r.payments[payment.PaymentID] = payment
	return nil
}

func setupPaymentIntegrationService(pvd provider.Behavior) (*service.PaymentService, *InMemoryPaymentRepo) {
	repo := NewInMemoryPaymentRepo()
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
	return paymentService, repo
}

func TestFullPaymentLifecycle(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "lifecycle-test-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           10000,
		Currency:         "GBP",
		CounterpartyID:   "cp-001",
		CounterpartyName: "Test Payee",
		Reference:        "Lifecycle test",
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
}

func TestIdempotencyIntegration(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "idempotent-integration-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           5000,
		Currency:         "GBP",
		CounterpartyID:   "cp-002",
		CounterpartyName: "Idempotent Payee",
		Reference:        "Idempotent integration test",
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
		t.Errorf("expected same payment ID, got %s and %s", payment1.PaymentID, payment2.PaymentID)
	}
}

func TestStateTransitionsIntegration(t *testing.T) {
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

func TestUnknownStateHandling(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorUnknown)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "unknown-integration-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           7500,
		Currency:         "EUR",
		CounterpartyID:   "cp-004",
		CounterpartyName: "Unknown Test Payee",
		Reference:        "Unknown state integration test",
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

func TestProviderCallbackIntegration(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "callback-integration-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           5000,
		Currency:         "GBP",
		CounterpartyID:   "cp-011",
		CounterpartyName: "Callback Test Payee",
		Reference:        "Callback integration test",
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

func TestFailedProviderIntegration(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorAlwaysFail)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "fail-integration-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           3000,
		Currency:         "USD",
		CounterpartyID:   "cp-012",
		CounterpartyName: "Fail Test Payee",
		Reference:        "Fail integration test",
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

func TestCancelPaymentIntegration(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "cancel-integration-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeBankTransfer,
		Amount:           25000,
		Currency:         "GBP",
		CounterpartyID:   "cp-006",
		CounterpartyName: "Cancel Test Payee",
		Reference:        "Cancel integration test",
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

func TestPaymentMetadataIntegration(t *testing.T) {
	paymentService, _ := setupPaymentIntegrationService(provider.BehaviorAlwaysSucceed)
	ctx := context.Background()

	createReq := &domain.CreatePaymentRequest{
		IdempotencyKey:   "metadata-integration-001",
		AccountID:        uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           1000,
		Currency:         "GBP",
		CounterpartyID:   "cp-009",
		CounterpartyName: "Metadata Test Payee",
		Reference:        "Metadata integration test",
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
}
