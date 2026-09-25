package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/events"
)

// systemTotals sums every entry in the ledger, the way the financial invariant
// checker does: across the whole ledger, debits must equal credits.
func systemTotals(t *testing.T, repo *mockRepository) (int64, int64) {
	t.Helper()
	repo.mu.RLock()
	defer repo.mu.RUnlock()
	var debits, credits int64
	for _, e := range repo.entries {
		switch e.EntryType {
		case domain.EntryTypeDebit:
			debits += e.Amount
		case domain.EntryTypeCredit:
			credits += e.Amount
		}
	}
	return debits, credits
}

// TestSettlementIsBalancedDoubleEntry is the regression test for the single-leg
// settlement: settling a hold used to write only the customer debit, so total
// debits exceeded total credits for the whole ledger (every card capture broke
// the books-balance invariant) and the money that left the customer was never
// recorded on the other side.
func TestSettlementIsBalancedDoubleEntry(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()

	customer := uuid.New()
	fundAccount(t, svc, customer, 50_000)

	reservation, err := svc.ReserveFunds(ctx, customer, 12_000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserving funds: %v", err)
	}

	entry, err := svc.SettleReservation(ctx, reservation.ReservationID)
	if err != nil {
		t.Fatalf("settling reservation: %v", err)
	}
	if entry == nil {
		t.Fatal("settlement returned no entry")
	}

	if got, want := balanceOf(t, svc, customer), int64(38_000); got != want {
		t.Errorf("customer balance after settlement = %d, want %d", got, want)
	}

	// The money that left the customer must be recorded on the other side.
	clearingEntries := entriesFor(t, repo, domain.ClearingAccountID)
	counterLeg := clearingEntries[len(clearingEntries)-1]
	if counterLeg.EntryType != domain.EntryTypeCredit {
		t.Errorf("counter-leg must be a CREDIT, got %s", counterLeg.EntryType)
	}
	if counterLeg.EntryDirection != domain.EntryDirectionInbound {
		t.Errorf("counter-leg direction = %s, want INBOUND", counterLeg.EntryDirection)
	}
	if counterLeg.Amount != 12_000 {
		t.Errorf("counter-leg amount = %d, want the settled 12000", counterLeg.Amount)
	}
	if counterLeg.TransactionID != reservation.TransactionID {
		t.Errorf("counter-leg is not grouped with the reservation's transaction: %s vs %s", counterLeg.TransactionID, reservation.TransactionID)
	}

	debits, credits := systemTotals(t, repo)
	if debits != credits {
		t.Errorf("ledger is unbalanced: total debits %d != total credits %d", debits, credits)
	}
}

// A settlement must debit the customer exactly once, even when the reservation
// is settled alongside other entries.
func TestSettlementDebitsCustomerExactlyOnce(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()

	customer := uuid.New()
	fundAccount(t, svc, customer, 20_000)

	reservation, err := svc.ReserveFunds(ctx, customer, 5_000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserving funds: %v", err)
	}
	if _, err := svc.SettleReservation(ctx, reservation.ReservationID); err != nil {
		t.Fatalf("settling reservation: %v", err)
	}

	// Settling twice must be refused rather than debiting again.
	if _, err := svc.SettleReservation(ctx, reservation.ReservationID); err == nil {
		t.Error("settling an already settled reservation must be refused")
	}

	debits := 0
	for _, e := range entriesFor(t, repo, customer) {
		if e.EntryType == domain.EntryTypeDebit {
			debits++
		}
	}
	if debits != 1 {
		t.Errorf("customer was debited %d times for one settlement, want 1", debits)
	}
	if got := balanceOf(t, svc, customer); got != 15_000 {
		t.Errorf("customer balance = %d, want 15000", got)
	}
}

