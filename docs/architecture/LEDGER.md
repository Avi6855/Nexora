# Ledger Design

## Overview

Nexora's ledger implements double-entry bookkeeping to ensure financial accuracy and auditability.

## Double-Entry Design

### Core Principle

Every financial transaction creates at least two entries:
- **Debit**: Money leaving an account (decreases balance)
- **Credit**: Money entering an account (increases balance)

### Invariant

```
Sum(Debits) == Sum(Credits)
```

### Example

Transfer £100 from Account A to Account B:

```
Account A (Debit):  -£100 (OUTBOUND)
Account B (Credit): +£100 (INBOUND)
```

## Money Representation

### Minor Units

All amounts stored as `int64` minor units (pence, cents):

```go
type Money struct {
    Amount   int64  // Minor units (e.g., 1050 = £10.50)
    Currency string // ISO 4217 code
}
```

### Currency Handling

| Currency | Minor Units | Example |
|----------|-------------|---------|
| GBP | 2 | 1050 = £10.50 |
| USD | 2 | 2500 = $25.00 |
| EUR | 2 | 750 = €7.50 |
| JPY | 0 | 1000 = ¥1000 |
| KRW | 0 | 5000 = ₩5000 |

### Arithmetic Operations

```go
func (m Money) Add(other Money) (Money, error) {
    if m.Currency != other.Currency {
        return Money{}, ErrDifferentCurrency
    }
    // Overflow protection
    if m.Amount > 0 && other.Amount > math.MaxInt64-m.Amount {
        return Money{}, ErrOverflow
    }
    return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}, nil
}
```

## Ledger Entry Structure

```go
type LedgerEntry struct {
    EntryID        uuid.UUID      // Unique entry identifier
    AccountID      uuid.UUID      // Account affected
    TransactionID  uuid.UUID      // Transaction this entry belongs to
    EntryType      EntryType      // DEBIT or CREDIT
    EntryDirection EntryDirection // INBOUND or OUTBOUND
    Amount         int64          // Amount in minor units
    Currency       string         // ISO 4217 currency code
    BalanceBefore  int64          // Balance before this entry
    BalanceAfter   int64          // Balance after this entry
    Description    string         // Human-readable description
    CorrelationID  string         // Request ID for tracing
    CausationID    string         // Event ID that caused this
    EventVersion   int            // Schema version
    CreatedAt      time.Time      // When entry was created
}
```

## Transaction Structure

```go
type LedgerTransaction struct {
    TransactionID  uuid.UUID         // Unique transaction ID
    IdempotencyKey string            // For idempotent operations
    TransactionType TransactionType  // PAYMENT, TRANSFER, etc.
    Status         TransactionStatus // PENDING, COMPLETED, FAILED
    TotalAmount    int64             // Total transaction amount
    Currency       string            // Currency code
    Description    string            // Description
    CorrelationID  string            // Request tracing ID
    CausationID    string            // Event lineage
    EventVersion   int               // Schema version
    CreatedAt      time.Time         // When created
    CompletedAt    *time.Time        // When completed
}
```

## Invariants

### 1. Double-Entry Balance

```go
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
```

### 2. Balance Consistency

```go
func (s *LedgerService) VerifyBalanceIntegrity(ctx context.Context, accountID uuid.UUID) (*BalanceIntegrityResult, error) {
    totalDebits, _ := s.ledgerRepo.SumDebitsByAccount(ctx, accountID)
    totalCredits, _ := s.ledgerRepo.SumCreditsByAccount(ctx, accountID)
    computedBalance := totalCredits - totalDebits

    latestBalance, _ := s.ledgerRepo.GetLatestBalance(ctx, accountID)

    return &BalanceIntegrityResult{
        IsBalanced:      computedBalance == latestBalance,
        ComputedBalance: computedBalance,
        LatestBalance:   latestBalance,
    }, nil
}
```

### 3. No Negative Balance

```go
func (s *LedgerService) ReserveFunds(ctx context.Context, accountID uuid.UUID, amount int64, ...) (*Reservation, error) {
    available, _ := s.GetAvailableBalance(ctx, accountID)
    if available < amount {
        return nil, ErrInsufficientFunds
    }
    // Create reservation
}
```

## Reservation Engine

