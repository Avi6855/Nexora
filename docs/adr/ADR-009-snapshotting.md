# ADR-009: Snapshotting

## Status

Accepted

## Context

Event replay can be slow for services with long event histories. Nexora needs a way to speed up state reconstruction.

## Decision

We will implement snapshotting to create periodic checkpoints of service state.

## Alternatives

### Full Event Replay
- **Pros**: Complete history
- **Cons**: Slow for large histories

### Periodic Backup
- **Pros**: Fast recovery
- **Cons**: Limited granularity

### CQRS with Read Models
- **Pros**: Optimized reads
- **Cons**: Complex implementation

## Trade-offs

### Gained
- Faster state reconstruction
- Reduced replay time
- Lower storage costs
- Better performance

### Lost
- Complete event history
- Simpler implementation
- Real-time consistency

## Consequences

### Positive
- State reconstruction is faster
- Less replay time needed
- Lower storage costs
- Better user experience

### Negative
- Must implement snapshotting logic
- Must manage snapshot storage
- Must handle snapshot consistency

## Implementation Notes

### Snapshot Structure
```go
type Snapshot struct {
    AggregateID   uuid.UUID
    Version       int
    State         []byte
    CreatedAt     time.Time
}
```

### Snapshotting Logic
```go
func (s *SnapshotService) CreateSnapshot(ctx context.Context, aggregateID uuid.UUID) error {
    state, _ := s.replayService.ReconstructState(ctx, aggregateID)
    snapshot := &Snapshot{
        AggregateID: aggregateID,
        Version:     s.getVersion(aggregateID),
        State:       state,
        CreatedAt:   time.Now().UTC(),
    }
    return s.snapshotRepo.Create(ctx, snapshot)
}
```

### Recovery
```go
func (s *RecoveryService) RecoverState(ctx context.Context, aggregateID uuid.UUID) error {
    snapshot, _ := s.snapshotRepo.GetLatest(ctx, aggregateID)
    if snapshot != nil {
        // Apply events after snapshot
        events, _ := s.eventStore.GetEventsAfter(ctx, aggregateID, snapshot.Version)
        for _, event := range events {
            // Apply event to state
        }
    } else {
        // Full replay
        s.replayService.ReplayEvents(ctx, aggregateID)
    }
    return nil
}
```
