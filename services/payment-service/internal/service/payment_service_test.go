package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/repository"
	"github.com/nexora/nexora/shared/outbox"
)

// ── Test doubles ────────────────────────────────────────────────────────────
//
// fakeRepo is an in-memory PaymentRepository with the same compare-and-set
// semantics as the Cassandra LWT claim (ClaimIdempotencyKey is mutually
// exclusive), but WITHOUT AtomicEventWriter, so the saga takes the fallback
// update-then-publish path.
type fakeRepo struct {
	mu        sync.Mutex
	payments  map[uuid.UUID]*domain.Payment
	byKey     map[string]*domain.Payment
	claims    map[string]uuid.UUID
	creates   int
	createErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		payments: make(map[uuid.UUID]*domain.Payment),
		byKey:    make(map[string]*domain.Payment),
		claims:   make(map[string]uuid.UUID),
	}
}

func (r *fakeRepo) store(payment *domain.Payment) {
	r.payments[payment.PaymentID] = payment
	r.byKey[payment.IdempotencyKey] = payment
}

func (r *fakeRepo) Create(ctx context.Context, payment *domain.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	r.creates++
	r.store(payment)
	return nil
}

func (r *fakeRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.payments[id], nil
}

func (r *fakeRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byKey[key], nil
}

func (r *fakeRepo) GetByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Payment
	for _, p := range r.payments {
		if p.AccountID == accountID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakeRepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Payment
	for _, p := range r.payments {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakeRepo) Update(ctx context.Context, payment *domain.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store(payment)
	return nil
}

// ClaimIdempotencyKey mirrors INSERT ... IF NOT EXISTS: exactly one caller wins.
func (r *fakeRepo) ClaimIdempotencyKey(ctx context.Context, key string, paymentID uuid.UUID) (uuid.UUID, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.claims[key]; ok {
		return existing, false, nil
	}
	r.claims[key] = paymentID
	return uuid.Nil, true, nil
}

func (r *fakeRepo) ReleaseIdempotencyClaim(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.claims, key)
	return nil
}

func (r *fakeRepo) storedPayments() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.payments)
}

func (r *fakeRepo) createCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creates
}

func (r *fakeRepo) claimCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.claims)
}

// atomicRepo adds AtomicEventWriter: state change and outbox event are written
// in one call, which is what the saga must use when the store supports it.
type atomicRepo struct {
	*fakeRepo
	mu            sync.Mutex
	atomicCreates int
	atomicUpdates int
	events        [][]*outbox.Event
	atomicErr     error
}

func (r *atomicRepo) CreateWithEvents(ctx context.Context, payment *domain.Payment, evs []*outbox.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.atomicErr != nil {
		return r.atomicErr
	}
	r.atomicCreates++
	r.events = append(r.events, evs)
	r.fakeRepo.mu.Lock()
	r.fakeRepo.store(payment)
	r.fakeRepo.mu.Unlock()
	return nil
}

func (r *atomicRepo) UpdateWithEvents(ctx context.Context, payment *domain.Payment, evs []*outbox.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.atomicErr != nil {
		return r.atomicErr
	}
	r.atomicUpdates++
	r.events = append(r.events, evs)
	r.fakeRepo.mu.Lock()
	r.fakeRepo.store(payment)
	r.fakeRepo.mu.Unlock()
	return nil
}

func (r *atomicRepo) recordedEvents() [][]*outbox.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]*outbox.Event, len(r.events))
	copy(out, r.events)
	return out
}

// stubProvider returns a fixed provider outcome, including failures and
// timeouts, so the saga's failure policy can be asserted directly.
type stubProvider struct {
	result *provider.PaymentResult
	err    error
	calls  int
}

func (p *stubProvider) ProcessPayment(ctx context.Context, req *provider.PaymentRequest) (*provider.PaymentResult, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return p.result, nil
}

func successResult() *provider.PaymentResult {
	return &provider.PaymentResult{
		ProviderID:     "stub-provider",
		TransactionRef: "TXN-stub-1",
		Status:         "SUCCESS",
		ResponseCode:   "00",
	}
}

// recordingPublisher captures the events published on the fallback path.
type recordingPublisher struct {
	mu      sync.Mutex
	eventCh []events.EventType
}

