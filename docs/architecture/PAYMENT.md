# Payment Flow

## Overview

Nexora's payment service implements a state machine with saga pattern for reliable payment processing.

## State Machine

### States

| State | Description |
|-------|-------------|
| CREATED | Payment created, awaiting authorization |
| AUTHORIZED | Payment authorized, ready for processing |
| PROCESSING | Payment being processed by provider |
| UNKNOWN | Provider response uncertain |
| CONFIRMED | Provider confirmed payment |
| SETTLED | Payment settled, funds transferred |
| FAILED | Payment failed |
| REVERSED | Payment reversed/refunded |
| CANCELLED | Payment cancelled by user |

### Valid Transitions

```
CREATED → AUTHORIZED, FAILED, REVERSED, CANCELLED
AUTHORIZED → PROCESSING, FAILED, REVERSED, CANCELLED
PROCESSING → CONFIRMED, FAILED, UNKNOWN
UNKNOWN → CONFIRMED, FAILED
CONFIRMED → SETTLED, REVERSED
SETTLED → (terminal)
FAILED → (terminal)
REVERSED → (terminal)
CANCELLED → (terminal)
```

### State Diagram

```
                                    ┌───────────┐
                                    │  CREATED  │
                                    └─────┬─────┘
                                          │
            ┌─────────────────────────────┼─────────────────────────────┐
            │                             │                             │
            v                             v                             v
    ┌───────────────┐            ┌───────────────┐            ┌───────────────┐
    │  CANCELLED    │            │  AUTHORIZED   │            │    FAILED     │
    └───────────────┘            └───────┬───────┘            └───────────────┘
                                         │
                                         v
                                 ┌───────────────┐
                                 │  PROCESSING   │
                                 └───────┬───────┘
                                         │
            ┌────────────────────────────┼────────────────────────────┐
            │                            │                            │
            v                            v                            v
    ┌───────────────┐            ┌───────────────┐            ┌───────────────┐
    │    FAILED     │            │   CONFIRMED   │            │    UNKNOWN    │
    └───────────────┘            └───────┬───────┘            └───────┬───────┘
                                         │                            │
                                         │                            │
                                         v                            v
                                 ┌───────────────┐            ┌───────────────┐
                                 │    SETTLED    │            │   CONFIRMED   │
                                 └───────────────┘            └───────────────┘
```

## Saga Pattern

### Payment Creation Saga

```go
func (s *PaymentSaga) ExecuteCreatePayment(ctx context.Context, req *CreatePaymentRequest, correlationID string) (*Payment, error) {
    // Step 1: Check idempotency
    existing, _ := s.paymentRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
    if existing != nil {
        return existing, nil
    }

    // Step 2: Create payment
    payment := NewPayment(req)
    s.paymentRepo.Create(ctx, payment)

    // Step 3: Publish event
    s.eventPublisher.PublishPaymentEvent(ctx, EventTypePaymentCreated, payload, correlationID)

    return payment, nil
}
```

### Payment Processing Saga

```go
func (s *PaymentSaga) ExecuteProcessPayment(ctx context.Context, paymentID string, correlationID string) (*Payment, error) {
    // Step 1: Validate state
    payment, _ := s.paymentRepo.GetByID(ctx, id)
    if payment.State != PaymentStateAuthorized {
        return nil, fmt.Errorf("cannot process payment in state %s", payment.State)
    }

    // Step 2: Transition to PROCESSING
    payment.TransitionTo(PaymentStateProcessing)
    s.paymentRepo.Update(ctx, payment)

    // Step 3: Call provider
    result, err := s.provider.ProcessPayment(ctx, payReq)

    // Step 4: Handle result
    s.handleProviderResult(ctx, payment, result, correlationID)

    return payment, nil
}
```

### Provider Result Handling

```go
func (s *PaymentSaga) handleProviderResult(ctx context.Context, payment *Payment, result *PaymentResult, correlationID string) {
    payment.ProviderResponse = &ProviderResponse{
        ProviderID:     result.ProviderID,
        TransactionRef: result.TransactionRef,
        Status:         result.Status,
    }

    switch result.Status {
    case "SUCCESS":
        s.confirmAndSettle(ctx, payment, correlationID)
    case "FAILED":
        s.failPayment(ctx, payment, result.Message, correlationID)
    case "UNKNOWN":
        s.markUnknown(ctx, payment, correlationID)
    default:
        s.failPayment(ctx, payment, "unexpected status", correlationID)
    }
}
```

## Idempotency

### Implementation

```go
func (s *PaymentSaga) ExecuteCreatePayment(ctx context.Context, req *CreatePaymentRequest, correlationID string) (*Payment, error) {
    // Check if already processed
    existing, _ := s.paymentRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
    if existing != nil {
        return existing, nil
    }

    // Create payment with idempotency key
    payment := NewPayment(req)
    s.paymentRepo.Create(ctx, payment)

    return payment, nil
}
```

