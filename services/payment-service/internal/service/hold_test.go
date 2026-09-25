package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/payment-service/internal/clients"
	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
)

// ── Test doubles ────────────────────────────────────────────────────────────

type holdRequest struct {
	accountID uuid.UUID
	amount    int64
	currency  string
	txID      uuid.UUID
	ttl       string
}

// fakeHolder stands in for the ledger of record: it records every hold taken
// and every hold given back, and can be told to refuse or to be unreachable.
type fakeHolder struct {
	mu         sync.Mutex
	reserves   []holdRequest
	releases   []uuid.UUID
	reserveErr error
	releaseErr error
	nextID     uuid.UUID
}

func newFakeHolder() *fakeHolder {
	return &fakeHolder{nextID: uuid.New()}
}

func (h *fakeHolder) Reserve(ctx context.Context, accountID uuid.UUID, amount int64, currency string, txID uuid.UUID, ttl string) (*clients.LedgerReservation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.reserveErr != nil {
		return nil, h.reserveErr
	}
	h.reserves = append(h.reserves, holdRequest{accountID: accountID, amount: amount, currency: currency, txID: txID, ttl: ttl})
	return &clients.LedgerReservation{
		ReservationID: h.nextID,
		Status:        "ACTIVE",
		Amount:        amount,
		Currency:      currency,
	}, nil
}

func (h *fakeHolder) ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.releaseErr != nil {
		return h.releaseErr
	}
	h.releases = append(h.releases, reservationID)
	return nil
}

func (h *fakeHolder) holdCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.reserves)
}

func (h *fakeHolder) releaseCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.releases)
}

func (h *fakeHolder) lastHold() holdRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reserves[len(h.reserves)-1]
}

// failingUpdateRepo fails the state write the way an unavailable Cassandra
// does, so the saga's compensation path can be exercised.
type failingUpdateRepo struct {
	*fakeRepo
}

func (r *failingUpdateRepo) Update(ctx context.Context, payment *domain.Payment) error {
	return errors.New("cassandra unavailable")
}

func newServiceWithHolder(repo *fakeRepo, holder *fakeHolder, prov *stubProvider, pub *recordingPublisher) *PaymentService {
	svc := newTestService(repo, prov, pub)
	svc.SetFundHolder(holder)
	return svc
}

// failedResult is a provider outcome that declines the payment outright.
func failedResult() *provider.PaymentResult {
	return &provider.PaymentResult{
		ProviderID:   "stub-provider",
		Status:       "FAILED",
		Message:      "declined by provider",
		ResponseCode: "05",
	}
}

func containsEvent(types []events.EventType, want events.EventType) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

// ── Authorisation hold ──────────────────────────────────────────────────────

// Authorising a payment must hold the money, otherwise the balance stays
// spendable while the payment is in flight and the same money can be committed
// twice.
func TestAuthorizeHoldsFundsAtTheLedger(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, &recordingPublisher{})

	payment := mustCreate(t, svc, testCreateRequest("hold-key", 12_500))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}

	if holder.holdCount() != 1 {
		t.Fatalf("ledger saw %d holds, want exactly 1", holder.holdCount())
	}
	hold := holder.lastHold()
	if hold.accountID != payment.AccountID {
		t.Errorf("held against account %s, want the payment's account %s", hold.accountID, payment.AccountID)
	}
	if hold.amount != payment.Amount {
		t.Errorf("held %d, want the payment amount %d", hold.amount, payment.Amount)
	}
	if hold.currency != payment.Currency {
		t.Errorf("held currency %s, want %s", hold.currency, payment.Currency)
	}
	if hold.txID != payment.PaymentID {
		t.Errorf("hold tied to %s, want the payment id %s", hold.txID, payment.PaymentID)
	}
	if hold.ttl == "" {
		t.Error("hold has no TTL: a hold that is never released would strand the money forever")
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.ReservationID == "" {
		t.Error("authorised payment did not record its hold id")
	}
}

// A payment that cannot be funded must not be authorised: the balance would
// otherwise be committed to a payment that can never settle.
func TestAuthorizeRefusedWhenFundsAreNotAvailable(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	holder.reserveErr = clients.ErrInsufficientFunds
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, &recordingPublisher{})

	payment := mustCreate(t, svc, testCreateRequest("broke-key", 999_999))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err == nil {
		t.Fatal("payment was authorised without available funds")
	} else if !clients.IsInsufficientFunds(err) {
		t.Errorf("error should be a refusal, got %v", err)
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateCreated {
		t.Errorf("state = %s, want CREATED after a refused authorisation", stored.State)
	}
	if stored.ReservationID != "" {
		t.Error("refused authorisation must not leave a hold recorded")
	}
}

// A ledger outage must fail closed: without a hold we cannot prove the money
// exists, so the payment is refused rather than authorised optimistically.
func TestAuthorizeFailsClosedWhenTheLedgerIsUnreachable(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	holder.reserveErr = errors.New("ledger service unreachable")
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, &recordingPublisher{})

	payment := mustCreate(t, svc, testCreateRequest("ledger-down-key", 4_000))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err == nil {
		t.Fatal("payment was authorised while the ledger was unreachable")
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateCreated {
		t.Errorf("state = %s, want CREATED when no hold could be taken", stored.State)
	}
}

