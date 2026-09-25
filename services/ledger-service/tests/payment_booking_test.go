package tests

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/events"
	"github.com/nexora/nexora/services/ledger-service/internal/service"
)

// clearingAccountID is the suspense account the ledger credits when money
// leaves a customer account on the payment rail.
var clearingAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// fundAccount puts real money in an account by booking a balanced credit, so
// availability checks run against entries rather than a counter.
func fundAccount(t *testing.T, svc *service.LedgerService, accountID uuid.UUID, amount int64) {
	t.Helper()
	req := &domain.CreateDoubleEntryRequest{
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
		Amount:          amount,
		Currency:        "GBP",
		Description:     "test funding",
		DebitAccountID:  clearingAccountID,
		CreditAccountID: accountID,
		Lines: []domain.DoubleEntryLine{
			{AccountID: clearingAccountID, EntryType: domain.EntryTypeDebit, Amount: amount, Currency: "GBP"},
			{AccountID: accountID, EntryType: domain.EntryTypeCredit, Amount: amount, Currency: "GBP"},
		},
	}
	if _, _, err := svc.CreateDoubleEntryTransaction(context.Background(), req); err != nil {
		t.Fatalf("funding account: %v", err)
	}
}

func settledPayload(paymentID uuid.UUID, accountID uuid.UUID, amount int64, key string) *events.PaymentEventPayload {
	return &events.PaymentEventPayload{
		PaymentID:      paymentID.String(),
		IdempotencyKey: key,
		AccountID:      accountID.String(),
		UserID:         uuid.New().String(),
		Amount:         amount,
		Currency:       "GBP",
		State:          "SETTLED",
		Reference:      "test payment",
	}
}

func balanceOf(t *testing.T, svc *service.LedgerService, accountID uuid.UUID) int64 {
	t.Helper()
	balance, err := svc.GetAvailableBalance(context.Background(), accountID)
	if err != nil {
		t.Fatalf("reading balance: %v", err)
	}
	return balance
}

func entriesFor(t *testing.T, repo *mockRepository, accountID uuid.UUID) []*domain.LedgerEntry {
	t.Helper()
	repo.mu.RLock()
	defer repo.mu.RUnlock()
	return append([]*domain.LedgerEntry(nil), repo.accountEntries[accountID]...)
}

// TestPaymentBookingDebitsPayerNotCredit is the regression test for the sign
// bug: an outbound payment must take money OUT of the paying account. Booking
// the payer as the CREDIT leg paid them instead — the balance went UP by the
// payment amount and the books did not reflect money leaving the customer.
func TestPaymentBookingDebitsPayerNotCredit(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 100_000)
	before := balanceOf(t, svc, payer)
	if before != 100_000 {
		t.Fatalf("expected funded balance 100000, got %d", before)
	}

	paymentID := uuid.New()
	payload := settledPayload(paymentID, payer, 25_000, uuid.New().String())
	if err := processor.HandlePaymentEvent(context.Background(), payload, "payment.settled"); err != nil {
		t.Fatalf("handling payment event: %v", err)
	}

	after := balanceOf(t, svc, payer)
	if after != before-25_000 {
		t.Errorf("outbound payment must lower the payer's balance: %d -> %d, expected %d", before, after, before-25_000)
	}

	payerEntries := entriesFor(t, repo, payer)
	last := payerEntries[len(payerEntries)-1]
	if last.EntryType != domain.EntryTypeDebit {
		t.Errorf("payer entry must be a DEBIT, got %s", last.EntryType)
	}
	if last.EntryDirection != domain.EntryDirectionOutbound {
		t.Errorf("payer entry direction must be OUTBOUND, got %s", last.EntryDirection)
	}

	// Money out of the customer must show up as a credit on the counterparty
	// side, so the transaction still balances.
	clearingEntries := entriesFor(t, repo, clearingAccountID)
	if len(clearingEntries) == 0 {
		t.Fatal("expected a counterparty leg for the payment")
	}
	lastClearing := clearingEntries[len(clearingEntries)-1]
	if lastClearing.EntryType != domain.EntryTypeCredit {
		t.Errorf("counterparty leg must be a CREDIT, got %s", lastClearing.EntryType)
	}
	if lastClearing.Amount != 25_000 {
		t.Errorf("counterparty leg amount = %d, want 25000", lastClearing.Amount)
	}
}

