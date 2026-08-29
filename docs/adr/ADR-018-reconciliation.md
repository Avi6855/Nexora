# ADR-018: Payment Reconciliation

## Status

Accepted

## Context

Nexora interacts with external payment providers (banking rails, card networks, payment processors) whose responses may be delayed, inconsistent, or lost. Internal payment state may diverge from external state, leading to UNKNOWN payments where the outcome is uncertain. We need automated reconciliation to detect and resolve these discrepancies.

## Decision

We will implement a reconciliation service that continuously compares internal payment state against external provider state, with automated resolution for common discrepancies and escalation for complex cases.

## Alternatives

### Ignore Discrepancies
- **Pros**: Simple, no additional infrastructure
- **Cons**: Financial losses, regulatory violations, customer complaints

### Manual Reconciliation Only
- **Pros**: Human judgment for complex cases
- **Cons**: Slow, error-prone, doesn't scale, staff fatigue

### Real-Time Webhook Only
- **Pros**: Immediate state updates
- **Cons**: Provider reliability varies, no guaranteed delivery, replay impossible

## Trade-offs

### Gained
- Automated detection of payment state mismatches
- Systematic resolution of UNKNOWN payment states
- Audit trail for all reconciliation decisions
- Historical analysis of provider reliability
- Configurable tolerance thresholds per provider

### Lost
- Instant resolution for all cases
- Zero infrastructure overhead
- Simpler payment flow

## Consequences

### Positive
- UNKNOWN payments are resolved within defined SLAs
- Provider outages are detected and escalated automatically
- Financial positions are accurate after reconciliation runs
- Provider reliability metrics inform routing decisions
- Discrepancy patterns identify systemic issues

### Negative
- Reconciliation service adds operational complexity
- Provider API rate limits may delay reconciliation
- Some cases require manual intervention

## Implementation Notes

### Reconciliation Flow
```go
type ReconciliationService struct {
    store    ReconciliationStore
    providers map[string]PaymentProvider
    resolver  DiscrepancyResolver
}

func (rs *ReconciliationService) Reconcile(ctx context.Context, window TimeWindow) (*ReconciliationResult, error) {
    // Fetch internal payments in window
    internalPayments := rs.store.GetPaymentsByWindow(ctx, window)

    // Fetch external state from providers
    externalStates := make(map[uuid.UUID]ExternalState)
    for _, payment := range internalPayments {
        external, err := rs.providers[payment.Provider].GetPaymentStatus(ctx, payment.ExternalID)
        if err != nil {
            continue // Will be retried
        }
        externalStates[payment.ID] = external
    }

    // Compare and resolve
    result := &ReconciliationResult{}
    for _, payment := range internalPayments {
        external := externalStates[payment.ID]
        discrepancy := rs.compare(payment, external)
        if discrepancy != nil {
            resolution := rs.resolver.Resolve(ctx, discrepancy)
            result.Discrepancies = append(result.Discrepancies, discrepancy)
            result.Resolutions = append(result.Resolutions, resolution)
        }
    }

    return result, nil
}
```

### Discrepancy Types
```go
type DiscrepancyType string

const (
    DiscrepancyStateMismatch    DiscrepancyType = "STATE_MISMATCH"
    DiscrepancyMissingExternal  DiscrepancyType = "MISSING_EXTERNAL"
    DiscrepancyMissingInternal  DiscrepancyType = "MISSING_INTERNAL"
    DiscrepancyAmountMismatch   DiscrepancyType = "AMOUNT_MISMATCH"
    DiscrepancyDelayedResponse  DiscrepancyType = "DELAYED_RESPONSE"
)
```

### Resolution Strategy
```go
type DiscrepancyResolver interface {
    Resolve(ctx context.Context, d *Discrepancy) *Resolution
}

// Priority resolution for UNKNOWN payments
func (r *DefaultResolver) Resolve(ctx context.Context, d *Discrepancy) *Resolution {
    switch d.Type {
    case DiscrepancyMissingExternal:
        // Provider hasn't responded yet, schedule retry
        return r.scheduleRetry(ctx, d)
    case DiscrepancyStateMismatch:
        // Internal vs external state differs, escalate
        return r.escalateToHuman(ctx, d)
    case DiscrepancyAmountMismatch:
        // Amount differs, critical escalation
        return r.escalateToFinance(ctx, d)
    default:
        return r.logAndContinue(ctx, d)
    }
}
```
