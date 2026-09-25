package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
)

// ── Refund booking ──────────────────────────────────────────────────────────
//
// A refund is money returning from outside the bank (an acquirer refund of a
// settled card presentment). It must behave like every other money movement:
// balanced legs in one batch, exactly-once on its key, and no way to pay the
// customer twice.

func TestBookRefundCreditsTheCustomerWithABalancedClearingLeg(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	accountID := uuid.New()

	tx, entries, err := svc.BookRefund(ctx, &domain.RefundRequest{
		IdempotencyKey: uuid.New().String(),
		AccountID:      accountID,
		Amount:         75_00,
		Currency:       "GBP",
		Description:    "refund Pret A Manger",
	})
	if err != nil {
		t.Fatalf("BookRefund: %v", err)
	}
	if tx.TransactionType != domain.TransactionTypeRefund {
		t.Fatalf("transaction type = %s, want REFUND", tx.TransactionType)
	}
	if tx.Status != domain.TransactionStatusCompleted || tx.CompletedAt == nil {
		t.Fatalf("status = %s (completed_at=%v), want COMPLETED", tx.Status, tx.CompletedAt)
	}
	if len(entries) != 2 {
		t.Fatalf("booked %d entries, want the customer credit and its clearing counter-leg", len(entries))
	}

	var customer, clearing *domain.LedgerEntry
	for _, e := range entries {
		if e.AccountID == accountID {
			customer = e
		}
		if e.AccountID == domain.ClearingAccountID {
			clearing = e
		}
	}
	if customer == nil || clearing == nil {
		t.Fatal("the refund pair is missing a leg")
	}
	if customer.EntryType != domain.EntryTypeCredit || customer.EntryDirection != domain.EntryDirectionInbound {
		t.Fatalf("customer leg = %s/%s, want CREDIT/INBOUND", customer.EntryType, customer.EntryDirection)
	}
	if clearing.EntryType != domain.EntryTypeDebit || clearing.EntryDirection != domain.EntryDirectionOutbound {
		t.Fatalf("clearing leg = %s/%s, want DEBIT/OUTBOUND", clearing.EntryType, clearing.EntryDirection)
	}
	if customer.BalanceAfter != 75_00 {
		t.Fatalf("customer balance after = %d, want 7500", customer.BalanceAfter)
	}
	if clearing.BalanceAfter != -75_00 {
		t.Fatalf("clearing balance after = %d, want -7500", clearing.BalanceAfter)
	}

	// The system-wide invariant the chaos checker asserts.
	var debits, credits int64
	for _, e := range repo.entries {
		switch e.EntryType {
		case domain.EntryTypeDebit:
			debits += e.Amount
		case domain.EntryTypeCredit:
			credits += e.Amount
		}
	}
	if debits != credits {
		t.Fatalf("debits (%d) != credits (%d) after a refund", debits, credits)
	}
}

func TestBookRefundIsExactlyOnce(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	accountID := uuid.New()
	key := uuid.New().String()
	req := &domain.RefundRequest{
		IdempotencyKey: key,
		AccountID:      accountID,
		Amount:         12_00,
		Currency:       "GBP",
	}

	first, firstEntries, err := svc.BookRefund(ctx, req)
	if err != nil {
		t.Fatalf("BookRefund: %v", err)
	}

	// A retried refund (the merchant pressed refund twice, or a Kafka consumer
	// redelivered) must return the original booking, never a second credit.
	second, secondEntries, err := svc.BookRefund(ctx, req)
	if err != nil {
		t.Fatalf("replayed BookRefund: %v", err)
	}
	if second.TransactionID != first.TransactionID {
		t.Fatalf("replay created a new transaction %s (first was %s)", second.TransactionID, first.TransactionID)
	}
	if len(secondEntries) != len(firstEntries) {
		t.Fatalf("replay returned %d entries, want the original %d", len(secondEntries), len(firstEntries))
	}

	var creditsToCustomer int
	for _, e := range repo.entries {
		if e.AccountID == accountID && e.EntryType == domain.EntryTypeCredit {
			creditsToCustomer++
		}
	}
	if creditsToCustomer != 1 {
		t.Fatalf("customer was credited %d times, want exactly 1", creditsToCustomer)
	}
}

func TestBookRefundKeepsTheCustomerBalanceCorrectAfterASpend(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	accountID := uuid.New()
	merchantAccount := uuid.New()

	// The customer spent 30.00 on the card: the settlement debits them.
	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, &domain.CreateDoubleEntryRequest{
		DebitAccountID:  accountID,
		CreditAccountID: merchantAccount,
		Amount:          30_00,
		Currency:        "GBP",
		Description:     "Tesco",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypePayment,
	}); err != nil {
		t.Fatalf("CreateDoubleEntryTransaction: %v", err)
	}

	if _, _, err := svc.BookRefund(ctx, &domain.RefundRequest{
		IdempotencyKey: uuid.New().String(),
		AccountID:      accountID,
		Amount:         30_00,
		Currency:       "GBP",
		Description:    "refund Tesco",
	}); err != nil {
		t.Fatalf("BookRefund: %v", err)
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("GetAccountBalance: %v", err)
	}
	if balance.Balance != 0 {
		t.Fatalf("balance after a full refund = %d, want 0", balance.Balance)
	}

	// And the whole ledger is still balanced.
	var debits, credits int64
	for _, e := range repo.entries {
		switch e.EntryType {
		case domain.EntryTypeDebit:
			debits += e.Amount
		case domain.EntryTypeCredit:
			credits += e.Amount
		}
	}
	if debits != credits {
		t.Fatalf("debits (%d) != credits (%d) after a spend and its refund", debits, credits)
	}
}

func TestBookRefundRefusesInvalidRequests(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()

	cases := []struct {
		name string
		req  *domain.RefundRequest
		want error
	}{
		{
			name: "no idempotency key",
			req:  &domain.RefundRequest{AccountID: uuid.New(), Amount: 100, Currency: "GBP"},
			want: nil,
		},
		{
			name: "zero amount",
			req:  &domain.RefundRequest{IdempotencyKey: uuid.New().String(), AccountID: uuid.New(), Amount: 0, Currency: "GBP"},
			want: domain.ErrInvalidAmount,
		},
		{
			name: "negative amount",
			req:  &domain.RefundRequest{IdempotencyKey: uuid.New().String(), AccountID: uuid.New(), Amount: -5, Currency: "GBP"},
			want: domain.ErrInvalidAmount,
		},
		{
			name: "missing account",
			req:  &domain.RefundRequest{IdempotencyKey: uuid.New().String(), Amount: 100, Currency: "GBP"},
			want: nil,
		},
		{
			name: "clearing account as the beneficiary",
			req:  &domain.RefundRequest{IdempotencyKey: uuid.New().String(), AccountID: domain.ClearingAccountID, Amount: 100, Currency: "GBP"},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.BookRefund(ctx, tc.req)
			if err == nil {
				t.Fatalf("BookRefund accepted %s", tc.name)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	if len(repo.entries) != 0 {
		t.Fatalf("a refused refund wrote %d ledger entries", len(repo.entries))
	}
}
