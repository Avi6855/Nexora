package financial

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/service"
)

type invariantRepository struct {
	transactions    map[uuid.UUID]*domain.LedgerTransaction
	idempotencyKeys map[string]*domain.LedgerTransaction
	entries         map[uuid.UUID]*domain.LedgerEntry
	accountEntries  map[uuid.UUID][]*domain.LedgerEntry
	reservations    map[uuid.UUID]*domain.Reservation
}

func newInvariantRepository() *invariantRepository {
	return &invariantRepository{
		transactions:    make(map[uuid.UUID]*domain.LedgerTransaction),
		idempotencyKeys: make(map[string]*domain.LedgerTransaction),
		entries:         make(map[uuid.UUID]*domain.LedgerEntry),
		accountEntries:  make(map[uuid.UUID][]*domain.LedgerEntry),
		reservations:    make(map[uuid.UUID]*domain.Reservation),
	}
}

func (r *invariantRepository) CreateTransaction(ctx context.Context, tx *domain.LedgerTransaction) error {
	r.transactions[tx.TransactionID] = tx
	r.idempotencyKeys[tx.IdempotencyKey] = tx
	return nil
}

func (r *invariantRepository) GetTransaction(ctx context.Context, id uuid.UUID) (*domain.LedgerTransaction, error) {
	if tx, ok := r.transactions[id]; ok {
		return tx, nil
	}
	return nil, domain.ErrTransactionNotFound
}

func (r *invariantRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	return r.idempotencyKeys[key], nil
}

func (r *invariantRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus) error {
	if tx, ok := r.transactions[id]; ok {
		tx.Status = status
		now := time.Now().UTC()
		tx.CompletedAt = &now
		return nil
	}
	return domain.ErrTransactionNotFound
}

func (r *invariantRepository) CreateEntry(ctx context.Context, entry *domain.LedgerEntry) error {
	r.entries[entry.EntryID] = entry
	r.accountEntries[entry.AccountID] = append(r.accountEntries[entry.AccountID], entry)
	return nil
}

func (r *invariantRepository) CreateEntryIfNotExists(ctx context.Context, entry *domain.LedgerEntry) (bool, error) {
	if _, exists := r.entries[entry.EntryID]; exists {
		return false, nil
	}
	r.entries[entry.EntryID] = entry
	r.accountEntries[entry.AccountID] = append(r.accountEntries[entry.AccountID], entry)
	return true, nil
}

func (r *invariantRepository) GetEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.LedgerEntry, error) {
	entries := r.accountEntries[accountID]
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (r *invariantRepository) GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]*domain.LedgerEntry, error) {
	var result []*domain.LedgerEntry
	for _, e := range r.entries {
		if e.TransactionID == txID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (r *invariantRepository) GetAllEntriesByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.LedgerEntry, error) {
	return r.accountEntries[accountID], nil
}

func (r *invariantRepository) CreateReservation(ctx context.Context, reservation *domain.Reservation) (bool, error) {
	if _, exists := r.reservations[reservation.ReservationID]; exists {
		return false, nil
	}
	r.reservations[reservation.ReservationID] = reservation
	return true, nil
}

func (r *invariantRepository) GetReservation(ctx context.Context, id uuid.UUID) (*domain.Reservation, error) {
	if res, ok := r.reservations[id]; ok {
		return res, nil
	}
	return nil, domain.ErrReservationNotFound
}

func (r *invariantRepository) GetActiveReservationsByAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Reservation, error) {
	var result []*domain.Reservation
	for _, res := range r.reservations {
		if res.AccountID == accountID && res.Status == domain.ReservationStatusActive {
			result = append(result, res)
		}
	}
	return result, nil
}

func (r *invariantRepository) UpdateReservationStatus(ctx context.Context, id uuid.UUID, status domain.ReservationStatus) error {
	res, ok := r.reservations[id]
	if !ok {
		return domain.ErrReservationNotFound
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

func (r *invariantRepository) SumDebitsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var total int64
	for _, e := range r.accountEntries[accountID] {
		if e.EntryType == domain.EntryTypeDebit {
			total += e.Amount
		}
	}
	return total, nil
}

func (r *invariantRepository) SumCreditsByAccount(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var total int64
	for _, e := range r.accountEntries[accountID] {
		if e.EntryType == domain.EntryTypeCredit {
			total += e.Amount
		}
	}
	return total, nil
}

func (r *invariantRepository) GetLatestBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	entries := r.accountEntries[accountID]
	if len(entries) == 0 {
		return 0, nil
	}
	return entries[len(entries)-1].BalanceAfter, nil
}

func (r *invariantRepository) SumActiveReservationAmounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var total int64
	for _, res := range r.reservations {
		if res.AccountID == accountID && res.Status == domain.ReservationStatusActive {
			total += res.Amount
		}
	}
	return total, nil
}

func newFinancialService() (*service.LedgerService, *invariantRepository) {
	repo := newInvariantRepository()
	logger := zerolog.Nop()
	svc := service.NewLedgerService(repo, logger)
	return svc, repo
}

func TestDebitsEqualCredits(t *testing.T) {
	svc, _ := newFinancialService()
	ctx := context.Background()

	accounts := make([]uuid.UUID, 5)
	for i := range accounts {
		accounts[i] = uuid.New()
	}

	for i := 0; i < 10; i++ {
		req := &domain.CreateDoubleEntryRequest{
			DebitAccountID:  accounts[i%len(accounts)],
			CreditAccountID: accounts[(i+1)%len(accounts)],
			Amount:          1000,
			Currency:        "GBP",
			Description:     "transfer test",
			IdempotencyKey:  uuid.New().String(),
			TransactionType: domain.TransactionTypeTransfer,
		}

		_, entries, err := svc.CreateDoubleEntryTransaction(ctx, req)
		if err != nil {
			t.Fatalf("transaction %d failed: %v", i, err)
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
			t.Errorf("transaction %d failed validation: %v", i, err)
		}
	}
}