func (p *recordingPublisher) PublishPaymentEvent(ctx context.Context, eventType events.EventType, payment interface{}, correlationID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eventCh = append(p.eventCh, eventType)
	return nil
}

func (p *recordingPublisher) Close() error { return nil }

func (p *recordingPublisher) published() []events.EventType {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]events.EventType, len(p.eventCh))
	copy(out, p.eventCh)
	return out
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func newTestService(repo repository.PaymentRepository, prov provider.PaymentProvider, pub events.EventPublisher) *PaymentService {
	return NewPaymentService(repo, prov, pub, zerolog.Nop())
}

func testCreateRequest(key string, amount int64) *domain.CreatePaymentRequest {
	return &domain.CreatePaymentRequest{
		IdempotencyKey:   key,
		AccountID:        uuid.New().String(),
		UserID:           uuid.New().String(),
		PaymentType:      domain.PaymentTypeCard,
		Amount:           amount,
		Currency:         "GBP",
		CounterpartyID:   "merchant-tesco-1",
		CounterpartyName: "Tesco",
		Reference:        "weekly shop",
	}
}

func mustCreate(t *testing.T, svc *PaymentService, req *domain.CreatePaymentRequest) *domain.Payment {
	t.Helper()
	payment, err := svc.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if payment == nil {
		t.Fatal("CreatePayment returned no payment")
	}
	return payment
}

func mustAdvanceToProcessing(t *testing.T, svc *PaymentService, payment *domain.Payment) {
	t.Helper()
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
}

// ── Idempotency (the double-tap problem) ────────────────────────────────────

// A user double-tapping "send" fires two requests with the same idempotency
// key at the same time. Both used to read "no payment yet" and both inserted,
// creating two payments for one user action. The key claim makes exactly one
// caller the creator; every other caller must receive that same payment.
func TestConcurrentCreateWithSameIdempotencyKeyCreatesOnePayment(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &stubProvider{result: successResult()}, &recordingPublisher{})
	req := testCreateRequest("double-tap-key", 1299)

	const callers = 16
	ids := make([]uuid.UUID, callers)
	errs := make([]error, callers)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			payment, err := svc.CreatePayment(context.Background(), req)
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = payment.PaymentID
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed instead of receiving the existing payment: %v", i, err)
		}
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("caller %d created payment %s but caller 0 got %s: one user action must map to one payment", i, id, ids[0])
		}
	}
	if got := repo.createCount(); got != 1 {
		t.Fatalf("repository saw %d creates, want exactly 1", got)
	}
	if got := repo.storedPayments(); got != 1 {
		t.Fatalf("%d payments stored, want exactly 1", got)
	}
}

func TestRepeatedCreateWithSameKeyReturnsSamePayment(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &stubProvider{result: successResult()}, &recordingPublisher{})
	req := testCreateRequest("retry-key", 500)

	first := mustCreate(t, svc, req)
	second := mustCreate(t, svc, req)
	third, err := svc.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("third CreatePayment: %v", err)
	}

	if second.PaymentID != first.PaymentID || third.PaymentID != first.PaymentID {
		t.Fatalf("idempotency key produced multiple payments: %s, %s, %s", first.PaymentID, second.PaymentID, third.PaymentID)
	}
	if got := repo.createCount(); got != 1 {
		t.Fatalf("repository saw %d creates, want 1", got)
	}
}

// ── Transactional outbox (no dual write) ────────────────────────────────────

func TestCreatePersistsStateAndEventInOneAtomicWrite(t *testing.T) {
	repo := &atomicRepo{fakeRepo: newFakeRepo()}
	pub := &recordingPublisher{}
	svc := newTestService(repo, &stubProvider{result: successResult()}, pub)

	mustCreate(t, svc, testCreateRequest("atomic-key", 250))

	if repo.atomicCreates != 1 {
		t.Fatalf("atomic creates = %d, want 1", repo.atomicCreates)
	}
	written := repo.recordedEvents()
	if len(written) != 1 || len(written[0]) != 1 {
		t.Fatalf("want the payment and exactly 1 outbox event written together, got %d batches", len(written))
	}
	if got := written[0][0].EventType; got != string(events.EventTypePaymentCreated) {
		t.Fatalf("outbox event type = %q, want %q", got, events.EventTypePaymentCreated)
	}
	if got := pub.published(); len(got) != 0 {
		t.Fatalf("event was also published directly (%v): that reintroduces the dual write the outbox removes", got)
	}
}

