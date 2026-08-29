# ADR-013: Multi-Party Authorization

## Status

Accepted

## Context

Nexora requires approval workflows for sensitive operations such as large transfers, account recovery, policy changes, and incident response. A single authorization step is insufficient for high-risk operations that could result in financial loss or regulatory violations.

## Decision

We will implement a multi-party authorization workflow with states: REQUESTED → APPROVAL_REQUIRED → APPROVED → EXECUTED, supporting configurable approval thresholds and escalation paths.

## Alternatives

### Single Approval
- **Pros**: Simple UX, fast execution
- **Cons**: Single point of compromise, insufficient for high-risk operations

### Role-Based Only
- **Pros**: Standard RBAC model, easy to understand
- **Cons**: No time-bound approval, no audit trail for authorization decisions

### External PAM Solution
- **Pros**: Enterprise-grade, pre-built workflows
- **Cons**: Vendor dependency, integration complexity, licensing cost

## Trade-offs

### Gained
- Cryptographic proof of each approver's identity
- Configurable thresholds per operation type
- Complete audit trail of authorization decisions
- Time-bound approval windows to prevent stale authorizations
- Escalation paths for unresponsive approvers

### Lost
- Instant execution for all operations
- Simpler user experience
- Lower implementation complexity

## Consequences

### Positive
- Large transfers require dual authorization, reducing fraud risk
- Policy changes require security team sign-off before activation
- Incident response can be delegated with time-limited authority
- Complete audit trail satisfies regulatory requirements
- Thresholds can be tuned based on risk scoring

### Negative
- Some operations require multiple human approvals, adding latency
- UX complexity for approvers (notification, review, approve/reject)
- Need to handle edge cases (approver unavailable, timeout, revocation)

## Implementation Notes

### Authorization State Machine
```go
type AuthState string

const (
    AuthStateRequested         AuthState = "REQUESTED"
    AuthStateApprovalRequired  AuthState = "APPROVAL_REQUIRED"
    AuthStateApproved          AuthState = "APPROVED"
    AuthStateRejected          AuthState = "REJECTED"
    AuthStateExecuted          AuthState = "EXECUTED"
    AuthStateExpired           AuthState = "EXPIRED"
)

type Authorization struct {
    ID            uuid.UUID
    OperationType string
    OperationID   uuid.UUID
    RequesterID   uuid.UUID
    State         AuthState
    Threshold     int
    Approvals     []Approval
    ExpiresAt     time.Time
    CreatedAt     time.Time
}
```

### Threshold Configuration
```go
var Thresholds = map[string]int{
    "TRANSFER_STANDARD":  1,
    "TRANSFER_LARGE":     2,
    "ACCOUNT_RECOVERY":   2,
    "POLICY_CHANGE":      3,
    "INCIDENT_RESPONSE":  1,
}
```
