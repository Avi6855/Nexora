package service

import (
	"context"
	"testing"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/events"
)

// These tests cover the second half of the UNKNOWN story. The first half — an
// indeterminate provider answer leaves the payment UNKNOWN and the hold in
// place — is asserted in hold_test.go. What is asserted here is that the
// payment does not stay there forever: a late provider callback and, failing
// that, a reconciliation decision must both be able to conclude it, and must
// give the customer's hold back exactly once.

// mustReachUnknown drives a payment into UNKNOWN the way a provider timeout
// does: authorise (hold taken), then process against an unreachable provider.
func mustReachUnknown(t *testing.T, svc *PaymentService, repo *fakeRepo, key string, amount int64) *domain.Payment {
	t.Helper()

	payment := mustCreate(t, svc, testCreateRequest(key, amount))
	if _, err := svc.AuthorizePayment(context.Background(), payment.PaymentID); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	if _, err := svc.ProcessPayment(context.Background(), payment.PaymentID, &domain.ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}

	stored, err := repo.GetByID(context.Background(), payment.PaymentID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.State != domain.PaymentStateUnknown {
		t.Fatalf("setup failed: state = %s, want UNKNOWN", stored.State)
	}
	return payment
}

func newUnknownServiceFixture(t *testing.T, key string, amount int64) (*PaymentService, *fakeRepo, *fakeHolder, *recordingPublisher, *domain.Payment) {
	t.Helper()

	repo := newFakeRepo()
	holder := newFakeHolder()
	pub := &recordingPublisher{}
	svc := newServiceWithHolder(repo, holder, &stubProvider{err: context.DeadlineExceeded}, pub)
	payment := mustReachUnknown(t, svc, repo, key, amount)

	if holder.holdCount() != 1 {
		t.Fatalf("expected the hold to be taken once, got %d holds", holder.holdCount())
	}
	if holder.releaseCount() != 0 {
		t.Fatalf("an UNKNOWN payment must not release its hold, got %d releases", holder.releaseCount())
	}

	return svc, repo, holder, pub, payment
}

// A provider that timed out and then answers SUCCESS is telling us the money
// moved. That answer used to be rejected ("must be PROCESSING"), which left the
// payment UNKNOWN and the customer's money held with the provider having
// actually taken it.
func TestLateProviderSuccessResolvesUnknownPayment(t *testing.T) {
	svc, repo, holder, pub, payment := newUnknownServiceFixture(t, "late-success-key", 4_200)

	if _, err := svc.HandleProviderCallback(context.Background(), &domain.ProviderCallbackRequest{
		PaymentID:      payment.PaymentID.String(),
		ProviderID:     "stub-provider",
		TransactionRef: "TXN-late-success",
		Status:         "SUCCESS",
		ResponseCode:   "00",
	}); err != nil {
		t.Fatalf("late SUCCESS callback was rejected: %v", err)
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateSettled {
		t.Fatalf("state = %s, want SETTLED after a late SUCCESS", stored.State)
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("settling a resolved UNKNOWN payment must release the hold exactly once, got %d", holder.releaseCount())
	}
	published := pub.published()
	for _, want := range []events.EventType{events.EventTypePaymentConfirmed, events.EventTypePaymentSettled, events.EventTypePaymentReservationReleased} {
		if !containsEvent(published, want) {
			t.Errorf("late settlement is missing %s; events = %v", want, published)
		}
	}
}

// The other half of the same answer: the provider says the payment failed, so
// the hold comes back to the customer instead of expiring on its own.
func TestLateProviderFailureReleasesTheHoldOnce(t *testing.T) {
	svc, repo, holder, pub, payment := newUnknownServiceFixture(t, "late-failure-key", 3_100)

	if _, err := svc.HandleProviderCallback(context.Background(), &domain.ProviderCallbackRequest{
		PaymentID:  payment.PaymentID.String(),
		ProviderID: "stub-provider",
		Status:     "FAILED",
		Message:    "declined at the rail",
	}); err != nil {
		t.Fatalf("late FAILED callback was rejected: %v", err)
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateFailed {
		t.Fatalf("state = %s, want FAILED after a late FAILED", stored.State)
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("hold releases = %d, want exactly 1", holder.releaseCount())
	}
	if !containsEvent(pub.published(), events.EventTypePaymentReservationReleased) {
		t.Errorf("hold release was not published; events = %v", pub.published())
	}
}

// Reconciliation deciding the money moved concludes the payment the same way a
// late callback would.
func TestReconciliationCanConcludeAnUnknownPayment(t *testing.T) {
	svc, repo, holder, _, payment := newUnknownServiceFixture(t, "recon-confirm-key", 8_400)

	resolved, err := svc.ResolveUnknownPayment(context.Background(), payment.PaymentID, domain.ResolutionOutcomeConfirmed, "provider statement matched")
	if err != nil {
		t.Fatalf("ResolveUnknownPayment: %v", err)
	}
	if resolved.State != domain.PaymentStateSettled {
		t.Fatalf("state = %s, want SETTLED", resolved.State)
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("hold releases = %d, want exactly 1", holder.releaseCount())
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateSettled {
		t.Fatalf("stored state = %s, want SETTLED", stored.State)
	}
}

// A reconciliation sweep retries, and a retry that arrives after the payment
// was already concluded must not conclude it twice. Double-concluding would
// release the same hold twice — money out of the bank for a payment that never
// moved a second time.
func TestReconciliationResolutionIsIdempotent(t *testing.T) {
	svc, repo, holder, _, payment := newUnknownServiceFixture(t, "recon-idempotent-key", 2_600)

	if _, err := svc.ResolveUnknownPayment(context.Background(), payment.PaymentID, domain.ResolutionOutcomeFailed, "rail rejected the payment"); err != nil {
		t.Fatalf("first resolution: %v", err)
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("hold releases after the first resolution = %d, want 1", holder.releaseCount())
	}

	// The sweep runs again, or a late callback duplicates the decision.
	for i := 0; i < 2; i++ {
		again, err := svc.ResolveUnknownPayment(context.Background(), payment.PaymentID, domain.ResolutionOutcomeFailed, "rail rejected the payment")
		if err != nil {
			t.Fatalf("repeat resolution %d: %v", i, err)
		}
		if again.State != domain.PaymentStateFailed {
			t.Fatalf("repeat resolution %d changed the state to %s, want FAILED", i, again.State)
		}
	}
	if holder.releaseCount() != 1 {
		t.Fatalf("hold releases = %d after repeated resolutions, want exactly 1", holder.releaseCount())
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateFailed {
		t.Fatalf("stored state = %s, want FAILED", stored.State)
	}
}

// Reconciliation must never rewrite a payment that had a definite outcome. A
// payment that already settled is not "unknown, resolved as failed" because
// somebody re-ran a sweep with stale state.
func TestResolutionLeavesConcludedPaymentsAlone(t *testing.T) {
	repo := newFakeRepo()
	holder := newFakeHolder()
	svc := newServiceWithHolder(repo, holder, &stubProvider{result: successResult()}, &recordingPublisher{})

	payment := mustCreate(t, svc, testCreateRequest("already-settled-key", 1_750))
	mustAdvanceToProcessing(t, svc, payment)

	settled, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if settled.State != domain.PaymentStateSettled {
		t.Fatalf("setup failed: state = %s, want SETTLED", settled.State)
	}
	releasesBefore := holder.releaseCount()

	resolved, err := svc.ResolveUnknownPayment(context.Background(), payment.PaymentID, domain.ResolutionOutcomeFailed, "stale sweep")
	if err != nil {
		t.Fatalf("resolving a settled payment should be a no-op, got error: %v", err)
	}
	if resolved.State != domain.PaymentStateSettled {
		t.Fatalf("state = %s, want it left at SETTLED", resolved.State)
	}
	if holder.releaseCount() != releasesBefore {
		t.Fatalf("a no-op resolution released the hold again: %d releases, want %d", holder.releaseCount(), releasesBefore)
	}
}

// An outcome nobody defined is a bug in the caller, not permission to guess.
func TestResolutionRejectsAnUnknownOutcome(t *testing.T) {
	svc, repo, holder, _, payment := newUnknownServiceFixture(t, "bad-outcome-key", 990)

	if _, err := svc.ResolveUnknownPayment(context.Background(), payment.PaymentID, domain.ResolutionOutcome("MAYBE"), ""); err == nil {
		t.Fatal("an undefined resolution outcome was accepted")
	}

	stored, _ := repo.GetByID(context.Background(), payment.PaymentID)
	if stored.State != domain.PaymentStateUnknown {
		t.Errorf("state = %s, want it left at UNKNOWN after a rejected outcome", stored.State)
	}
	if holder.releaseCount() != 0 {
		t.Errorf("a rejected resolution released the hold %d times, want 0", holder.releaseCount())
	}
}