### Benefits

1. **Safe retries**: Client can retry without duplicate processing
2. **Network resilience**: Handles network partitions gracefully
3. **Consistent state**: Same result for same request

## UNKNOWN State

### Purpose

The UNKNOWN state handles uncertain provider responses:

```go
func (s *PaymentSaga) markUnknown(ctx context.Context, payment *Payment, correlationID string) {
    payment.TransitionTo(PaymentStateUnknown)
    s.paymentRepo.Update(ctx, payment)

    // Publish event for reconciliation
    s.eventPublisher.PublishPaymentEvent(ctx, EventTypePaymentUnknown, payload, correlationID)
}
```

### Resolution

Unknown payments are resolved via:

1. **Provider callback**: Provider confirms/rejects later
2. **Reconciliation**: Periodic reconciliation job
3. **Manual review**: Support team reviews

### Reconciliation Flow

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   UNKNOWN   │────▶│  RECONCILE  │────▶│  CONFIRMED  │
│   PAYMENT   │     │    JOB      │     │  or FAILED  │
└─────────────┘     └─────────────┘     └─────────────┘
```

## Reconciliation

### Daily Reconciliation

```go
func (s *ReconciliationService) ReconcilePayments(ctx context.Context, date time.Time) error {
    // 1. Get all UNKNOWN payments for date
    payments, _ := s.paymentRepo.GetByStateAndDate(ctx, PaymentStateUnknown, date)

    // 2. Check each with provider
    for _, payment := range payments {
        status, _ := s.provider.CheckPaymentStatus(ctx, payment.PaymentID.String())

        switch status {
        case "SUCCESS":
            payment.TransitionTo(PaymentStateConfirmed)
            s.settlePayment(ctx, payment)
        case "FAILED":
            payment.TransitionTo(PaymentStateFailed)
            s.releaseReservation(ctx, payment)
        }
    }

    return nil
}
```

### Ledger Reconciliation

```go
func (s *ReconciliationService) ReconcileLedger(ctx context.Context, accountID uuid.UUID) error {
    // 1. Get computed balance
    result, _ := s.ledgerService.VerifyBalanceIntegrity(ctx, accountID)

    // 2. Check if balanced
    if !result.IsBalanced {
        // Alert and investigate
        s.alertService.SendAlert(ctx, "Ledger imbalance", map[string]interface{}{
            "account_id":       accountID,
            "computed_balance": result.ComputedBalance,
            "latest_balance":   result.LatestBalance,
        })
    }

    return nil
}
```

## Error Handling

### Provider Errors

```go
func (s *PaymentSaga) handleProviderError(ctx context.Context, payment *Payment, err error, correlationID string) {
    if ctx.Err() != nil || err == context.DeadlineExceeded || err == context.Canceled {
        // Timeout → UNKNOWN state
        s.markUnknown(ctx, payment, correlationID)
        return
    }

    // Other errors → FAILED state
    s.failPayment(ctx, payment, err.Error(), correlationID)
}
```

### Retry Strategy

```go
func (s *PaymentSaga) retryWithBackoff(ctx context.Context, fn func() error, maxRetries int) error {
    for i := 0; i < maxRetries; i++ {
        if err := fn(); err != nil {
            if i == maxRetries-1 {
                return err
            }
            time.Sleep(time.Duration(1<<uint(i)) * time.Second)
            continue
        }
        return nil
    }
    return nil
}
```

## Observability

### Metrics

```go
// Payment processing metrics
paymentCreatedCounter := prometheus.NewCounter(prometheus.CounterOpts{
    Name: "nexora_payments_created_total",
    Help: "Total payments created",
})

paymentProcessingDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
    Name:    "nexora_payments_processing_duration_seconds",
    Help:    "Payment processing duration",
    Buckets: prometheus.ExponentialBuckets(0.01, 2, 15),
})
```

### Distributed Tracing

```go
func (s *PaymentSaga) ExecuteProcessPayment(ctx context.Context, paymentID string, correlationID string) (*Payment, error) {
    ctx, span := tracer.Start(ctx, "payment.process")
    defer span.End()

    span.SetAttributes(attribute.String("payment_id", paymentID))
    span.SetAttributes(attribute.String("correlation_id", correlationID))

    // Process payment...

    return payment, nil
}
```

## Best Practices

1. **Idempotency**: Always use idempotency keys
2. **State validation**: Validate state before transitions
3. **Event sourcing**: Publish events for all state changes
4. **Timeout handling**: Handle provider timeouts gracefully
5. **Reconciliation**: Regular reconciliation for UNKNOWN payments
6. **Monitoring**: Track payment metrics and alerts
7. **Audit trail**: Complete audit trail for compliance