### Purpose

Reservations prevent double spending by temporarily locking funds:

1. **Reserve**: Lock funds before payment processing
2. **Settle**: Convert reservation to actual debit on success
3. **Release**: Unlock funds on failure or cancellation

### Reservation Structure

```go
type Reservation struct {
    ReservationID uuid.UUID          // Unique ID
    AccountID     uuid.UUID          // Account to reserve from
    TransactionID uuid.UUID          // Associated transaction
    Amount        int64              // Amount to reserve
    Currency      string             // Currency
    Status        ReservationStatus  // ACTIVE, RELEASED, SETTLED, EXPIRED
    ExpiresAt     time.Time          // When reservation expires
    CreatedAt     time.Time          // When created
    ReleasedAt    *time.Time         // When released
    SettledAt     *time.Time         // When settled
}
```

### Reservation Lifecycle

```
┌─────────────┐
│   CREATED   │
└──────┬──────┘
       │
       v
┌─────────────┐
│  RESERVE    │◄─────────────────────────────────────┐
│  (Lock)     │                                       │
└──────┬──────┘                                       │
       │                                              │
       ├──────────────────────────────────────────┐   │
       │                                          │   │
       v                                          v   │
┌─────────────┐                            ┌───────────┐
│  PROCESSING │                            │  TIMEOUT  │
│  (Provider) │                            │  (Auto)   │
└──────┬──────┘                            └─────┬─────┘
       │                                         │
       ├──────────────┬──────────────────────────┘
       │              │
       v              v
┌─────────────┐  ┌─────────────┐
│  CONFIRMED  │  │   FAILED    │
└──────┬──────┘  └──────┬──────┘
       │                │
       v                v
┌─────────────┐  ┌─────────────┐
│  SETTLED    │  │  RELEASE    │
│  (Debit)    │  │  (Unlock)   │
└─────────────┘  └─────────────┘
```

### Concurrent Reservation Protection

```go
func (s *LedgerService) ReserveFunds(ctx context.Context, accountID uuid.UUID, amount int64, ...) (*Reservation, error) {
    // Check available balance
    available, _ := s.GetAvailableBalance(ctx, accountID)
    if available < amount {
        return nil, ErrInsufficientFunds
    }

    // Create reservation (atomic operation)
    applied, _ := s.ledgerRepo.CreateReservation(ctx, reservation)
    if !applied {
        return nil, ErrDuplicateEntry
    }

    return reservation, nil
}
```

## Balance Reconstruction

### From Entries

```go
func (s *LedgerService) GetAccountBalance(ctx context.Context, accountID uuid.UUID) (*BalanceBreakdown, error) {
    latestBalance, _ := s.ledgerRepo.GetLatestBalance(ctx, accountID)
    pending, _ := s.ledgerRepo.SumActiveReservationAmounts(ctx, accountID)
    totalDebits, _ := s.ledgerRepo.SumDebitsByAccount(ctx, accountID)
    totalCredits, _ := s.ledgerRepo.SumCreditsByAccount(ctx, accountID)

    return &BalanceBreakdown{
        Balance:      latestBalance,
        Pending:      pending,
        Available:    latestBalance - pending,
        TotalDebits:  totalDebits,
        TotalCredits: totalCredits,
    }, nil
}
```

### From Events (Replay)

```go
func (s *ReplayService) ReconstructBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
    entries, _ := s.eventStore.GetEventsByAggregate(accountID)
    var balance int64

    for _, event := range entries {
        switch event.Type {
        case "ledger.entry.created":
            var entry LedgerEntry
            json.Unmarshal(event.Payload, &entry)
            if entry.EntryType == "CREDIT" {
                balance += entry.Amount
            } else {
                balance -= entry.Amount
            }
        }
    }

    return balance, nil
}
```

## Audit Trail

### Complete History

Every entry provides complete audit trail:

1. **Who**: Account ID, User ID
2. **What**: Amount, Currency, Type
3. **When**: Timestamp, Entry ID
4. **Why**: Description, Correlation ID
5. **How**: Transaction ID, Event Version

### Compliance

- Immutable entries (no updates/deletes)
- Complete balance history
- Event sourcing for state reconstruction
- Cryptographic integrity (hash chains)
