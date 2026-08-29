# ADR-004: Idempotency

## Status

Accepted

## Context

Nexora must handle network failures, retries, and duplicate requests without creating duplicate transactions or inconsistent state.

## Decision

We will implement idempotency keys for all operations that modify state, ensuring that repeated requests with the same key produce the same result.

## Alternatives

### Client-Side Deduplication
- **Pros**: Server remains simple
- **Cons**: Unreliable, depends on client implementation

### Database Constraints
- **Pros**: Strong guarantee, database-level
- **Cons**: Limited flexibility, complex queries

### Token-Based Idempotency
- **Pros**: Simple implementation
- **Cons**: Requires token management, storage overhead

## Trade-offs

### Gained
- Safe retries without duplicate processing
- Network resilience
- Consistent state across failures
- Simple client retry logic

### Lost
- Additional storage for idempotency records
- Complexity in idempotency key management
- TTL management for old records

## Consequences

### Positive
- Clients can retry safely
- Network partitions don't cause duplicates
- Simple retry logic for mobile clients
- Better user experience during failures

### Negative
- Must store idempotency records
- Must manage TTL for records
- Must handle key collisions

## Implementation Notes

### Key Generation
```go
func GenerateKey(parts ...string) string {
    h := sha256.New()
    for _, p := range parts {
        h.Write([]byte(p))
        h.Write([]byte{0})
    }
    return hex.EncodeToString(h.Sum(nil))
}
```

### Usage
```go
func (s *PaymentSaga) ExecuteCreatePayment(ctx context.Context, req *CreatePaymentRequest, correlationID string) (*Payment, error) {
    existing, _ := s.paymentRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
    if existing != nil {
        return existing, nil
    }
    // Create payment...
}
```
