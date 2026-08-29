# ADR-014: Shadow Policy Execution

## Status

Accepted

## Context

Nexora's policy engine evaluates fraud detection, transaction limits, and compliance rules. Testing new policies in production risks customer impact—false positives block legitimate transactions, false negatives allow fraud. We need a way to validate policy changes against real traffic without affecting customers.

## Decision

We will implement shadow mode execution where both the old and new policy are executed in parallel, with results compared but only the old policy's result applied to the transaction.

## Alternatives

### A/B Testing
- **Pros**: Real customer impact measurement, statistical significance
- **Cons**: Some customers receive degraded experience, hard to revert

### Gradual Rollout
- **Pros**: Limited blast radius, easy rollback
- **Cons**: Still impacts customers, requires percentage-based routing

### Staging Environment Only
- **Pros**: Zero production risk
- **Cons**: Staging traffic doesn't represent real patterns, data skew

## Trade-offs

### Gained
- Zero customer impact during policy validation
- Direct comparison of old vs new policy on real transactions
- Statistical analysis of false positive/negative rates
- Safe validation of complex policy interactions
- Ability to test policy changes during high-traffic periods

### Lost
- Direct measurement of customer experience impact
- Real-time policy effect on transaction outcomes
- Simpler implementation

## Consequences

### Positive
- Policy changes can be validated against millions of real transactions
- False positive rates can be measured before customer impact
- Fraud detection improvements can be quantified precisely
- Compliance rule changes can be validated without regulatory risk
- Shadow results feed back into policy tuning pipeline

### Negative
- Double compute cost during shadow execution
- Shadow results must be stored and compared asynchronously
- Complex diffing logic for policy decisions with multiple dimensions

## Implementation Notes

### Shadow Execution Flow
```go
func (pe *PolicyEngine) EvaluateWithShadow(ctx context.Context, tx Transaction, newPolicy *Policy) (*PolicyResult, *ShadowResult) {
    // Execute current policy (applied to transaction)
    oldResult := pe.evaluate(ctx, tx, pe.currentPolicy)

    // Execute new policy in shadow (compared only)
    shadowResult := pe.evaluate(ctx, tx, newPolicy)

    // Emit comparison metrics
    pe.metrics.EmitShadowComparison(oldResult, shadowResult)

    return oldResult, &ShadowResult{
        OldPolicyResult: oldResult,
        NewPolicyResult: shadowResult,
        TransactionID:   tx.ID,
        Timestamp:       time.Now(),
    }
}
```

### Shadow Comparison Metrics
```go
type ShadowResult struct {
    OldPolicyResult *PolicyResult
    NewPolicyResult *PolicyResult
    TransactionID   uuid.UUID
    Timestamp       time.Time
}

// Metrics emitted
shadow.decisions.match     // Both policies agree
shadow.decisions.differ    // Policies disagree
shadow.false_positive_new  // New policy blocks legitimate
shadow.false_negative_new  // New policy allows fraud
```