// The hold must be taken once per payment, not once per attempt.
func TestRepeatedAuthorizeTakesNoSecondHold(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, &recordingPublisher{})

	payment := mustCreate(t, svc, testCreateRequest("double-auth-key", 1_500))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("first authorize: %v", err)
	}
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err == nil {
		t.Fatal("second authorisation of an AUTHORIZED payment should be rejected")
	}

	if holder.holdCount() != 1 {
		t.Errorf("ledger saw %d holds for one payment, want 1", holder.holdCount())
	}
}

// A failed authorisation write must hand the hold straight back, not leave the
// customer's money held until the TTL expires.
func TestHoldReleasedWhenAuthorizationCannotBePersisted(t *testing.T) {
	repo := &failingUpdateRepo{fakeRepo: newFakeRepo()}
	holder := newFakeHolder()
	pub := &recordingPublisher{}
	svc := newTestService(repo, &stubProvider{result: successResult()}, pub)
	svc.SetFundHolder(holder)

	// Create through the passing path by writing the payment directly, then
	// drive the authorisation into the failing write.
	payment := domain.NewPayment("unwritable-key", uuid.New(), uuid.New(), domain.PaymentTypeCard, 3_000, "GBP", "merchant", "Merchant", "ref")
	repo.fakeRepo.store(payment)

	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err == nil {
		t.Fatal("authorisation succeeded even though the state write failed")
	}

	if holder.holdCount() != 1 {
		t.Fatalf("expected a hold to have been taken, got %d", holder.holdCount())
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("hold was not given back after the failed write: %d releases", holder.releaseCount())
	}
	if containsEvent(pub.published(), events.EventTypePaymentReversed) {
		t.Error("releasing a hold must not be published as payment.reversed")
	}
}

// ── Release on terminal outcomes ────────────────────────────────────────────

func TestFailedPaymentReleasesItsHold(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	pub := &recordingPublisher{}
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: failedResult()}, pub)

	payment := mustCreate(t, svc, testCreateRequest("fail-key", 7_000))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("process: %v", err)
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateFailed {
		t.Fatalf("state = %s, want FAILED", stored.State)
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("a failed payment must release its hold exactly once, got %d releases", holder.releaseCount())
	}
	if !containsEvent(pub.published(), events.EventTypePaymentReservationReleased) {
		t.Errorf("hold release was not published; events = %v", pub.published())
	}
}

func TestCancelledPaymentReleasesItsHold(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	pub := &recordingPublisher{}
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, pub)

	payment := mustCreate(t, svc, testCreateRequest("cancel-key", 2_200))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err := svc.CancelPayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if holder.releaseCount() != 1 {
		t.Fatalf("cancelling must release the hold exactly once, got %d", holder.releaseCount())
	}
	released := pub.published()
	if !containsEvent(released, events.EventTypePaymentReservationReleased) {
		t.Errorf("cancellation did not publish the hold release; events = %v", released)
	}
	if containsEvent(released, events.EventTypePaymentReversed) {
		t.Errorf("a cancelled payment released its hold as payment.reversed, which reads as money returned to the customer: %v", released)
	}
}

// Settling books the real debit from the settlement event, so keeping the hold
// would double-charge the customer's available balance forever.
func TestSettledPaymentReleasesItsHoldExactlyOnce(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	pub := &recordingPublisher{}
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, pub)

	payment := mustCreate(t, svc, testCreateRequest("settle-key", 5_500))
	mustAdvanceToProcessing(t, svc, payment)

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateSettled {
		t.Fatalf("state = %s, want SETTLED", stored.State)
	}
	if holder.holdCount() != 1 {
		t.Fatalf("expected exactly 1 hold, got %d", holder.holdCount())
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("settled payment released its hold %d times, want exactly 1", holder.releaseCount())
	}

	published := pub.published()
	for _, want := range []events.EventType{events.EventTypePaymentConfirmed, events.EventTypePaymentSettled} {
		if !containsEvent(published, want) {
			t.Errorf("settlement path is missing %s; events = %v", want, published)
		}
	}
	if !containsEvent(published, events.EventTypePaymentReservationReleased) {
		t.Errorf("settled payment did not publish its hold release; events = %v", published)
	}
}

// An unknown provider outcome means the money may still move. The hold stays
// in place so an unaccounted payment cannot be spent a second time.
func TestUnknownOutcomeKeepsTheHold(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	pub := &recordingPublisher{}
	svc := newServiceWithHolder(repo, holder, &stubProvider{err: context.DeadlineExceeded}, pub)

	payment := mustCreate(t, svc, testCreateRequest("timeout-key", 1_100))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("process: %v", err)
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateUnknown {
		t.Fatalf("state = %s, want UNKNOWN", stored.State)
	}
	if holder.releaseCount() != 0 {
		t.Errorf("an unknown outcome must NOT release the hold (money may have moved), got %d releases", holder.releaseCount())
	}
}

// Without a ledger configured the saga must still work: the hold is simply not
// taken, which is the behaviour every earlier test in this package relies on.
func TestNoHolderMeansNoHoldAndUnaffectedFlow(t *testing.T) {
	repo := newFakeRepo()
	pub := &recordingPublisher{}
	svc := newTestService(repo, &stubProvider{result: successResult()}, pub)

	payment := mustCreate(t, svc, testCreateRequest("no-holder-key", 800))
	mustAdvanceToProcessing(t, svc, payment)

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateSettled {
		t.Fatalf("state = %s, want SETTLED", stored.State)
	}
	if containsEvent(pub.published(), events.EventTypePaymentReservationReleased) {
		t.Errorf("no hold was taken, so none should be released: %v", pub.published())
	}
}
