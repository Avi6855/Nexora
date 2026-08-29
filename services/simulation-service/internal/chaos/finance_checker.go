package chaos

import (
	"context"
	"time"
)

type LedgerBalanceResult struct {
	Balanced    bool  `json:"balanced"`
	TotalDebits int64 `json:"total_debits"`
	TotalCredits int64 `json:"total_credits"`
}

type DuplicateCheckResult struct {
	HasDuplicates  bool  `json:"has_duplicates"`
	DuplicateCount int64 `json:"duplicate_count"`
}

type DoubleSpendResult struct {
	HasDoubleSpend    bool  `json:"has_double_spend"`
	DoubleSpendCount  int64 `json:"double_spend_count"`
}

type ReservationConsistencyResult struct {
	Consistent          bool   `json:"consistent"`
	InconsistencyDetail string `json:"inconsistency_detail,omitempty"`
}

type FinancialInvariantChecker interface {
	CheckDebitsEqualCredits(ctx context.Context) (*LedgerBalanceResult, error)
	CheckNoDuplicatePayments(ctx context.Context) (*DuplicateCheckResult, error)
	CheckNoDoubleSpend(ctx context.Context) (*DoubleSpendResult, error)
	CheckReservationConsistency(ctx context.Context) (*ReservationConsistencyResult, error)
}

