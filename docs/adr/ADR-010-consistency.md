# ADR-010: Consistency Levels

## Status

Accepted

## Context

Nexora requires different consistency levels for different operations. Financial data needs strong consistency, while non-critical data can tolerate eventual consistency.

## Decision

We will implement tunable consistency levels using Cassandra's consistency levels and Kafka's ordering guarantees.

## Alternatives

### Strong Consistency Only
- **Pros**: Simple, predictable
- **Cons**: Performance overhead, availability issues

### Eventual Consistency Only
- **Pros**: High availability, performance
- **Cons**: Data inconsistency, complex logic

### Custom Consistency
- **Pros**: Full control
- **Cons**: Complex implementation

## Trade-offs

### Gained
- Flexible consistency per operation
- Better performance for non-critical data
- High availability for reads
- Strong consistency for financial data

### Lost
- Uniform consistency
- Simple implementation
- Predictable behavior

## Consequences

### Positive
- Financial data is strongly consistent
- Non-critical data is eventually consistent
- Better performance and availability
- Flexible per operation

### Negative
- Must manage multiple consistency levels
- Complex application logic
- Harder to debug

## Implementation Notes

### Write Consistency
```go
// Financial writes use LOCAL_QUORUM
INSERT INTO nexora.ledger_entries (...)
VALUES (...)
USING CONSISTENCY LOCAL_QUORUM;

// Non-critical writes use ONE
INSERT INTO nexora.audit_logs (...)
VALUES (...)
USING CONSISTENCY ONE;
```

### Read Consistency
```go
// Balance queries use LOCAL_QUORUM
SELECT balance FROM nexora.accounts
WHERE account_id = ?
USING CONSISTENCY LOCAL_QUORUM;

// Historical reads use ONE
SELECT * FROM nexora.ledger_entries
WHERE account_id = ?
USING CONSISTENCY ONE;
```

### Kafka Ordering
```go
// Partition by aggregate ID for ordering
key := payment.PaymentID.String()
producer.SendMessage(ctx, topic, key, event)
```
