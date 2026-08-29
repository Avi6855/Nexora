# ADR-016: Digital Twin Simulation

## Status

Accepted

## Context

Nexora needs to simulate financial scenarios—market crashes, fraud spikes, regulatory changes—without risking real money or customer data. Production simulation is too risky, and separate test environments don't capture real traffic patterns. We need a read-only simulation environment that uses production data patterns but executes in isolation.

## Decision

We will implement a read-only digital twin with isolated state that mirrors production data patterns for counterfactual simulation.

## Alternatives

### Direct Production Simulation
- **Pros**: Real data, real patterns
- **Cons**: Risk of data corruption, regulatory violations, customer impact

### Separate Test Environment
- **Pros**: Complete isolation, safe
- **Cons**: Synthetic data doesn't represent real patterns, maintenance overhead

### Production Data Clone
- **Pros**: Real data patterns
- **Cons**: PII exposure, storage cost, freshness issues

## Trade-offs

### Gained
- Safe simulation using production traffic patterns
- Counterfactual analysis ("what if" scenarios)
- No risk to real financial state or customer data
- Simulation results can inform policy and infrastructure changes
- Replay historical scenarios for post-incident analysis

### Lost
- Real-time production state fidelity
- Full production data volume
- Zero-latency simulation

## Consequences

### Positive
- Can simulate salary day load without real customer impact
- Fraud detection models can be tested against real attack patterns
- Regulatory scenarios can be simulated without compliance risk
- Incident response procedures can be validated against realistic conditions
- Business continuity plans can be tested regularly

### Negative
- Digital twin state may drift from production over time
- Simulation results are approximations, not guarantees
- Maintenance overhead for twin synchronization

## Implementation Notes

### Twin Architecture
```go
type DigitalTwin struct {
    ID             uuid.UUID
    SourceEnv      string // "production" (read-only mirror)
    SnapshotTime   time.Time
    State          TwinState
    Simulations    []Simulation
}

type TwinState struct {
    Accounts    []AccountSnapshot
    Balances    map[uuid.UUID]Money
    Policies    []PolicySnapshot
    Ledger      []LedgerEntry // anonymized
}
```

### Simulation Execution
```go
func (dt *DigitalTwin) Simulate(ctx context.Context, scenario Scenario) (*SimulationResult, error) {
    // Load twin state at snapshot time
    state := dt.State

    // Apply scenario modifications
    modifiedState := scenario.Apply(state)

    // Execute simulation (read-only, no writes to production)
    result, err := dt.executeSimulation(ctx, modifiedState, scenario)

    // Compare against baseline
    comparison := dt.compareBaseline(state, modifiedState, result)

    return &SimulationResult{
        Scenario:   scenario,
        Baseline:   state,
        Modified:   modifiedState,
        Result:     result,
        Comparison: comparison,
    }, nil
}
```

### Scenario Types
```go
type ScenarioType string

const (
    ScenarioMarketCrash       ScenarioType = "MARKET_CRASH"
    ScenarioFraudSpike        ScenarioType = "FRAUD_SPIKE"
    ScenarioRegulatoryChange  ScenarioType = "REGULATORY_CHANGE"
    ScenarioLoadSpike         ScenarioType = "LOAD_SPIKE"
    ScenarioProviderOutage    ScenarioType = "PROVIDER_OUTAGE"
    ScenarioInsolvencyEvent   ScenarioType = "INSOLVENCY_EVENT"
)
```
