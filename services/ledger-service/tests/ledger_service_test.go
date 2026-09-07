package tests

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

type mockRepository struct {
	mu              sync.RWMutex
	transactions    map[uuid.UUID]*domain.LedgerTransaction
	idempotencyKeys map[string]*domain.LedgerTransaction
	entries         map[uuid.UUID]*domain.LedgerEntry
	accountEntries  map[uuid.UUID][]*domain.LedgerEntry
	reservations    map[uuid.UUID]*domain.Reservation
	bookedPayments  map[uuid.UUID]bool
	notes           map[uuid.UUID]*domain.TransactionNote
	guards          map[uuid.UUID]*domain.IntegrityGuard
	integrityEvents []*domain.IntegrityEvent
	nextEntryID     int64
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		transactions:    make(map[uuid.UUID]*domain.LedgerTransaction),
		idempotencyKeys: make(map[string]*domain.LedgerTransaction),
		entries:         make(map[uuid.UUID]*domain.LedgerEntry),
		accountEntries:  make(map[uuid.UUID][]*domain.LedgerEntry),
		reservations:    make(map[uuid.UUID]*domain.Reservation),
	}
}

func (m *mockRepository) CreateTransaction(ctx context.Context, tx *domain.LedgerTransaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.idempotencyKeys[tx.IdempotencyKey]; exists {
		return fmt.Errorf("idempotency conflict")
	}
	m.transactions[tx.TransactionID] = tx
	m.idempotencyKeys[tx.IdempotencyKey] = tx
	return nil
}

func (m *mockRepository) GetTransaction(ctx context.Context, id uuid.UUID) (*domain.LedgerTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tx, ok := m.transactions[id]; ok {
		return tx, nil
	}
	return nil, domain.ErrTransactionNotFound
}

func (m *mockRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tx, ok := m.idempotencyKeys[key]; ok {
		return tx, nil
	}
	return nil, nil
}

func (m *mockRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus) error {
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

func (m *mockRepository) CreateEntry(ctx context.Context, entry *domain.LedgerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[entry.EntryID] = entry
	m.accountEntries[entry.AccountID] = append(m.accountEntries[entry.AccountID], entry)
	return nil
}

func (m *mockRepository) CreateEntryIfNotExists(ctx context.Context, entry *domain.LedgerEntry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[entry.EntryID]; exists {
		return false, nil
	}
	m.entries[entry.EntryID] = entry
	m.accountEntries[entry.AccountID] = append(m.accountEntries[entry.AccountID], entry)
	return true, nil
}

func (m *mockRepository) GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, filter domain.EntryFilter) ([]*domain.LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := m.accountEntries[accountID]
	result := make([]*domain.LedgerEntry, 0, len(entries))
	for _, e := range entries {
		if !filter.Matches(e) {
			continue
		}
		result = append(result, e)
		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}
	return result, nil
}

func (m *mockRepository) GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error) {
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

func (m *mockRepository) GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accountEntries[accountID], nil
}

func (m *mockRepository) CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.reservations[reservation.ReservationID]; exists {
		return false, nil
	}
	m.reservations[reservation.ReservationID] = reservation
	return true, nil
}

func (m *mockRepository) GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if res, ok := m.reservations[id]; ok {
		return res, nil
	}
	return nil, domain.ErrReservationNotFound
}

func (m *mockRepository) GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error) {
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

func (m *mockRepository) UpdateReservationStatus(ctx context.Context, id uuid.UUID, status domain.ReservationStatus) error {
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

func (m *mockRepository) SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
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

func (m *mockRepository) SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
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

func (m *mockRepository) GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := m.accountEntries[accountID]
	if len(entries) == 0 {
		return 0, nil
	}
	return entries[len(entries)-1].BalanceAfter, nil
}

func (m *mockRepository) SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
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

func (m *mockRepository) GetAccountOwner(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (m *mockRepository) UpsertNote(ctx context.Context, note *domain.TransactionNote) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notes == nil {
		m.notes = make(map[uuid.UUID]*domain.TransactionNote)
	}
	m.notes[note.EntryID] = note
	return nil
}

func (m *mockRepository) GetNote(ctx context.Context, entryID uuid.UUID) (*domain.TransactionNote, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if n, ok := m.notes[entryID]; ok {
		return n, nil
	}
	return nil, nil
}

func (m *mockRepository) GetEntryByID(ctx context.Context, entryID uuid.UUID) (*domain.LedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.entries[entryID]; ok {
		return e, nil
	}
	return nil, domain.ErrTransactionNotFound
}

// IsAccountLocked implements the lockdown probe; the mock reports no accounts
// as locked unless a test sets a flag (not needed by current cases).
func (m *mockRepository) IsAccountLocked(ctx context.Context, accountID uuid.UUID) (bool, error) {
	return false, nil
}

func (m *mockRepository) MarkPaymentBooked(ctx context.Context, paymentID uuid.UUID, idempotencyKey, eventType string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bookedPayments == nil {
		m.bookedPayments = map[uuid.UUID]bool{}
	}
	if m.bookedPayments[paymentID] {
		return false, nil
	}
	m.bookedPayments[paymentID] = true
	return true, nil
}

// ── Ledger invariant monitor mocks ──
func (m *mockRepository) ListEntryAccounts(ctx context.Context) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0)
	for _, e := range m.entries {
		if !seen[e.AccountID] {
			seen[e.AccountID] = true
			out = append(out, e.AccountID)
		}
	}
	return out, nil
}