// TestPaymentBookingRefusesToOverdraw checks the ledger of record refuses to
// write a payment debit the account cannot cover, so a payment can never push
// the balance negative even if upstream checks were skipped or raced.
func TestPaymentBookingRefusesToOverdraw(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 10_000)

	paymentID := uuid.New()
	payload := settledPayload(paymentID, payer, 25_000, uuid.New().String())
	err := processor.HandlePaymentEvent(context.Background(), payload, "payment.settled")
	if err == nil {
		t.Fatal("expected an insufficient-funds refusal, got nil")
	}
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Errorf("expected ErrInsufficientFunds, got %v", err)
	}

	if got := balanceOf(t, svc, payer); got != 10_000 {
		t.Errorf("refused booking must not change the balance: got %d, want 10000", got)
	}

	// No debit may have been written on the payment's behalf.
	for _, e := range entriesFor(t, repo, payer) {
		if e.EntryType == domain.EntryTypeDebit && e.Amount == 25_000 {
			t.Error("a refused payment must not write a debit entry")
		}
	}
}

// TestRefusedPaymentBookingHandsBackItsClaim proves the claim is compensated:
// without the release the payment would stay marked as booked with no money
// moved, and every later delivery would be skipped as a duplicate.
func TestRefusedPaymentBookingHandsBackItsClaim(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 10_000)

	paymentID := uuid.New()
	payload := settledPayload(paymentID, payer, 25_000, uuid.New().String())
	if err := processor.HandlePaymentEvent(context.Background(), payload, "payment.settled"); err == nil {
		t.Fatal("expected the first booking attempt to be refused")
	}

	repo.mu.RLock()
	stillClaimed := repo.bookedPayments[paymentID]
	repo.mu.RUnlock()
	if stillClaimed {
		t.Fatal("a failed booking must release its claim so the payment can be retried")
	}

	// Top the account up and redeliver: the retry must now book.
	fundAccount(t, svc, payer, 50_000)
	if err := processor.HandlePaymentEvent(context.Background(), payload, "payment.settled"); err != nil {
		t.Fatalf("retry after funding should book: %v", err)
	}

	if got, want := balanceOf(t, svc, payer), int64(35_000); got != want {
		t.Errorf("balance after successful retry = %d, want %d", got, want)
	}
}

// TestDuplicatePaymentLifecycleEventsBookOnce covers the confirmed + settled
// pair the payment service publishes for one payment: the customer must be
// debited exactly once.
func TestDuplicatePaymentLifecycleEventsBookOnce(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 100_000)

	paymentID := uuid.New()
	key := uuid.New().String()
	payload := settledPayload(paymentID, payer, 40_000, key)

	for _, eventType := range []string{"payment.confirmed", "payment.settled", "payment.confirmed"} {
		if err := processor.HandlePaymentEvent(context.Background(), payload, eventType); err != nil {
			t.Fatalf("handling %s: %v", eventType, err)
		}
	}

	if got, want := balanceOf(t, svc, payer), int64(60_000); got != want {
		t.Errorf("balance after one payment delivered three times = %d, want %d", got, want)
	}

	debits := 0
	for _, e := range entriesFor(t, repo, payer) {
		if e.EntryType == domain.EntryTypeDebit {
			debits++
		}
	}
	if debits != 1 {
		t.Errorf("expected exactly 1 payment debit, got %d", debits)
	}
}

// TestConcurrentPaymentDeliveriesBookOnce fires the same settlement at the
// ledger from many goroutines, the shape of a Kafka redelivery or a rebalance:
// exactly one of them may move money.
func TestConcurrentPaymentDeliveriesBookOnce(t *testing.T) {
	svc, repo := newTestService()
	processor := events.NewPaymentEventProcessor(svc, repo, zerolog.Nop())

	payer := uuid.New()
	fundAccount(t, svc, payer, 100_000)

	paymentID := uuid.New()
	payload := settledPayload(paymentID, payer, 30_000, uuid.New().String())

	const deliverers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(deliverers)
	for i := 0; i < deliverers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_ = processor.HandlePaymentEvent(context.Background(), payload, "payment.settled")
		}()
	}
	close(start)
	wg.Wait()

	if got, want := balanceOf(t, svc, payer), int64(70_000); got != want {
		t.Errorf("balance after %d concurrent deliveries = %d, want %d (money moved more than once)", deliverers, got, want)
	}

	debits := 0
	for _, e := range entriesFor(t, repo, payer) {
		if e.EntryType == domain.EntryTypeDebit {
			debits++
		}
	}
	if debits != 1 {
		t.Errorf("expected exactly 1 payment debit under concurrency, got %d", debits)
	}
}