// A failed write must not leave the key claimed, otherwise the client's retry
// with the same key would be rejected forever by its own claim.
func TestFailedCreateReleasesTheIdempotencyClaim(t *testing.T) {
	repo := &atomicRepo{fakeRepo: newFakeRepo()}
	repo.atomicErr = errors.New("cassandra unavailable")
	svc := newTestService(repo, &stubProvider{result: successResult()}, &recordingPublisher{})
	req := testCreateRequest("compensate-key", 900)

	if _, err := svc.CreatePayment(context.Background(), req); err == nil {
		t.Fatal("CreatePayment succeeded even though the write failed")
	}
	if got := repo.claimCount(); got != 0 {
		t.Fatalf("%d claims survived a failed create; the client's retry would be stuck", got)
	}

	repo.atomicErr = nil
	retried := mustCreate(t, svc, req)
	if retried.State != domain.PaymentStateCreated {
		t.Fatalf("state after retry = %s, want CREATED", retried.State)
	}
}

// ── State machine ───────────────────────────────────────────────────────────

func TestHappyPathEmitsEventsInOrderAndSettles(t *testing.T) {
	repo := newFakeRepo()
	pub := &recordingPublisher{}
	svc := newTestService(repo, &stubProvider{result: successResult()}, pub)

	payment := mustCreate(t, svc, testCreateRequest("happy-path", 4200))
	mustAdvanceToProcessing(t, svc, payment)

	if payment.State != domain.PaymentStateSettled {
		t.Fatalf("state = %s, want SETTLED", payment.State)
	}
	if payment.SettledAt == nil {
		t.Fatal("SETTLED payment has no settlement timestamp")
	}

	want := []events.EventType{
		events.EventTypePaymentCreated,
		events.EventTypePaymentAuthorized,
		events.EventTypePaymentProcessing,
		events.EventTypePaymentConfirmed,
		events.EventTypePaymentSettled,
	}
	got := pub.published()
	if len(got) != len(want) {
		t.Fatalf("published %d events (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}

func TestTerminalStatesAcceptNoFurtherTransitions(t *testing.T) {
	terminal := []domain.PaymentState{
		domain.PaymentStateSettled,
		domain.PaymentStateFailed,
		domain.PaymentStateReversed,
		domain.PaymentStateCancelled,
	}
	every := []domain.PaymentState{
		domain.PaymentStateCreated,
		domain.PaymentStateAuthorized,
		domain.PaymentStateProcessing,
		domain.PaymentStateUnknown,
		domain.PaymentStateConfirmed,
		domain.PaymentStateSettled,
		domain.PaymentStateFailed,
		domain.PaymentStateReversed,
		domain.PaymentStateCancelled,
	}

	for _, from := range terminal {
		for _, to := range every {
			if from.CanTransitionTo(to) {
				t.Errorf("%s -> %s must be rejected: %s is terminal", from, to, from)
			}
		}
	}

	illegal := []struct{ from, to domain.PaymentState }{
		{domain.PaymentStateCreated, domain.PaymentStateSettled},
		{domain.PaymentStateCreated, domain.PaymentStateProcessing},
		{domain.PaymentStateAuthorized, domain.PaymentStateConfirmed},
		{domain.PaymentStateProcessing, domain.PaymentStateAuthorized},
		{domain.PaymentStateUnknown, domain.PaymentStateProcessing},
		{domain.PaymentStateConfirmed, domain.PaymentStateProcessing},
		{domain.PaymentStateFailed, domain.PaymentStateAuthorized},
	}
	for _, tc := range illegal {
		if tc.from.CanTransitionTo(tc.to) {
			t.Errorf("%s -> %s must be rejected", tc.from, tc.to)
		}
	}
}

func TestUnknownOutcomeIsNeverMarkedFailed(t *testing.T) {
	// A provider timeout means the money may or may not have moved. Declaring
	// FAILED here would tell the customer nothing moved when it might have, so
	// the payment must land in UNKNOWN and be reconciled instead.
	repo := newFakeRepo()
	pub := &recordingPublisher{}
	prov := &stubProvider{result: successResult()}
	svc := newTestService(repo, prov, pub)

	payment := mustCreate(t, svc, testCreateRequest("timeout-key", 750))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}

	prov.err = context.DeadlineExceeded
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}

	if payment.State != domain.PaymentStateUnknown {
		t.Fatalf("state = %s, want UNKNOWN for an indeterminate provider outcome", payment.State)
	}
	for _, e := range pub.published() {
		if e == events.EventTypePaymentFailed || e == events.EventTypePaymentSettled {
			t.Fatalf("published %q for an unknown outcome", e)
		}
	}
}

func TestProviderHardFailureFailsPaymentAndNeverSettles(t *testing.T) {
	repo := newFakeRepo()
	pub := &recordingPublisher{}
	prov := &stubProvider{result: successResult()}
	svc := newTestService(repo, prov, pub)

	payment := mustCreate(t, svc, testCreateRequest("hard-fail-key", 310))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}

	prov.err = errors.New("503 service unavailable")
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}

	if payment.State != domain.PaymentStateFailed {
		t.Fatalf("state = %s, want FAILED", payment.State)
	}
	if payment.FailureReason == "" {
		t.Fatal("failed payment has no failure reason for the customer")
	}
	for _, e := range pub.published() {
		if e == events.EventTypePaymentSettled {
			t.Fatal("a failed provider call settled the payment")
		}
	}
}