func (m *mockRepository) GetIntegrityGuard(ctx context.Context, accountID uuid.UUID) (*domain.IntegrityGuard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.guards == nil {
		return nil, nil
	}
	return m.guards[accountID], nil
}

func (m *mockRepository) SetIntegrityGuard(ctx context.Context, g *domain.IntegrityGuard) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.guards == nil {
		m.guards = map[uuid.UUID]*domain.IntegrityGuard{}
	}
	m.guards[g.AccountID] = g
	return nil
}

func (m *mockRepository) ClearIntegrityGuard(ctx context.Context, accountID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.guards, accountID)
	return nil
}

func (m *mockRepository) InsertIntegrityEvent(ctx context.Context, ev *domain.IntegrityEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.integrityEvents = append(m.integrityEvents, ev)
	return nil
}

func (m *mockRepository) ListIntegrityEvents(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.IntegrityEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.integrityEvents, nil
}

func newTestService() (*service.LedgerService, *mockRepository) {
	repo := newMockRepository()
	logger := zerolog.Nop()
	svc := service.NewLedgerService(repo, nil, logger)
	return svc, repo
}

func TestDoubleEntryCreatesBalancedEntries(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	debitAccount := uuid.New()
	creditAccount := uuid.New()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  debitAccount,
		CreditAccountID: creditAccount,
		Amount:          5000,
		Currency:        "GBP",
		Description:     "test transfer",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	tx, entries, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx == nil {
		t.Fatal("expected transaction, got nil")
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

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
	if totalDebits != 5000 {
		t.Errorf("expected total debits 5000, got %d", totalDebits)
	}
	if totalCredits != 5000 {
		t.Errorf("expected total credits 5000, got %d", totalCredits)
	}

	if entries[0].EntryType == domain.EntryTypeDebit {
		if entries[0].AccountID != debitAccount {
			t.Error("debit entry has wrong account")
		}
		if entries[0].EntryDirection != domain.EntryDirectionOutbound {
			t.Error("debit entry should be OUTBOUND")
		}
		if entries[0].BalanceAfter != -5000 {
			t.Errorf("debit balance_after expected -5000, got %d", entries[0].BalanceAfter)
		}
	} else {
		if entries[1].AccountID != debitAccount {
			t.Error("debit entry has wrong account")
		}
	}

	if entries[0].TransactionID != tx.TransactionID {
		t.Error("entries should reference the transaction")
	}
}

func TestDebitsEqualCredits(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	debitAccount := uuid.New()
	creditAccount := uuid.New()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  debitAccount,
		CreditAccountID: creditAccount,
		Amount:          1000,
		Currency:        "USD",
		Description:     "credit/debit check",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypePayment,
	}

	_, entries, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var debits, credits []*domain.LedgerEntry
	for _, e := range entries {
		if e.EntryType == domain.EntryTypeDebit {
			debits = append(debits, e)
		} else {
			credits = append(credits, e)
		}
	}

	if err := domain.ValidateDoubleEntry(debits, credits); err != nil {
		t.Errorf("validation failed: %v", err)
	}

	if len(debits) != 1 {
		t.Errorf("expected 1 debit, got %d", len(debits))
	}
	if len(credits) != 1 {
		t.Errorf("expected 1 credit, got %d", len(credits))
	}
}

