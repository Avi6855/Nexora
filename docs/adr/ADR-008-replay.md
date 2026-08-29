# ADR-008: Event Replay

## Status

Accepted

## Context

Nexora needs the ability to rebuild service state from events, recover from bugs, and bootstrap new services.

## Decision

We will implement event replay capability using Kafka's durable event log.

## Alternatives

### Database Snapshots
- **Pros**: Fast recovery
- **Cons**: Limited history, storage overhead

### Change Data Capture
- **Pros**: Real-time sync
- **Cons**: Complex setup, limited replay

### Manual Recovery
- **Pros**: Full control
- **Cons**: Time-consuming, error-prone

## Trade-offs

### Gained
- Complete event history
- State reconstruction
- Bug recovery
- New service bootstrap

### Lost
- Storage efficiency
- Simple recovery
- Low latency reads

## Consequences

### Positive
- Can rebuild state from any point in time
- Can recover from bugs by replaying events
- Can bootstrap new services from events
- Complete audit trail

### Negative
- Must store all events
- Replay can be slow for large histories
- Must handle event schema evolution

## Implementation Notes

### Replay Service
```go
func (s *ReplayService) ReplayEvents(ctx context.Context, fromTime time.Time) error {
    topics := []string{"payment.events", "ledger.events", "user.events"}
    for _, topic := range topics {
        partitions, _ := s.consumer.Partitions(topic)
        for _, partition := range partitions {
            offset := s.getOffsetForTime(topic, partition, fromTime)
            s.consumer.SetOffset(topic, partition, offset)
        }
    }
    return nil
}
```

### State Reconstruction
```go
func (s *ReplayService) ReconstructBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
    entries, _ := s.eventStore.GetEventsByAggregate(accountID)
    var balance int64
    for _, event := range entries {
        // Apply event to balance
    }
    return balance, nil
}
```
