# ADR-006: Saga Pattern

## Status

Accepted

## Context

Nexora requires distributed transactions across multiple services (payment, ledger, notification). Traditional two-phase commit is not suitable for microservices.

## Decision

We will implement the saga pattern for distributed transactions, using compensating actions for rollback.

## Alternatives

### Two-Phase Commit
- **Pros**: Strong consistency
- **Cons**: Performance overhead, tight coupling

### Event Sourcing
- **Pros**: Complete audit trail
- **Cons**: Complex implementation

### Choreography
- **Pros**: Loose coupling
- **Cons**: Hard to track, complex error handling

## Trade-offs

### Gained
- Distributed transactions without 2PC
- Loose coupling between services
- Better performance
- Fault tolerance

### Lost
- Strong consistency
- Simple rollback
- Easy debugging

## Consequences

### Positive
- Services remain loosely coupled
- Better performance than 2PC
- Can handle partial failures
- Compensating actions for rollback

### Negative
- Complex error handling
- Hard to track saga progress
- Must implement compensating actions

## Implementation Notes

### Saga Steps
```go
type SagaStep func(ctx context.Context, payment *Payment) error
type CompensationStep func(ctx context.Context, payment *Payment) error

type PaymentSaga struct {
    steps           []SagaStep
    compensations   []CompensationStep
}
```

### Execution
```go
func (s *PaymentSaga) Execute(ctx context.Context, payment *Payment) error {
    for i, step := range s.steps {
        if err := step(ctx, payment); err != nil {
            // Compensate previous steps
            for j := i - 1; j >= 0; j-- {
                s.compensations[j](ctx, payment)
            }
            return err
        }
    }
    return nil
}
```
