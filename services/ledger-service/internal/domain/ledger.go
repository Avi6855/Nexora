package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type EntryType string

const (
	EntryTypeDebit  EntryType = "DEBIT"
	EntryTypeCredit EntryType = "CREDIT"
)

type EntryDirection string

const (
	EntryDirectionInbound  EntryDirection = "INBOUND"
	EntryDirectionOutbound EntryDirection = "OUTBOUND"
)

type TransactionType string

const (
	TransactionTypePayment    TransactionType = "PAYMENT"
	TransactionTypeTransfer   TransactionType = "TRANSFER"
	TransactionTypeTopUp      TransactionType = "TOP_UP"
	TransactionTypeWithdrawal TransactionType = "WITHDRAWAL"
	TransactionTypeRefund     TransactionType = "REFUND"
)

type TransactionStatus string

const (
	TransactionStatusPending   TransactionStatus = "PENDING"
	TransactionStatusCompleted TransactionStatus = "COMPLETED"
	TransactionStatusFailed    TransactionStatus = "FAILED"
	TransactionStatusReversed  TransactionStatus = "REVERSED"
)

type ReservationStatus string

const (
	ReservationStatusActive   ReservationStatus = "ACTIVE"
	ReservationStatusReleased ReservationStatus = "RELEASED"
	ReservationStatusSettled  ReservationStatus = "SETTLED"
	ReservationStatusExpired  ReservationStatus = "EXPIRED"
)

var (
	ErrInsufficientFunds      = errors.New("insufficient funds")
	ErrReservationNotFound    = errors.New("reservation not found")
	ErrReservationNotActive   = errors.New("reservation is not active")
	ErrReservationExpired     = errors.New("reservation has expired")
	ErrTransactionNotFound    = errors.New("transaction not found")
	ErrAccountNotFound        = errors.New("account not found")
	ErrIdempotencyConflict    = errors.New("idempotency key already used with different request")
	ErrDoubleEntryMismatch    = errors.New("total debits do not equal total credits")
	ErrInvalidAmount          = errors.New("amount must be positive")
	ErrInvalidCurrency        = errors.New("currency mismatch between debit and credit entries")
	ErrDuplicateEntry         = errors.New("duplicate entry detected")
)

type LedgerEntry struct {
	EntryID        uuid.UUID      `json:"entry_id"`
	AccountID      uuid.UUID      `json:"account_id"`
	TransactionID  uuid.UUID      `json:"transaction_id"`
	EntryType      EntryType      `json:"entry_type"`
	EntryDirection EntryDirection `json:"entry_direction"`
	Amount         int64          `json:"amount"`
	Currency       string         `json:"currency"`
	BalanceBefore  int64          `json:"balance_before"`
	BalanceAfter   int64          `json:"balance_after"`
	Description    string         `json:"description"`
	CorrelationID  string         `json:"correlation_id"`
	CausationID    string         `json:"causation_id"`
	EventVersion   int            `json:"event_version"`
	CreatedAt      time.Time      `json:"created_at"`
}

type LedgerTransaction struct {
	TransactionID  uuid.UUID         `json:"transaction_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	TransactionType TransactionType  `json:"transaction_type"`
	Status         TransactionStatus `json:"status"`
	TotalAmount    int64             `json:"total_amount"`
	Currency       string            `json:"currency"`
	Description    string            `json:"description"`
	CorrelationID  string            `json:"correlation_id"`
	CausationID    string            `json:"causation_id"`
	EventVersion   int               `json:"event_version"`
	CreatedAt      time.Time         `json:"created_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
}

type Reservation struct {
	ReservationID uuid.UUID          `json:"reservation_id"`
	AccountID     uuid.UUID          `json:"account_id"`
	TransactionID uuid.UUID          `json:"transaction_id"`
	Amount        int64              `json:"amount"`
	Currency      string             `json:"currency"`
	Status        ReservationStatus  `json:"status"`
	ExpiresAt     time.Time          `json:"expires_at"`
	CreatedAt     time.Time          `json:"created_at"`
	ReleasedAt    *time.Time         `json:"released_at,omitempty"`
	SettledAt     *time.Time         `json:"settled_at,omitempty"`
}

type DoubleEntryLine struct {
	AccountID uuid.UUID `json:"account_id"`
	EntryType EntryType `json:"entry_type"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
}

type CreateDoubleEntryRequest struct {
	DebitAccountID  uuid.UUID           `json:"debit_account_id"`
	CreditAccountID uuid.UUID           `json:"credit_account_id"`
	Amount          int64               `json:"amount"`
	Currency        string              `json:"currency"`
	Description     string              `json:"description"`
	IdempotencyKey  string              `json:"idempotency_key"`
	CorrelationID   string              `json:"correlation_id"`
	CausationID     string              `json:"causation_id"`
	TransactionType TransactionType     `json:"transaction_type"`
	Lines           []DoubleEntryLine   `json:"lines,omitempty"`
}

type BalanceBreakdown struct {
	AccountID    uuid.UUID `json:"account_id"`
	Balance      int64     `json:"balance"`
	Pending      int64     `json:"pending"`
	Available    int64     `json:"available"`
	TotalDebits  int64     `json:"total_debits"`
	TotalCredits int64     `json:"total_credits"`
}

type BalanceIntegrityResult struct {
	AccountID       uuid.UUID `json:"account_id"`
	IsBalanced      bool      `json:"is_balanced"`
	ComputedBalance int64     `json:"computed_balance"`
	LatestBalance   int64     `json:"latest_balance"`
	TotalDebits     int64     `json:"total_debits"`
	TotalCredits    int64     `json:"total_credits"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (r *CreateDoubleEntryRequest) Validate() error {
	if r.Amount <= 0 {
		return ErrInvalidAmount
	}
	if r.IdempotencyKey == "" {
		return errors.New("idempotency_key is required")
	}
	if r.DebitAccountID == r.CreditAccountID {
		return errors.New("debit and credit accounts must be different")
	}
	if len(r.Lines) == 0 {
		r.Lines = []DoubleEntryLine{
			{AccountID: r.DebitAccountID, EntryType: EntryTypeDebit, Amount: r.Amount, Currency: r.Currency},
			{AccountID: r.CreditAccountID, EntryType: EntryTypeCredit, Amount: r.Amount, Currency: r.Currency},
		}
	}
	return r.ValidateLines()
}

func (r *CreateDoubleEntryRequest) ValidateLines() error {
	if len(r.Lines) < 2 {
		return errors.New("at least two lines required for double-entry")
	}

	var totalDebits, totalCredits int64
	var currency string

	for i, line := range r.Lines {
		if line.Amount <= 0 {
			return ErrInvalidAmount
		}
		if line.EntryType != EntryTypeDebit && line.EntryType != EntryTypeCredit {
			return errors.New("invalid entry type: must be DEBIT or CREDIT")
		}
		if i == 0 {
			currency = line.Currency
		} else if line.Currency != currency {
			return ErrInvalidCurrency
		}
		switch line.EntryType {
		case EntryTypeDebit:
			totalDebits += line.Amount
		case EntryTypeCredit:
			totalCredits += line.Amount
		}
	}

	if totalDebits != totalCredits {
		return ErrDoubleEntryMismatch
	}

	return nil
}

func ValidateDoubleEntry(debitEntries, creditEntries []*LedgerEntry) error {
	var totalDebits, totalCredits int64
	for _, e := range debitEntries {
		totalDebits += e.Amount
	}
	for _, e := range creditEntries {
		totalCredits += e.Amount
	}
	if totalDebits != totalCredits {
		return ErrDoubleEntryMismatch
	}
	return nil
}
