# ADR-015: Chaos Engineering

## Status

Accepted

## Context

Nexora handles financial transactions that must be resilient to infrastructure failures. We need to verify that circuit breakers, retries, failover, and financial invariants (no money created or destroyed) hold under realistic failure conditions. External chaos tools add operational complexity and may not integrate with financial invariant checking.

## Decision

We will implement a built-in chaos engine for fault injection in development and staging environments, with automatic financial invariant verification after each experiment.

## Alternatives

### Chaos Monkey / Simian Army
- **Pros**: Battle-tested, Netflix-proven
- **Cons**: AWS-specific, external dependency, no financial invariant checking

### Litmus Chaos
- **Pros**: Kubernetes-native, broad fault types, community edition
- **Cons**: Kubernetes-only, complex setup, no domain-specific checks

### Manual Fault Injection
- **Pros**: Full control, no tooling overhead
- **Cons**: Not reproducible, human error, no automation

## Trade-offs

### Gained
- Tightly integrated with Nexora's financial invariant checks
- Reproducible fault injection experiments with defined parameters
- Automatic rollback if financial invariants are violated
- Custom fault types specific to payment processing (e.g., ledger write delay)
- Experiment results stored for compliance audit

### Lost
- Off-the-shelf chaos tooling ecosystem
- Community-contributed fault scenarios
- Third-party verification of experiment results

## Consequences

### Positive
- Can verify that double-entry bookkeeping holds under partition
- Circuit breaker behavior validated against real failure patterns
- Data consistency verified during simulated infrastructure outages
- Experiment library grows with each incident post-mortem
- Confidence in disaster recovery procedures

### Negative
- Must maintain custom chaos engine code
- Limited to dev/staging (never production for financial safety)
- Experiment design requires domain expertise

## Implementation Notes

### Chaos Experiment Definition
```go
type ChaosExperiment struct {
    ID          uuid.UUID
    Name        string
    Description string
    Faults      []Fault
    Invariants  []InvariantCheck
    Environment string // "development" or "staging" only
    MaxDuration time.Duration
}

type Fault struct {
    Type      FaultType
    Target    string // service or endpoint
    Intensity float64
    Duration  time.Duration
}

type FaultType string

const (
    FaultNetworkPartition  FaultType = "NETWORK_PARTITION"
    FaultLatency           FaultType = "LATENCY_INJECTION"
    FaultServiceDown       FaultType = "SERVICE_DOWN"
    FaultDiskFull          FaultType = "DISK_FULL"
    FaultCPUStress         FaultType = "CPU_STRESS"
    FaultLedgerWriteDelay  FaultType = "LEDGER_WRITE_DELAY"
)
```

### Invariant Verification
```go
type InvariantCheck struct {
    Name    string
    Check   func(ctx context.Context) error
    Critical bool // If true, abort experiment
}

var FinancialInvariants = []InvariantCheck{
    {
        Name:     "total_debits_equals_total_credits",
        Check:    verifyDoubleEntry,
        Critical: true,
    },
    {
        Name:     "no_negative_balances",
        Check:    verifyNoNegativeBalances,
        Critical: true,
    },
    {
        Name:     "idempotency_preserved",
        Check:    verifyIdempotency,
        Critical: true,
    },
}
```
