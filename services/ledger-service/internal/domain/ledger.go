package domain

import (
	"errors"
	"strings"
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

// Category is a user-facing spend category inferred from the transaction
// description at booking time (Monzo-style auto-categorisation). It is
// denormalised onto the ledger entry so the app can group/filter without a join.
type Category string

const (
	CategoryGroceries     Category = "GROCERIES"
	CategoryEatingOut     Category = "EATING_OUT"
	CategoryTransport     Category = "TRANSPORT"
	CategoryShopping      Category = "SHOPPING"
	CategoryBills         Category = "BILLS"
	CategoryEntertainment Category = "ENTERTAINMENT"
	CategoryTravel        Category = "TRAVEL"
	CategoryHealth        Category = "HEALTH"
	CategorySavings       Category = "SAVINGS"
	CategoryTransfers     Category = "TRANSFERS"
	CategoryIncome        Category = "INCOME"
	CategoryOther         Category = "OTHER"
)

// InferCategory maps a transaction description to a spend category using
// keyword matching over the (lowercased) description. Unknown descriptions
// fall back to OTHER; empty descriptions become TRANSFERS which is what most
// unlabelled internal movements are.
func InferCategory(description string) Category {
	d := strings.ToLower(description)
	if d == "" {
		return CategoryTransfers
	}
	rules := []struct {
		keywords []string
		category Category
	}{
		{[]string{"tesco", "sainsbury", "asda", "aldi", "lidl", "morrisons", "waitrose", "iceland", "co-op", "grocery", "market"}, CategoryGroceries},
		{[]string{"cafe", "coffee", "restaurant", "pizza", "burger", "kfc", "mcdonald", "nando", "starbucks", "costa", "pret", "deliveroo", "just eat", "uber eats", "takeaway", "bar ", "pub "}, CategoryEatingOut},
		{[]string{"uber", "lyft", "tfl", "transport for london", "bus", "train", "tube", "national rail", "shell", "bp ", "esso", "fuel", "petrol", "parking", "citymapper", "bolt"}, CategoryTransport},
		{[]string{"amazon", "ebay", "asos", "zara", "h&m", "primark", "argos", "ikea", "john lewis", "boots", "superdrug", "shop", "store"}, CategoryShopping},
		{[]string{"bill", "electric", "gas ", "water ", "council tax", "broadband", "internet", "vodafone", "o2 ", "ee ", "three ", "sky ", "virgin media", "insurance", "rent", "mortgage", "council", "tv license", "tv licence"}, CategoryBills},
		{[]string{"netflix", "spotify", "disney", "prime video", "youtube premium", "cinema", "odeon", "steam", "playstation", "xbox", "apple.com", "app store", "google play", "game", "theatre", "concert"}, CategoryEntertainment},
		{[]string{"hotel", "airbnb", "booking.com", "expedia", "ryanair", "easyjet", "british airways", "flight", "airline", "airport", "holiday"}, CategoryTravel},
		{[]string{"pharmacy", "dentist", "doctor", "hospital", "clinic", "optician", "gym", "fitness", "puregym", "nhs"}, CategoryHealth},
		{[]string{"pot deposit", "pot-deposit", "roundup", "round-up", "savings", "interest"}, CategorySavings},
		{[]string{"salary", "payroll", "wages", "refund", "interest payment"}, CategoryIncome},
		{[]string{"transfer", "payment to", "sent to", "pot-withdraw"}, CategoryTransfers},
	}
	for _, rule := range rules {
		for _, kw := range rule.keywords {
			if strings.Contains(d, kw) {
				return rule.category
			}
		}
	}
	return CategoryOther
}

var (
	ErrInsufficientFunds    = errors.New("insufficient funds")
	// ErrAccountLocked refuses a money-OUT booking against an account in
	// emergency lockdown. Money-IN credits are never blocked.
	ErrAccountLocked = errors.New("account is locked: outbound movement is temporarily disabled")
	// ErrAccountIntegrityViolation refuses any booking against an account whose
	// ledger chain the invariant monitor has frozen: state cannot be trusted
	// until an engineer repairs the entries and clears the guard.
	ErrAccountIntegrityViolation = errors.New("account is frozen by the ledger invariant monitor: pending integrity repair")
	ErrReservationNotFound  = errors.New("reservation not found")
	ErrReservationNotActive = errors.New("reservation is not active")
	ErrReservationExpired   = errors.New("reservation has expired")
	ErrTransactionNotFound  = errors.New("transaction not found")
	ErrAccountNotFound      = errors.New("account not found")
	ErrIdempotencyConflict  = errors.New("idempotency key already used with different request")
	ErrDoubleEntryMismatch  = errors.New("total debits do not equal total credits")
	ErrInvalidAmount        = errors.New("amount must be positive")
	ErrInvalidCurrency      = errors.New("currency mismatch between debit and credit entries")
	ErrDuplicateEntry       = errors.New("duplicate entry detected")
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
	Category       Category       `json:"category,omitempty"`
	Note           string         `json:"note,omitempty"`
	CorrelationID  string         `json:"correlation_id"`
	CausationID    string         `json:"causation_id"`
	EventVersion   int            `json:"event_version"`
	CreatedAt      time.Time      `json:"created_at"`
}

// TransactionNote is the user's annotation on a ledger entry, stored outside
// the append-only ledger (see transaction_notes).
type TransactionNote struct {
	EntryID   uuid.UUID `json:"entry_id"`
	UserID    uuid.UUID `json:"user_id"`
	Note      string    `json:"note"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EntryFilter narrows GetEntries results. Zero-value fields mean "no filter".
type EntryFilter struct {
	Query    string // substring match on description or note (case-insensitive)
	Category string // exact category match ("" = all)
	Type     string // DEBIT or CREDIT ("" = both)
	Limit    int
}

// Matches reports whether a ledger entry satisfies the filter. It is exported
// so both the Cassandra repository and in-memory test mocks share one
// definition of filtering.
func (f EntryFilter) Matches(e *LedgerEntry) bool {
	if f.Category != "" && string(e.Category) != strings.ToUpper(f.Category) {
		return false
	}
	switch strings.ToUpper(f.Type) {
	case "DEBIT":
		if e.EntryType != EntryTypeDebit {
			return false
		}
	case "CREDIT":
		if e.EntryType != EntryTypeCredit {
			return false
		}
	}
	if f.Query != "" {
		q := strings.ToLower(f.Query)
		if !strings.Contains(strings.ToLower(e.Description), q) &&
			!strings.Contains(strings.ToLower(e.Note), q) {
			return false
		}
	}
	return true
}

type LedgerTransaction struct {
	TransactionID   uuid.UUID         `json:"transaction_id"`
	IdempotencyKey  string            `json:"idempotency_key"`
	TransactionType TransactionType   `json:"transaction_type"`
	Status          TransactionStatus `json:"status"`
	TotalAmount     int64             `json:"total_amount"`
	Currency        string            `json:"currency"`
	Description     string            `json:"description"`
	CorrelationID   string            `json:"correlation_id"`
	CausationID     string            `json:"causation_id"`
	EventVersion    int               `json:"event_version"`
	CreatedAt       time.Time         `json:"created_at"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty"`
}

type Reservation struct {
	ReservationID uuid.UUID         `json:"reservation_id"`
	AccountID     uuid.UUID         `json:"account_id"`
	TransactionID uuid.UUID         `json:"transaction_id"`
	Amount        int64             `json:"amount"`
	Currency      string            `json:"currency"`
	Status        ReservationStatus `json:"status"`
	ExpiresAt     time.Time         `json:"expires_at"`
	CreatedAt     time.Time         `json:"created_at"`
	ReleasedAt    *time.Time        `json:"released_at,omitempty"`
	SettledAt     *time.Time        `json:"settled_at,omitempty"`
}

type DoubleEntryLine struct {
	AccountID uuid.UUID `json:"account_id"`
	EntryType EntryType `json:"entry_type"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
}

type CreateDoubleEntryRequest struct {
	DebitAccountID  uuid.UUID         `json:"debit_account_id"`
	CreditAccountID uuid.UUID         `json:"credit_account_id"`
	Amount          int64             `json:"amount"`
	Currency        string            `json:"currency"`
	Description     string            `json:"description"`
	IdempotencyKey  string            `json:"idempotency_key"`
	CorrelationID   string            `json:"correlation_id"`
	CausationID     string            `json:"causation_id"`
	TransactionType TransactionType   `json:"transaction_type"`
	Lines           []DoubleEntryLine `json:"lines,omitempty"`
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

// ── Ledger invariant monitor ────────────────────────────────────────────────

// IntegrityEventType is the kind of monitor event.
type IntegrityEventType string

const (
	IntegrityEventViolation IntegrityEventType = "VIOLATION"
	IntegrityEventCleared   IntegrityEventType = "CLEARED"
)

// IntegrityGuard is the per-account freeze row the monitor sets when it
// detects a violation and clears once the ledger has been repaired.
type IntegrityGuard struct {
	AccountID  uuid.UUID `json:"account_id"`
	Frozen     bool      `json:"frozen"`
	Reason     string    `json:"reason,omitempty"`
	IncidentID uuid.UUID `json:"incident_id,omitempty"`
	DetectedAt time.Time `json:"detected_at"`
}

// IntegrityEvent is one entry in an account's integrity trail.
type IntegrityEvent struct {
	AccountID  uuid.UUID          `json:"account_id"`
	EventID    uuid.UUID          `json:"event_id"`
	EventType  IntegrityEventType `json:"event_type"`
	Message    string             `json:"message"`
	Detail     string             `json:"detail,omitempty"`
	DetectedAt time.Time          `json:"detected_at"`
	ClearedAt  *time.Time         `json:"cleared_at,omitempty"`
}

// IntegrityScanResult summarises one account's monitor sweep.
type IntegrityScanResult struct {
	AccountID          uuid.UUID `json:"account_id"`
	EntriesChecked     int       `json:"entries_checked"`
	ChainContinuous    bool      `json:"chain_continuous"`
	RecomputeBalanced  bool      `json:"recompute_balanced"`
	Violation          bool      `json:"violation"`
	FirstBadEntryID    uuid.UUID `json:"first_bad_entry_id,omitempty"`
	Message            string    `json:"message,omitempty"`
	GuardApplied       bool      `json:"guard_applied"`
	IncidentCreated    bool      `json:"incident_created"`
}

// IntegrityScanSummary is the aggregate of a full sweep.
type IntegrityScanSummary struct {
	AccountsScanned int                     `json:"accounts_scanned"`
	Violations      int                     `json:"violations"`
	GuardsApplied   int                     `json:"guards_applied"`
	IncidentsRaised int                     `json:"incidents_raised"`
	CheckedAt       time.Time               `json:"checked_at"`
	Results         []*IntegrityScanResult   `json:"results"`
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

// TransferRequest books a funds movement between two accounts as one balanced
// double-entry transaction with an availability guarantee on the source.
type TransferRequest struct {
	IdempotencyKey       string    `json:"idempotency_key"`
	SourceAccountID      uuid.UUID `json:"source_account_id"`
	DestinationAccountID uuid.UUID `json:"destination_account_id"`
	Amount               int64     `json:"amount"`
	Currency             string    `json:"currency"`
	Description          string    `json:"description"`
}

func (r *TransferRequest) Validate() error {
	if r.IdempotencyKey == "" {
		return errors.New("idempotency_key is required")
	}
	if r.Amount <= 0 {
		return ErrInvalidAmount
	}
	if r.Currency == "" {
		return errors.New("currency is required")
	}
	if r.SourceAccountID == uuid.Nil || r.DestinationAccountID == uuid.Nil {
		return errors.New("source and destination accounts are required")
	}
	if r.SourceAccountID == r.DestinationAccountID {
		return errors.New("source and destination accounts must be different")
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