func TestNoDoubleSpend(t *testing.T) {
	svc, repo := newFinancialService()
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

	res, err := svc.ReserveFunds(ctx, accountID, 3000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("first reserve failed: %v", err)
	}

	_, err = svc.ReserveFunds(ctx, accountID, 3000, uuid.New(), "GBP", 30*time.Minute)
	if err != domain.ErrInsufficientFunds {
		t.Errorf("second reserve should fail, got: %v", err)
	}

	err = svc.SettleReservation(ctx, res.ReservationID)
	if err != nil {
		t.Fatalf("settle failed: %v", err)
	}

	entries := repo.accountEntries[accountID]
	var totalDebits int64
	for _, e := range entries {
		if e.EntryType == domain.EntryTypeDebit {
			totalDebits += e.Amount
		}
	}

	if totalDebits != 3000 {
		t.Errorf("total debits should be 3000, got %d", totalDebits)
	}
}

func TestNoDuplicatePaymentFromDuplicateEvent(t *testing.T) {
	svc, _ := newFinancialService()
	ctx := context.Background()

	accountID := uuid.New()
	idempotencyKey := uuid.New().String()

	for i := 0; i < 5; i++ {
		req := &domain.CreateDoubleEntryRequest{
			DebitAccountID:  uuid.New(),
			CreditAccountID: accountID,
			Amount:          1000,
			Currency:        "GBP",
			Description:     "duplicate event test",
			IdempotencyKey:  idempotencyKey,
			TransactionType: domain.TransactionTypePayment,
		}

		_, _, err := svc.CreateDoubleEntryTransaction(ctx, req)
		if err != nil {
			t.Fatalf("attempt %d failed: %v", i, err)
		}
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}

	if balance.Balance != 1000 {
		t.Errorf("expected balance 1000, got %d (duplicate payments were applied)", balance.Balance)
	}
}

func TestBalanceReconstruction(t *testing.T) {
	svc, _ := newFinancialService()
	ctx := context.Background()

	accountID := uuid.New()
	otherAccount := uuid.New()

	transactions := []struct {
		amount int64
		desc   string
	}{
		{1000, "initial deposit"},
		{-200, "payment 1"},
		{-300, "payment 2"},
		{500, "refund"},
		{-100, "fee"},
	}

	expectedBalance := int64(0)
	for _, tx := range transactions {
		req := &domain.CreateDoubleEntryRequest{
			DebitAccountID:  otherAccount,
			CreditAccountID: accountID,
			Amount:          tx.amount,
			Currency:        "GBP",
			Description:     tx.desc,
			IdempotencyKey:  uuid.New().String(),
			TransactionType: domain.TransactionTypeTransfer,
		}

		if tx.amount < 0 {
			req.DebitAccountID = accountID
			req.CreditAccountID = otherAccount
			req.Amount = -tx.amount
		}

		if _, _, err := svc.CreateDoubleEntryTransaction(ctx, req); err != nil {
			t.Fatalf("transaction %s failed: %v", tx.desc, err)
		}

		expectedBalance += tx.amount
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}

	if balance.Balance != expectedBalance {
		t.Errorf("expected balance %d, got %d", expectedBalance, balance.Balance)
	}

	result, err := svc.VerifyBalanceIntegrity(ctx, accountID)
	if err != nil {
		t.Fatalf("verify integrity failed: %v", err)
	}

	if !result.IsBalanced {
		t.Errorf("account should be balanced, computed=%d, latest=%d", result.ComputedBalance, result.LatestBalance)
	}
}

func TestReservationConsistency(t *testing.T) {
	svc, _ := newFinancialService()
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

	res1, err := svc.ReserveFunds(ctx, accountID, 3000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserve 1 failed: %v", err)
	}

	res2, err := svc.ReserveFunds(ctx, accountID, 2000, uuid.New(), "GBP", 30*time.Minute)
	if err != nil {
		t.Fatalf("reserve 2 failed: %v", err)
	}

	balance, err := svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}

	if balance.Balance != 10000 {
		t.Errorf("balance should still be 10000, got %d", balance.Balance)
	}
	if balance.Pending != 5000 {
		t.Errorf("pending should be 5000, got %d", balance.Pending)
	}
	if balance.Available != 5000 {
		t.Errorf("available should be 5000, got %d", balance.Available)
	}

	if err := svc.ReleaseReservation(ctx, res1.ReservationID); err != nil {
		t.Fatalf("release 1 failed: %v", err)
	}

	balance, err = svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}
	if balance.Pending != 2000 {
		t.Errorf("pending after release should be 2000, got %d", balance.Pending)
	}
	if balance.Available != 8000 {
		t.Errorf("available after release should be 8000, got %d", balance.Available)
	}

	entry, err := svc.SettleReservation(ctx, res2.ReservationID)
	if err != nil {
		t.Fatalf("settle 2 failed: %v", err)
	}
	if entry == nil {
		t.Fatal("settle should return an entry")
	}

	balance, err = svc.GetAccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance failed: %v", err)
	}
	if balance.Balance != 8000 {
		t.Errorf("balance after settle should be 8000, got %d", balance.Balance)
	}
	if balance.Pending != 0 {
		t.Errorf("pending after settle should be 0, got %d", balance.Pending)
	}
	if balance.Available != 8000 {
		t.Errorf("available after settle should be 8000, got %d", balance.Available)
	}
}