func TestIdempotencyPreventsDuplicate(t *testing.T) {
	svc, _ := newTestService()
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

func TestConcurrentReservationsDontOverspend(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	accountID := uuid.New()

	firstReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: accountID,
		Amount:          10000,
		Currency:        "GBP",
		Description:     "initial deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, firstReq); err != nil {
		t.Fatalf("setup deposit failed: %v", err)
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

func TestAvailableBalanceEqualsBalanceMinusReserved(t *testing.T) {
	svc, _ := newTestService()
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

	_, err := svc.ReserveFunds(ctx, accountID, 3000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	breakdown, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}

	if breakdown.Balance != 10000 {
		t.Errorf("balance expected 10000, got %d", breakdown.Balance)
	}
	if breakdown.Pending != 3000 {
		t.Errorf("pending expected 3000, got %d", breakdown.Pending)
	}
	if breakdown.Available != 7000 {
		t.Errorf("available expected 7000, got %d", breakdown.Available)
	}
	if breakdown.Available != breakdown.Balance-breakdown.Pending {
		t.Error("available should equal balance - pending")
	}
}

func TestReleasingReservationRestoresAvailable(t *testing.T) {
	svc, _ := newTestService()
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

	pendingAfter, _ := svc.GetPendingBalance(ctx, accountID)
	if pendingAfter != 0 {
		t.Errorf("pending after release expected 0, got %d", pendingAfter)
	}
}

func TestInvalidTransitionFails(t *testing.T) {
	svc, _ := newTestService()
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

	res, err := svc.ReserveFunds(ctx, accountID, 1000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if err := svc.ReleaseReservation(ctx, res.ReservationID); err != nil {
		t.Fatalf("first release should succeed: %v", err)
	}

	err = svc.ReleaseReservation(ctx, res.ReservationID)
	if err != domain.ErrReservationNotActive {
		t.Errorf("releasing already released reservation should return ErrReservationNotActive, got: %v", err)
	}

	_, err = svc.SettleReservation(ctx, res.ReservationID)
	if err != domain.ErrReservationNotActive {
		t.Errorf("settling already released reservation should return ErrReservationNotActive, got: %v", err)
	}
}

func TestNegativeAmountRejected(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: uuid.New(),
		Amount:          -100,
		Currency:        "GBP",
		Description:     "negative amount",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != domain.ErrInvalidAmount {
		t.Errorf("negative amount should be rejected, got: %v", err)
	}
}

func TestZeroAmountRejected(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	req := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  uuid.New(),
		CreditAccountID: uuid.New(),
		Amount:          0,
		Currency:        "GBP",
		Description:     "zero amount",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTransfer,
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != domain.ErrInvalidAmount {
		t.Errorf("zero amount should be rejected, got: %v", err)
	}
}

func TestSameAccountDebitCreditRejected(t *testing.T) {
	svc, _ := newTestService()
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

func TestReserveMoreThanAvailableFails(t *testing.T) {
	svc, _ := newTestService()
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

func TestSettleReservationCreatesEntry(t *testing.T) {
	svc, _ := newTestService()
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

	entry, err := svc.SettleReservation(ctx, res.ReservationID)
	if err != nil {
		t.Fatalf("settle failed: %v", err)
	}

	if entry == nil {
		t.Fatal("settle should return an entry")
	}
	if entry.EntryType != domain.EntryTypeDebit {
		t.Errorf("settled entry should be DEBIT, got %s", entry.EntryType)
	}
	if entry.Amount != 3000 {
		t.Errorf("settled entry amount expected 3000, got %d", entry.Amount)
	}
	if entry.AccountID != accountID {
		t.Error("settled entry should be on the reserved account")
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
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

func TestVerifyIntegrity(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	accountID := uuid.New()
	otherAccount := uuid.New()

	depositReq := &domain.CreateDoubleEntryRequest{
		DebitAccountID:  otherAccount,
		CreditAccountID: accountID,
		Amount:          8000,
		Currency:        "GBP",
		Description:     "deposit",
		IdempotencyKey:  uuid.New().String(),
		TransactionType: domain.TransactionTypeTopUp,
	}

	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, depositReq); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	result, err := svc.VerifyBalanceIntegrity(ctx, accountID)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	if !result.IsBalanced {
		t.Error("account should be balanced after a single transaction")
	}

	if result.ComputedBalance != 8000 {
		t.Errorf("computed balance expected 8000, got %d", result.ComputedBalance)
	}
	if result.LatestBalance != 8000 {
		t.Errorf("latest balance expected 8000, got %d", result.LatestBalance)
	}
	if result.TotalCredits != 8000 {
		t.Errorf("total credits expected 8000, got %d", result.TotalCredits)
	}
}

func TestGetEntriesReturnsCorrectLimit(t *testing.T) {
	svc, _ := newTestService()
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

func TestMultipleTransfersTrackBalanceCorrectly(t *testing.T) {
	svc, _ := newTestService()
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

func TestCurrencyMismatchRejected(t *testing.T) {
	svc, _ := newTestService()
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
	svc, _ := newTestService()
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
			{AccountID: uuid.New(), EntryType: domain.EntryTypeCredit, Amount: 400, Currency: "GBP"},
		},
	}

	_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
	if err != domain.ErrDoubleEntryMismatch {
		t.Errorf("unbalanced lines should be rejected, got: %v", err)
	}
}

func TestReserveNegativeAmountRejected(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	accountID := uuid.New()

	_, err := svc.ReserveFunds(ctx, accountID, -100, uuid.New(), "GBP", 30*time.Minute)
	if err != domain.ErrInvalidAmount {
		t.Errorf("negative reserve should be rejected, got: %v", err)
	}
}

func TestSettleNonexistentReservationFails(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.SettleReservation(ctx, uuid.New())
	if err != domain.ErrReservationNotFound {
		t.Errorf("settling nonexistent reservation should fail, got: %v", err)
	}
}

func TestReleaseNonexistentReservationFails(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	err := svc.ReleaseReservation(ctx, uuid.New())
	if err != domain.ErrReservationNotFound {
		t.Errorf("releasing nonexistent reservation should fail, got: %v", err)
	}
}

func TestIntegrityMonitorFreezesOnViolationAndSelfHeals(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()

	src := uuid.New() // funded account
	clearing := uuid.New()
	recipient := uuid.New()

	// Fund src with a real double-entry booking.
	if _, _, err := svc.CreateDoubleEntryTransaction(ctx, &domain.CreateDoubleEntryRequest{
		DebitAccountID: clearing, CreditAccountID: src,
		Amount: 100000, Currency: "GBP", Description: "opening balance",
		IdempotencyKey: uuid.New().String(), TransactionType: domain.TransactionTypeTopUp,
	}); err != nil {
		t.Fatalf("funding failed: %v", err)
	}

	// Sanity: a clean account sweeps with no violation.
	clean, err := svc.ScanAccountIntegrity(ctx, src)
	if err != nil {
		t.Fatalf("clean scan failed: %v", err)
	}
	if clean.Violation {
		t.Fatalf("expected clean account to verify, got violation: %s", clean.Message)
	}

	// Corrupt the ledger: inflate the credit amount without touching balances.
	repo.mu.Lock()
	var credit *domain.LedgerEntry
	for _, e := range repo.accountEntries[src] {
		if e.EntryType == domain.EntryTypeCredit {
			credit = e
		}
	}
	repo.mu.Unlock()
	if credit == nil {
		t.Fatal("no credit entry found for src account")
	}
	credit.Amount = 110000

	// The monitor sweep must detect the violation and freeze the account.
	res, err := svc.ScanAccountIntegrity(ctx, src)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !res.Violation {
		t.Fatal("expected violation to be detected after tamper")
	}
	if !res.GuardApplied {
		t.Fatal("expected integrity guard to be applied")
	}
	guard, err := repo.GetIntegrityGuard(ctx, src)
	if err != nil || guard == nil || !guard.Frozen {
		t.Fatalf("expected frozen guard, got %v (err %v)", guard, err)
	}

	// New money movement out of the frozen account must be refused.
	_, _, err = svc.BookTransfer(ctx, &domain.TransferRequest{
		SourceAccountID: src, DestinationAccountID: recipient,
		Amount: 1000, Currency: "GBP", Description: "should be blocked",
		IdempotencyKey: uuid.New().String(),
	})
	if err != domain.ErrAccountIntegrityViolation {
		t.Fatalf("expected ErrAccountIntegrityViolation, got %v", err)
	}
	// New holds must be refused too.
	if _, err := svc.ReserveFunds(ctx, src, 1000, uuid.New(), "GBP", time.Minute); err != domain.ErrAccountIntegrityViolation {
		t.Fatalf("expected ErrAccountIntegrityViolation on reserve, got %v", err)
	}

	// Repair the entry: the next sweep self-heals the guard.
	credit.Amount = 100000
	res, err = svc.ScanAccountIntegrity(ctx, src)
	if err != nil {
		t.Fatalf("post-repair scan failed: %v", err)
	}
	if res.Violation {
		t.Fatalf("expected account clean after repair, got: %s", res.Message)
	}
	guard, err = repo.GetIntegrityGuard(ctx, src)
	if err != nil || guard != nil {
		t.Fatalf("expected guard cleared after repair, got %v (err %v)", guard, err)
	}

	// Money movement works again.
	if _, _, err := svc.BookTransfer(ctx, &domain.TransferRequest{
		SourceAccountID: src, DestinationAccountID: recipient,
		Amount: 1000, Currency: "GBP", Description: "after repair",
		IdempotencyKey: uuid.New().String(),
	}); err != nil {
		t.Fatalf("transfer after repair failed: %v", err)
	}
}

func TestIntegrityMonitorSweepAllAccounts(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	summary, err := svc.ScanAllAccountsIntegrity(ctx)
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if summary == nil {
		t.Fatal("expected sweep summary")
	}
	if summary.AccountsScanned < 0 {
		t.Fatalf("negative accounts scanned: %d", summary.AccountsScanned)
	}
}