func TestCancelIsOnlyAllowedBeforeMoneyIsInFlight(t *testing.T) {
	repo := newFakeRepo()
	prov := &stubProvider{result: successResult()}
	svc := newTestService(repo, prov, &recordingPublisher{})

	created := mustCreate(t, svc, testCreateRequest("cancel-created", 100))
	cancelled, err := svc.CancelPayment(context.Background(), created.PaymentID)
	if err != nil {
		t.Fatalf("cancelling a CREATED payment: %v", err)
	}
	if cancelled.State != domain.PaymentStateCancelled {
		t.Fatalf("state = %s, want CANCELLED", cancelled.State)
	}

	authorized := mustCreate(t, svc, testCreateRequest("cancel-authorized", 100))
	if _, err := svc.AuthorizePayment(context.Background(), authorized.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	if _, err := svc.CancelPayment(context.Background(), authorized.PaymentID); err != nil {
		t.Fatalf("cancelling an AUTHORIZED payment: %v", err)
	}

	inFlight := mustCreate(t, svc, testCreateRequest("cancel-processing", 100))
	if _, err := svc.AuthorizePayment(context.Background(), inFlight.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	// The provider's outcome is unknown (timeout), so the payment is no longer
	// safely cancellable.
	prov.err = context.DeadlineExceeded
	if _, err := svc.ProcessPayment(context.Background(), inFlight.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
	if inFlight.State != domain.PaymentStateUnknown {
		t.Fatalf("setup failed: state = %s, want UNKNOWN", inFlight.State)
	}
	if _, err := svc.CancelPayment(context.Background(), inFlight.PaymentID); err == nil {
		t.Fatal("cancelling a payment with an indeterminate outcome must be rejected")
	}
}

func TestDuplicateProviderCallbackCannotDoubleBook(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &stubProvider{result: successResult()}, &recordingPublisher{})

	payment := mustCreate(t, svc, testCreateRequest("callback-key", 1234))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
	if payment.State != domain.PaymentStateSettled {
		t.Fatalf("setup failed: state = %s, want SETTLED", payment.State)
	}

	// The provider retries its callback after the payment already settled.
	_, err := svc.HandleProviderCallback(context.Background(), &domain.ProviderCallbackRequest{
		PaymentID:      payment.PaymentID.String(),
		ProviderID:     "stub-provider",
		TransactionRef: "TXN-stub-1",
		Status:         "SUCCESS",
	})
	if err == nil {
		t.Fatal("a duplicate callback on a settled payment must be rejected")
	}
	if payment.State != domain.PaymentStateSettled {
		t.Fatalf("state after duplicate callback = %s, want it unchanged at SETTLED", payment.State)
	}
}
