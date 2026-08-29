# ADR-007: UNKNOWN State

## Status

Accepted

## Context

Payment providers may return uncertain responses due to timeouts, network issues, or partial failures. Nexora must handle these cases gracefully.

## Decision

We will implement an UNKNOWN payment state for uncertain provider responses, with reconciliation to resolve them.

## Alternatives

### Fail Immediately
- **Pros**: Simple implementation
- **Cons**: May incorrectly fail successful payments

### Succeed Immediately
- **Pros**: Better user experience
- **Cons**: May incorrectly succeed failed payments

### Retry Indefinitely
- **Pros**: Eventually consistent
- **Cons**: Resource waste, poor UX

## Trade-offs

### Gained
- Graceful handling of uncertain states
- Automatic reconciliation
- No incorrect state transitions
- Better user experience

### Lost
- Immediate resolution
- Simple state machine
- Predictable behavior

## Consequences

### Positive
- Uncertain responses are handled gracefully
- Reconciliation ensures eventual consistency
- No incorrect state transitions
- Better user experience during failures

### Negative
- Additional UNKNOWN state
- Must implement reconciliation
- Users may see uncertain status

## Implementation Notes

### State Transition
```go
func (s *PaymentSaga) markUnknown(ctx context.Context, payment *Payment, correlationID string) {
    payment.TransitionTo(PaymentStateUnknown)
    s.paymentRepo.Update(ctx, payment)
    s.eventPublisher.PublishPaymentEvent(ctx, EventTypePaymentUnknown, payload, correlationID)
}
```

### Reconciliation
```go
func (s *ReconciliationService) ReconcilePayments(ctx context.Context, date time.Time) error {
    payments, _ := s.paymentRepo.GetByStateAndDate(ctx, PaymentStateUnknown, date)
    for _, payment := range payments {
        status, _ := s.provider.CheckPaymentStatus(ctx, payment.PaymentID.String())
        // Resolve based on provider status
    }
    return nil
}
```