// Releasing a hold moves no money, so it must not write any entry. If it did,
// the customer would be charged for a payment that never left.
func TestReleasingAHoldWritesNoEntries(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()

	customer := uuid.New()
	fundAccount(t, svc, customer, 9_000)
	beforeDebits, beforeCredits := systemTotals(t, repo)

	reservation, err := svc.ReserveFunds(ctx, customer, 4_000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserving funds: %v", err)
	}
	if err := svc.ReleaseReservation(ctx, reservation.ReservationID); err != nil {
		t.Fatalf("releasing reservation: %v", err)
	}

	debits, credits := systemTotals(t, repo)
	if debits != beforeDebits || credits != beforeCredits {
		t.Errorf("releasing a hold changed the books: debits %d->%d, credits %d->%d", beforeDebits, debits, beforeCredits, credits)
	}
	if got := balanceOf(t, svc, customer); got != 9_000 {
		t.Errorf("released hold must leave the balance intact, got %d want 9000", got)
	}
}

// A refused payment booking used to be logged and dropped. It is now filed
// against the account so an operator can find the payment whose settlement the
// ledger never recorded.
func TestRefusedBookingIsRecordedForOperators(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 1_000)

	paymentID := uuid.New()
	payload := settledPayload(paymentID, payer, 90_000, uuid.New().String())
	if err := processor.HandlePaymentEvent(context.Background(), payload, "payment.settled"); err == nil {
		t.Fatal("expected the booking to be refused")
	}

	stored, err := repo.ListIntegrityEvents(context.Background(), payer, 10)
	if err != nil {
		t.Fatalf("listing integrity events: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("refused booking was not recorded anywhere an operator can see it")
	}

	found := false
	for _, ev := range stored {
		if ev.EventType == domain.IntegrityEventBookingRefused {
			found = true
			if ev.AccountID != payer {
				t.Errorf("event filed against %s, want the payer %s", ev.AccountID, payer)
			}
			if ev.Detail == "" {
				t.Error("recorded refusal has no detail explaining which payment and why")
			}
		}
	}
	if !found {
		t.Errorf("no BOOKING_REFUSED event recorded; got %d events of other kinds", len(stored))
	}

	// Recording the refusal must not stop the claim from being handed back.
	repo.mu.RLock()
	stillClaimed := repo.bookedPayments[paymentID]
	repo.mu.RUnlock()
	if stillClaimed {
		t.Error("a refused booking must still release its claim so it can be retried")
	}
}

// The incident reporter is optional; without it the durable integrity event is
// still written, so the refusal is never lost.
func TestRefusedBookingRecordedWithoutIncidentReporter(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	account := uuid.New()

	if err := svc.RecordPaymentBookingFailure(ctx, uuid.New(), account, "insufficient funds"); err != nil {
		t.Fatalf("recording booking failure: %v", err)
	}

	eventsOut, err := repo.ListIntegrityEvents(ctx, account, 5)
	if err != nil {
		t.Fatalf("listing integrity events: %v", err)
	}
	if len(eventsOut) != 1 || eventsOut[0].EventType != domain.IntegrityEventBookingRefused {
		t.Fatalf("expected one BOOKING_REFUSED event, got %+v", eventsOut)
	}
}

// A refusal must not be confused with corruption: the monitor's VIOLATION path
// freezes accounts, and a refused booking is not a reason to freeze one.
func TestRefusedBookingDoesNotFreezeTheAccount(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 500)
	_ = processor.HandlePaymentEvent(context.Background(), settledPayload(uuid.New(), payer, 90_000, uuid.New().String()), "payment.settled")

	guard, err := repo.GetIntegrityGuard(context.Background(), payer)
	if err != nil {
		t.Fatalf("reading integrity guard: %v", err)
	}
	if guard != nil && guard.Frozen {
		t.Error("a refused booking froze the account; refusals are expected behaviour, not corruption")
	}

	// The account must still be usable.
	if _, err := svc.ReserveFunds(context.Background(), payer, 100, uuid.New(), "GBP", time.Minute); err != nil {
		t.Errorf("account unusable after a refused booking: %v", err)
	}
}
