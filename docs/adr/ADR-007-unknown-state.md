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

### Both Money Paths (added after the same gap turned up twice)

There are two ways money leaves an account here, and both can be answered
indeterminately:

1. **An external rail** (card, bank transfer out). The provider may time out. The
   payment goes UNKNOWN, the authorisation hold is deliberately *not* released,
   and reconciliation decides — see ADR-018 for how the case is filed, swept and
   written back.
2. **The internal ledger booking** (`transfer-service`, account to account). The
   booking call may time out *after* the ledger committed. The transfer goes
   UNKNOWN rather than FAILED, and the recovery is a retry with the **same**
   ledger idempotency key: the ledger is exactly-once on that key, so the retry
   either settles the booking that already happened or books the one that never
   did. It cannot move the money twice.

The invariant in both paths is the same: **an indeterminate answer must never be
recorded as a definite one.** Marking a timeout as FAILED tells the customer the
money did not move when it may have, and — worse — makes the retry path refuse to
resume, because a failed movement is not resumable.

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
