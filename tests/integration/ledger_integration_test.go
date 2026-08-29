package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/service"
)

type mockLedgerRepository struct {
	mu              sync.RWMutex
	transactions    map[uuid.UUID]*domain.LedgerTransaction
	idempotencyKeys map[string]*domain.LedgerTransaction
	entries         map[uuid.UUID]*domain.LedgerEntry
	accountEntries  map[uuid.UUID][]*domain.LedgerEntry
	reservations    map[uuid.UUID]*domain.Reservation
}

func newMockLedgerRepository() *mockLedgerRepository {
	return &mockLedgerRepository{
		transactions:    make(map[uuid.UUID]*domain.LedgerTransaction),
		idempotencyKeys: make(map[string]*domain.LedgerTransaction),
		entries:         make(map[uuid.UUID]*domain.LedgerEntry),
		accountEntries:  make(map[uuid.UUID][]*domain.LedgerEntry),
		reservations:    make(map[uuid.UUID]*domain.Reservation),
	}
}

func (m *mockLedgerRepository) CreateTransaction(ctx context.Context, tx *domain.LedgerTransaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transactions[tx.TransactionID] = tx
	m.idempotencyKeys[tx.IdempotencyKey] = tx
	return nil
}

func (m *mockLedgerRepository) GetTransaction(ctx context.Context, id uuid.UUID) (*domain.LedgerTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tx, ok := m.transactions[id]; ok {
		return tx, nil
	}
	return nil, domain.ErrTransactionNotFound
}

func (m *mockLedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.idempotencyKeys[key], nil
}

func (m *mockLedgerRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tx, ok := m.transactions[id]; ok {
		tx.Status = status
		now := time.Now().UTC()
		tx.CompletedAt = &now
		return nil
	}
	return domain.ErrTransactionNotFound
}

func (m *mockLedgerRepository) CreateEntry(ctx context.Context, entry *domain.LedgerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[entry.EntryID] = entry
	m.accountEntries[entry.AccountID] = append(m.accountEntries[entry.AccountID], entry)
	return nil
}

func (m *mockLedgerRepository) CreateEntryIfNotExists(ctx context.Context, entry *domain.LedgerEntry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[entry.EntryID]; exists {
		return false, nil
	}
	m.entries[entry.EntryID] = entry
	m.accountEntries[entry.AccountID] = append(m.accountEntries[entry.AccountID], entry)
	return true, nil
}

func (m *mockLedgerRepository) GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := m.accountEntries[accountID]
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (m *mockLedgerRepository) GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*domain.LedgerEntry
	for _, e := range m.entries {
		if e.TransactionID == txID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (m *mockLedgerRepository) GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accountEntries[accountID], nil
}

func (m *mockLedgerRepository) CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.reservations[reservation.ReservationID]; exists {
		return false, nil
	}
	m.reservations[reservation.ReservationID] = reservation
	return true, nil
}

func (m *mockLedgerRepository) GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if res, ok := m.reservations[id]; ok {
		return res, nil
	}
	return nil, domain.ErrReservationNotFound
}

func (m *mockLedgerRepository) GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*domain.Reservation
	for _, r := range m.reservations {
		if r.AccountID == accountID && r.Status == domain.ReservationStatusActive {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockLedgerRepository) UpdateReservationStatus(ctx context.Context, id uuid.UUID, status domain.ReservationStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.reservations[id]
	if !ok {
		return domain.ErrReservationNotFound
	}
	if res.Status != domain.ReservationStatusActive {
		return domain.ErrReservationNotActive
	}
	res.Status = status
	now := time.Now().UTC()
	switch status {
	case domain.ReservationStatusReleased:
		res.ReleasedAt = &now
	case domain.ReservationStatusSettled:
		res.SettledAt = &now
	}
	return nil
}

func (m *mockLedgerRepository) SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total int64
	for _, e := range m.accountEntries[accountID] {
		if e.EntryType == domain.EntryTypeDebit {
			total += e.Amount
		}
	}
	return total, nil
}

