# ADR-005: Outbox Pattern

## Status

Accepted

## Context

Nexora must ensure atomicity between state changes and event publishing. Dual writes (separate database and event store) can lead to inconsistencies.

## Decision

We will implement the transactional outbox pattern to ensure atomicity between state changes and event publishing.

## Alternatives

### Dual Writes
- **Pros**: Simple implementation
- **Cons**: Inconsistent state possible, no atomicity

### Change Data Capture
- **Pros**: No application changes
- **Cons**: Complex setup, limited flexibility

### Two-Phase Commit
- **Pros**: Strong consistency
- **Cons**: Performance overhead, complexity

## Trade-offs

### Gained
- Atomicity between state and events
- At-least-once delivery guarantee
- No dual writes
- Consistent state across services

### Lost
- Additional storage for outbox
- Polling overhead
- Implementation complexity

## Consequences

### Positive
- State and events are always consistent
- No lost events during failures
- Simple recovery from failures
- Better reliability

### Negative
- Must implement outbox table
- Must poll outbox for publishing
- Must handle outbox cleanup

## Implementation Notes

### Outbox Table
```cql
CREATE TABLE nexora.outbox_events (
    event_id UUID,
    aggregate_id UUID,
    event_type TEXT,
    payload TEXT,
    created_at TIMESTAMP,
    published_at TIMESTAMP,
    PRIMARY KEY (event_id)
);
```

### Publishing
```go
func (s *EventPublisher) PublishEvents(ctx context.Context) error {
    events, _ := s.outboxRepo.GetUnpublished(ctx)
    for _, event := range events {
        s.kafka.Publish(ctx, event.Type, event)
        s.outboxRepo.MarkPublished(ctx, event.ID)
    }
    return nil
}
```