type LedgerEntry struct {
	AccountID    string `json:"account_id"`
	TransactionID string `json:"transaction_id"`
	EntryType    string `json:"entry_type"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
}

type LedgerTransaction struct {
	TransactionID  string `json:"transaction_id"`
	IdempotencyKey string `json:"idempotency_key"`
	TotalAmount    int64  `json:"total_amount"`
	Currency       string `json:"currency"`
}

type Reservation struct {
	ReservationID string `json:"reservation_id"`
	AccountID     string `json:"account_id"`
	TransactionID string `json:"transaction_id"`
	Amount        int64  `json:"amount"`
	Status        string `json:"status"`
}

type LedgerProvider interface {
	GetAllEntries(ctx context.Context) ([]*LedgerEntry, error)
	GetAllTransactions(ctx context.Context) ([]*LedgerTransaction, error)
	GetActiveReservations(ctx context.Context) ([]*Reservation, error)
	GetEntriesByTransaction(ctx context.Context, transactionID string) ([]*LedgerEntry, error)
}

type DefaultFinancialInvariantChecker struct {
	ledger LedgerProvider
}

func NewDefaultFinancialInvariantChecker(ledger LedgerProvider) *DefaultFinancialInvariantChecker {
	return &DefaultFinancialInvariantChecker{
		ledger: ledger,
	}
}

func (c *DefaultFinancialInvariantChecker) CheckDebitsEqualCredits(ctx context.Context) (*LedgerBalanceResult, error) {
	entries, err := c.ledger.GetAllEntries(ctx)
	if err != nil {
		return nil, err
	}

	var totalDebits, totalCredits int64
	for _, entry := range entries {
		switch entry.EntryType {
		case "DEBIT":
			totalDebits += entry.Amount
		case "CREDIT":
			totalCredits += entry.Amount
		}
	}

	balanced := totalDebits == totalCredits

	return &LedgerBalanceResult{
		Balanced:     balanced,
		TotalDebits:  totalDebits,
		TotalCredits: totalCredits,
	}, nil
}

func (c *DefaultFinancialInvariantChecker) CheckNoDuplicatePayments(ctx context.Context) (*DuplicateCheckResult, error) {
	transactions, err := c.ledger.GetAllTransactions(ctx)
	if err != nil {
		return nil, err
	}

	keyCounts := make(map[string]int64)
	for _, tx := range transactions {
		if tx.IdempotencyKey != "" {
			keyCounts[tx.IdempotencyKey]++
		}
	}

	var duplicateCount int64
	for _, count := range keyCounts {
		if count > 1 {
			duplicateCount += count - 1
		}
	}

	return &DuplicateCheckResult{
		HasDuplicates:  duplicateCount > 0,
		DuplicateCount: duplicateCount,
	}, nil
}

func (c *DefaultFinancialInvariantChecker) CheckNoDoubleSpend(ctx context.Context) (*DoubleSpendResult, error) {
	entries, err := c.ledger.GetAllEntries(ctx)
	if err != nil {
		return nil, err
	}

	type accountDebit struct {
		accountID     string
		transactionID string
	}

	seenDebits := make(map[accountDebit]int64)
	var doubleSpendCount int64

	for _, entry := range entries {
		if entry.EntryType == "DEBIT" {
			key := accountDebit{
				accountID:     entry.AccountID,
				transactionID: entry.TransactionID,
			}
			seenDebits[key] += entry.Amount
			if seenDebits[key] > entry.Amount {
				doubleSpendCount++
			}
		}
	}

	return &DoubleSpendResult{
		HasDoubleSpend:   doubleSpendCount > 0,
		DoubleSpendCount: doubleSpendCount,
	}, nil
}

func (c *DefaultFinancialInvariantChecker) CheckReservationConsistency(ctx context.Context) (*ReservationConsistencyResult, error) {
	reservations, err := c.ledger.GetActiveReservations(ctx)
	if err != nil {
		return nil, err
	}

	for _, res := range reservations {
		entries, err := c.ledger.GetEntriesByTransaction(ctx, res.TransactionID)
		if err != nil {
			continue
		}

		hasSettlement := false
		for _, entry := range entries {
			if entry.EntryType == "DEBIT" && entry.AccountID == res.AccountID {
				hasSettlement = true
				break
			}
		}

		if res.Status == "SETTLED" && !hasSettlement {
			return &ReservationConsistencyResult{
				Consistent:          false,
				InconsistencyDetail: "settled reservation has no corresponding debit entry for transaction " + res.TransactionID,
			}, nil
		}

		if res.Status == "ACTIVE" && time.Now().UTC().After(time.Now().UTC().Add(time.Hour)) {
			return &ReservationConsistencyResult{
				Consistent:          false,
				InconsistencyDetail: "active reservation may have expired for transaction " + res.TransactionID,
			}, nil
		}
	}

	return &ReservationConsistencyResult{
		Consistent: true,
	}, nil
}

type InMemoryLedgerProvider struct {
	entries       []*LedgerEntry
	transactions  []*LedgerTransaction
	reservations  []*Reservation
}

func NewInMemoryLedgerProvider() *InMemoryLedgerProvider {
	return &InMemoryLedgerProvider{
		entries:      make([]*LedgerEntry, 0),
		transactions: make([]*LedgerTransaction, 0),
		reservations: make([]*Reservation, 0),
	}
}

func (p *InMemoryLedgerProvider) AddEntry(entry *LedgerEntry) {
	p.entries = append(p.entries, entry)
}

func (p *InMemoryLedgerProvider) AddTransaction(tx *LedgerTransaction) {
	p.transactions = append(p.transactions, tx)
}

func (p *InMemoryLedgerProvider) AddReservation(res *Reservation) {
	p.reservations = append(p.reservations, res)
}

func (p *InMemoryLedgerProvider) GetAllEntries(ctx context.Context) ([]*LedgerEntry, error) {
	result := make([]*LedgerEntry, len(p.entries))
	copy(result, p.entries)
	return result, nil
}

func (p *InMemoryLedgerProvider) GetAllTransactions(ctx context.Context) ([]*LedgerTransaction, error) {
	result := make([]*LedgerTransaction, len(p.transactions))
	copy(result, p.transactions)
	return result, nil
}

func (p *InMemoryLedgerProvider) GetActiveReservations(ctx context.Context) ([]*Reservation, error) {
	var active []*Reservation
	for _, res := range p.reservations {
		if res.Status == "ACTIVE" {
			active = append(active, res)
		}
	}
	return active, nil
}

func (p *InMemoryLedgerProvider) GetEntriesByTransaction(ctx context.Context, transactionID string) ([]*LedgerEntry, error) {
	var result []*LedgerEntry
	for _, entry := range p.entries {
		if entry.TransactionID == transactionID {
			result = append(result, entry)
		}
	}
	return result, nil
}