func (m *mockLedgerRepository) SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total int64
	for _, e := range m.accountEntries[accountID] {
		if e.EntryType == domain.EntryTypeCredit {
			total += e.Amount
		}
	}
	return total, nil
}

func (m *mockLedgerRepository) GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := m.accountEntries[accountID]
	if len(entries) == 0 {
		return 0, nil
	}
	return entries[len(entries)-1].BalanceAfter, nil
}

func (m *mockLedgerRepository) SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total int64
	for _, r := range m.reservations {
		if r.AccountID == accountID && r.Status == domain.ReservationStatusActive {
			total += r.Amount
		}
	}
	return total, nil
}

func newIntegrationService() (*service.LedgerService, *mockLedgerRepository) {
	repo := newMockLedgerRepository()
	logger := zerolog.Nop()
	svc := service.NewLedgerService(repo, logger)
	return svc, repo
}

func TestDoubleEntryCreation(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	debitAccount := uuid.New()
	creditAccount := uuid.New()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  debitAccount,
		CreditAccountID: creditAccount,
		Amount:          5000,
		Currency:        "GBP",
		Description:     "integration test transfer",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	tx, entries, err := svc.CreateDoubleEntryTransaction(ctx, req)
	require := func() {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tx == nil {
			t.Fatal("expected transaction, got nil")
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	}
	require()

	var totalDebits, totalCredits int64
	for _, e := range entries {
		switch e.EntryType {
		case domain.EntryTypeDebit:
			totalDebits += e.Amount
		case domain.EntryTypeCredit:
			totalCredits += e.Amount
		}
	}

	if totalDebits != totalCredits {
		t.Errorf("debits (%d) != credits (%d)", totalDebits, totalCredits)
	}
}

func TestBalanceVerification(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()
	otherAccount := uuid.New()

	depositReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  otherAccount,
		CreditAccountID: accountID,
		Amount:          10000,
		Currency:        "GBP",
		Description:     "deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, depositReq); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}

	if balance.Balance != 10000 {
		t.Errorf("balance expected 10000, got %d", balance.Balance)
	}
	if balance.Available != 10000 {
		t.Errorf("available expected 10000, got %d", balance.Available)
	}

	result, err := svc.VerifyBalanceIntegrity(ctx, accountID)
	if err != nil {
		t.Fatalf("verify integrity failed: %v", err)
	}
	if !result.IsBalanced {
		t.Error("account should be balanced")
	}
}

func TestReservationLifecycle(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()

	depositReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: accountID,
		Amount:          10000,
		Currency:        "GBP",
		Description:     "deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, depositReq); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	res, err := svc.ReserveFunds(ctx, accountID, 3000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}
	if balance.Pending != 3000 {
		t.Errorf("pending expected 3000, got %d", balance.Pending)
	}
	if balance.Available != 7000 {
		t.Errorf("available expected 7000, got %d", balance.Available)
	}

	entry, err := svc.SettleReservation(ctx, res.ReservationID)
	if err != nil {
		t.Fatalf("settle failed: %v", err)
	}
	if entry == nil {
		t.Fatal("settle should return an entry")
	}

	balance, err = svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}
	if balance.Balance != 7000 {
		t.Errorf("balance after settle expected 7000, got %d", balance.Balance)
	}
	if balance.Pending != 0 {
		t.Errorf("pending after settle expected 0, got %d", balance.Pending)
	}
}

func TestConcurrentReservationProtection(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()

	depositReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: accountID,
		Amount:          10000,
		Currency:        "GBP",
		Description:     "deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, depositReq); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	var wg sync.WaitGroup
	var successCount int64
	var failCount int64

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.ReserveFunds(ctx, accountID, 6000, uuid.New(), "GBP", 30*time.Minute)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful reservation, got %d", successCount)
	}
	if failCount != 9 {
		t.Errorf("expected 9 failed reservations, got %d", failCount)
	}

	available, err := svc.GetAvailableBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get available balance failed: %v", err)
	}
	if available != 4000 {
		t.Errorf("expected available balance 4000, got %d", available)
	}
}

