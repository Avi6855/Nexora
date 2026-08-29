# ADR-003: Double-Entry Ledger

## Status

Accepted

## Context

Nexora requires financial accuracy and auditability for all monetary transactions. The system must maintain a complete audit trail and ensure balance consistency.

## Decision

We will implement a double-entry bookkeeping system where every financial transaction creates balanced debit and credit entries.

## Alternatives

### Single-Entry Ledger
- **Pros**: Simpler implementation, less storage
- **Cons**: No built-in balance verification, harder to audit

### Event Sourcing Only
- **Pros**: Complete event history, temporal queries
- **Cons**: Complex state reconstruction, performance issues

### Balance-Only Storage
- **Pros**: Simple queries, fast reads
- **Cons**: No audit trail, hard to debug inconsistencies

## Trade-offs

### Gained
- Built-in balance verification (debits == credits)
- Complete audit trail
- Easy debugging of financial discrepancies
- Compliance with accounting standards
- Temporal queries on balance history

### Lost
- Storage efficiency (two entries per transaction)
- Query simplicity (must aggregate entries)
- Implementation complexity

## Consequences

### Positive
- Financial accuracy is guaranteed by design
- Complete audit trail for compliance
- Easy to detect and fix discrepancies
- Can reconstruct any account's balance history

### Negative
- Must maintain two entries per transaction
- Must aggregate entries for current balance
- More complex queries for reporting

## Implementation Notes

### Entry Structure
```go
type LedgerEntry struct {
    EntryID        uuid.UUID
    AccountID      uuid.UUID
    TransactionID  uuid.UUID
    EntryType      EntryType      // DEBIT or CREDIT
    EntryDirection EntryDirection // INBOUND or OUTBOUND
    Amount         int64          // Minor units
    Currency       string
    BalanceBefore  int64
    BalanceAfter   int64
}
```

### Validation
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
