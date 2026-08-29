# ADR-017: Financial Backpressure

## Status

Accepted

## Context

During system overload, Nexora must prioritize critical financial operations (ledger writes, balance updates, fraud checks) over non-critical work (notifications, analytics, audit logging). Simple rate limiting treats all requests equally, which could block money movement during high load. We need priority-aware backpressure that protects financial invariants.

## Decision

We will implement priority-based backpressure with three tiers: CRITICAL (ledger writes, balance updates), IMPORTANT (transfers, fraud checks), and DEFERABLE (notifications, analytics, audit logging).

## Alternatives

### Simple Rate Limiting
- **Pros**: Easy to implement, predictable
- **Cons**: Treats all requests equally, may block critical operations

### Queue-Based Backpressure
- **Pros**: Smooths traffic spikes, configurable capacity
- **Cons**: Added latency, complex queue management, no priority differentiation

### Circuit Breaker Only
- **Pros**: Prevents cascade failures
- **Cons**: Binary (open/closed), no gradual degradation

## Trade-offs

### Gained
- Critical financial operations always get resources
- Non-critical work is deferred, not dropped
- Graduated degradation under load
- Financial invariants preserved during overload
- Configurable per-service priority tiers

### Lost
- Uniform request handling
- Simple backpressure model
- Predictable latency for all request types

## Consequences

### Positive
- Ledger writes complete even during extreme load
- Balance updates are never delayed by notification processing
- Fraud checks run before analytics processing
- Audit logs are written asynchronously without blocking transactions
- System degrades gracefully rather than failing catastrophically

### Negative
- Notification delivery may be delayed during overload
- Analytics data may be stale during high-traffic periods
- Priority classification requires careful tuning per service

## Implementation Notes

### Priority Tiers
```go
type Priority int

const (
    PriorityCritical   Priority = 100 // Ledger, balances, fraud
    PriorityImportant  Priority = 50  // Transfers, payments
    PriorityDeferrable Priority = 10  // Notifications, analytics
)

type PrioritizedRequest struct {
    ID       uuid.UUID
    Priority Priority
    Payload  interface{}
    EnqueuedAt time.Time
    Timeout  time.Duration
}
```

### Backpressure Controller
```go
type BackpressureController struct {
    mu            sync.RWMutex
    load          float64 // 0.0 - 1.0
    thresholds    map[Priority]float64
    deferredQueue *PriorityQueue
}

func (bc *BackpressureController) Accept(req PrioritizedRequest) bool {
    bc.mu.RLock()
    defer bc.mu.RUnlock()

    threshold, ok := bc.thresholds[req.Priority]
    if !ok {
        return false
    }

    if bc.load > threshold {
        if req.Priority < PriorityImportant {
            return false // Drop low-priority under high load
        }
        bc.deferredQueue.Push(req) // Defer important work
        return true
    }

    return true
}
```

### Load Shedding Configuration
```go
var DefaultThresholds = map[Priority]float64{
    PriorityCritical:   0.95, // Accept until 95% load
    PriorityImportant:  0.80, // Accept until 80% load
    PriorityDeferrable: 0.50, // Accept until 50% load
}
```