func TestMultipleTransfersTrackBalance(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountA := uuid.New()
	accountB := uuid.New()

	topUp := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: accountA,
		Amount:          10000,
		Currency:        "GBP",
		Description:     "initial top up",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, topUp); err != nil {
		t.Fatalf("top up failed: %v", err)
	}

	transfer1 := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  accountA,
		CreditAccountID: accountB,
		Amount:          3000,
		Currency:        "GBP",
		Description:     "transfer to B",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, transfer1); err != nil {
		t.Fatalf("transfer 1 failed: %v", err)
	}

	transfer2 := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  accountA,
		CreditAccountID: accountB,
		Amount:          2000,
		Currency:        "GBP",
		Description:     "transfer to B again",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, transfer2); err != nil {
		t.Fatalf("transfer 2 failed: %v", err)
	}

	balanceA, err := svc.GetAccountBalance(ctx, accountA)
	if err != nil {
		t.Fatalf("get balance A failed: %v", err)
	}
	if balanceA.Balance != 5000 {
		t.Errorf("account A balance expected 5000, got %d", balanceA.Balance)
	}

	balanceB, err := svc.GetAccountBalance(ctx, accountB)
	if err != nil {
		t.Fatalf("get balance B failed: %v", err)
	}
	if balanceB.Balance != 5000 {
		t.Errorf("account B balance expected 5000, got %d", balanceB.Balance)
	}
}

func TestIdempotencyPreventsDuplicate(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	debitAccount := uuid.New()
	creditAccount := uuid.New()
	idempotencyKey := uuid.New().String()

	req1 := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  debitAccount,
		CreditAccountID: creditAccount,
		Amount:          2000,
		Currency:        "GBP",
		Description:     "first attempt",
		IdempotencyKey:  idempotencyKey,
		TransactionType: domain.TransactionTypeTransfer,
	}

	tx1, _, err := svc.CreateDoubleEntryTransaction(ctx, req1)
	if err != nil {
		t.Fatalf("first attempt failed: %v", err)
	}

	req2 := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  debitAccount,
		CreditAccountID: creditAccount,
		Amount:          3000,
		Currency:        "GBP",
		Description:     "second attempt different amount",
		IdempotencyKey:  idempotencyKey,
		TransactionType: domain.TransactionTypeTransfer,
	}

	tx2, _, err := svc.CreateDoubleEntryTransaction(ctx, req2)
	if err != nil {
		t.Fatalf("second attempt failed: %v", err)
	}

	if tx1.TransactionID != tx2.TransactionID {
		t.Error("idempotent call should return same transaction")
	}
	if tx2.TotalAmount != 2000 {
		t.Errorf("idempotent call should return original amount, got %d", tx2.TotalAmount)
	}
}

func TestInvalidAmountRejected(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	negativeReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: uuid.New(),
		Amount:          -100,
		Currency:        "GBP",
		Description:     "negative amount",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, negativeReq)
	if err != domain.ErrInvalidAmount {
		t.Errorf("negative amount should be rejected, got: %v", err)
	}

	zeroReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: uuid.New(),
		Amount:          0,
		Currency:        "GBP",
		Description:     "zero amount",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	_, _, err = svc.CreateDoubleEntryTransaction(ctx, zeroReq)
	if err != domain.ErrInvalidAmount {
		t.Errorf("zero amount should be rejected, got: %v", err)
	}
}

func TestSameAccountDebitCreditRejected(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  accountID,
		CreditAccountID: accountID,
		Amount:          1000,
		Currency:        "GBP",
		Description:     "same account",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err == nil {
		t.Error("same account debit and credit should be rejected")
	}
}

func TestReleaseReservationRestoresAvailable(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()

	depositReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: accountID,
		Amount:          5000,
		Currency:        "GBP",
		Description:     "deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, depositReq); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	res, err := svc.ReserveFunds(ctx, accountID, 2000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	availableBefore, _ := svc.GetAvailableBalance(ctx, accountID)
	if availableBefore != 3000 {
		t.Errorf("available after reserve expected 3000, got %d", availableBefore)
	}

	if err := svc.ReleaseReservation(ctx, res.ReservationID); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	availableAfter, _ := svc.GetAvailableBalance(ctx, accountID)
	if availableAfter != 5000 {
		t.Errorf("available after release expected 5000, got %d", availableAfter)
	}
}

func TestCurrencyMismatchRejected(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: uuid.New(),
		Amount:          1000,
		Currency:        "GBP",
		Description:     "mismatched currency",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
		Lines: []domain.DoubleEntryLine{
			{AccountID: uuid.New(), EntryType: domain.EntryTypeDebit, Amount: 1000, Currency: "GBP"},
			{AccountID: uuid.New(), EntryType: domain.EntryTypeCredit, Amount: 1000, Currency: "USD"},
		},
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != domain.ErrInvalidCurrency {
		t.Errorf("currency mismatch should be rejected, got: %v", err)
	}
}

func TestUnbalancedLinesRejected(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: uuid.New(),
		Amount:          1000,
		Currency:        "GBP",
		Description:     "unbalanced",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
		Lines: []domain.DoubleEntryLine{
			{AccountID: uuid.New(), EntryType: domain.EntryTypeDebit, Amount: 1000, Currency: "GBP"},
			{AccountID: uuid.New(), EntryType: domain.EntryTypeCredit, Amount: 500, Currency: "GBP"},
			{AccountID: uuid.New(), EntryType: domain.EntryTypeCredit, Amount: 500, Currency: "GBP"},
		},
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != domain.ErrDoubleEntryMismatch {
		t.Errorf("unbalanced lines should be rejected, got: %v", err)
	}
}

func TestSettleNonexistentReservationFails(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	_, err := svc.SettleReservation(ctx, uuid.New())
	if err != domain.ErrReservationNotFound {
		t.Errorf("settling nonexistent reservation should fail, got: %v", err)
	}
}

func TestReleaseNonexistentReservationFails(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	err := svc.ReleaseReservation(ctx, uuid.New())
	if err != domain.ErrReservationNotFound {
		t.Errorf("releasing nonexistent reservation should fail, got: %v", err)
	}
}

func TestReserveMoreThanAvailableFails(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()

	depositReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: accountID,
		Amount:          1000,
		Currency:        "GBP",
		Description:     "small deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, depositReq); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	_, err := svc.ReserveFunds(ctx, accountID, 5000, uuid.New(), "GBP", 30*time.Minute)
	if err != domain.ErrInsufficientFunds {
		t.Errorf("reserving more than available should return ErrInsufficientFunds, got: %v", err)
	}
}

func TestGetEntriesReturnsCorrectLimit(t *testing.T) {
	svc, _ := newIntegrationService()
	ctx := context.Background()

	accountID := uuid.New()
	otherAccount := uuid.New()

	for i := 0; i < 5; i++ {
		req := &domain.CreateDoubleEntryRequest{
			DebitAccountID:  otherAccount,
			CreditAccountID: accountID,
			Amount:          100,
			Currency:        "GBP",
			Description:     fmt.Sprintf("transfer %d", i),
			IdempotencyKey:  uuid.New().String(),
			TransactionType: domain.TransactionTypeTransfer,
		}
		if _, _, err := svc.CreateDoubleEntryTransaction(ctx, req); err != nil {
			t.Fatalf("transfer %d failed: %v", i, err)
		}
	}

	entries, err := svc.GetEntries(ctx, accountID, 3)
	if err != nil {
		t.Fatalf("get entries failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}
