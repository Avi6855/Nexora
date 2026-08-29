# Kafka Event Design

## Overview

Apache Kafka serves as the event backbone for Nexora, providing durable, ordered, and fault-tolerant event streaming.

## Topic Design

### Topic Naming Convention

```
{service}.{event-domain}.{event-type}
```

### Core Topics

| Topic | Partitions | Retention | Description |
|-------|-----------|-----------|-------------|
| payment.events | 6 | 7 days | Payment lifecycle events |
| ledger.events | 6 | 7 days | Ledger transaction events |
| user.events | 3 | 7 days | User lifecycle events |
| account.events | 3 | 7 days | Account state changes |
| notification.events | 3 | 7 days | Notification triggers |
| fraud.events | 3 | 7 days | Fraud detection events |
| reconciliation.events | 3 | 7 days | Reconciliation results |

### Topic Configuration

```bash
# Create payment events topic
kafka-topics --create \
    --topic payment.events \
    --partitions 6 \
    --replication-factor 1 \
    --config retention.ms=604800000 \
    --config cleanup.policy=delete \
    --bootstrap-server localhost:9092

# Create ledger events topic
kafka-topics --create \
    --topic ledger.events \
    --partitions 6 \
    --replication-factor 1 \
    --config retention.ms=604800000 \
    --bootstrap-server localhost:9092
```

## Partition Strategy

### Key-Based Partitioning

Events are partitioned by aggregate ID to ensure ordering:

```go
// Payment events partitioned by payment_id
key := payment.PaymentID.String()

// Ledger events partitioned by account_id
key := entry.AccountID.String()

// User events partitioned by user_id
key := user.UserID.String()
```

### Benefits

1. **Ordering**: Events for same aggregate processed in order
2. **Locality**: Related events on same partition
3. **Parallelism**: Independent aggregates processed concurrently

## Consumer Groups

### Service Consumer Groups

| Service | Consumer Group | Topics |
|---------|---------------|--------|
| Ledger Service | ledger-consumer | payment.events |
| Fraud Service | fraud-consumer | payment.events, user.events |
| Notification Service | notification-consumer | payment.events, user.events |
| Reconciliation Service | reconciliation-consumer | payment.events, ledger.events |
| Audit Service | audit-consumer | *.events |
| Replay Service | replay-consumer | *.events |

### Consumer Configuration

```go
config := sarama.NewConfig()
config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
config.Consumer.Offsets.Initial = sarama.OffsetOldest
config.Consumer.Return.Errors = true
config.Net.MaxOpenRequests = 5
```

## Event Envelope

### Structure

```json
{
    "event_id": "550e8400-e29b-41d4-a716-446655440000",
    "event_type": "payment.created",
    "aggregate_id": "payment-123",
    "correlation_id": "req-456",
    "causation_id": "evt-789",
    "producer": "payment-service",
    "timestamp": "2024-01-01T00:00:00Z",
    "payload": {},
    "event_version": 1
}
```

### Fields

- **event_id**: Unique identifier for deduplication
- **event_type**: Type of event (e.g., payment.created)
- **aggregate_id**: ID of the aggregate root
- **correlation_id**: Request ID for tracing
- **causation_id**: Event ID that caused this event
- **producer**: Service that produced the event
- **timestamp**: When event occurred
- **payload**: Event-specific data
- **event_version**: Schema version

### Event Types

```go
const (
    // Payment events
    EventTypePaymentCreated    EventType = "payment.created"
    EventTypePaymentAuthorized EventType = "payment.authorized"
    EventTypePaymentProcessing EventType = "payment.processing"
    EventTypePaymentConfirmed  EventType = "payment.confirmed"
    EventTypePaymentSettled    EventType = "payment.settled"
    EventTypePaymentFailed     EventType = "payment.failed"
    EventTypePaymentReversed   EventType = "payment.reversed"
    EventTypePaymentCancelled  EventType = "payment.cancelled"
    EventTypePaymentUnknown    EventType = "payment.unknown"

    // Ledger events
    EventTypeLedgerTransactionCreated EventType = "ledger.transaction.created"
    EventTypeLedgerEntryCreated       EventType = "ledger.entry.created"
    EventTypeReservationCreated       EventType = "reservation.created"
    EventTypeReservationSettled       EventType = "reservation.settled"
    EventTypeReservationReleased      EventType = "reservation.released"

    // User events
    EventTypeUserCreated  EventType = "user.created"
    EventTypeUserUpdated  EventType = "user.updated"
    EventTypeUserVerified EventType = "user.verified"
)
```

## Outbox Pattern

### Implementation

Nexora uses the transactional outbox pattern to ensure atomicity between state changes and event publishing:

```go
func (s *PaymentSaga) ExecuteCreatePayment(ctx context.Context, req *CreatePaymentRequest) (*Payment, error) {
    // 1. Begin transaction
    tx, _ := s.db.Begin(ctx)

    // 2. Store payment
    payment := NewPayment(req)
    s.paymentRepo.Create(ctx, payment)

    // 3. Store outbox event
    event := NewEvent("payment.created", payment)
    s.outboxRepo.Store(ctx, event)

    // 4. Commit transaction
    tx.Commit()

    // 5. Publisher polls outbox and publishes
    return payment, nil
}
```

### Benefits

1. **Atomicity**: State change and event published together
2. **At-least-once delivery**: Guaranteed delivery
3. **No dual writes**: Single transaction for state and events

## Dead Letter Queue

### Configuration

```bash
# Create dead letter topic
kafka-topics --create \
    --topic payment.events.dlq \
    --partitions 3 \
    --replication-factor 1 \
    --bootstrap-server localhost:9092
```

### Handling

```go
func (c *Consumer) HandleError(ctx context.Context, event *Event, err error) {
    // Retry 3 times
    if c.retryCount < 3 {
        c.retryCount++
        time.Sleep(time.Duration(c.retryCount) * time.Second)
        return c.ProcessEvent(ctx, event)
    }

    // Send to DLQ
    c.publisher.Publish(ctx, topic+".dlq", event)
    c.logger.Error().Err(err).Str("event_id", event.EventID).Msg("sent to DLQ")
}
```

## Event Replay

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

### Use Cases

1. **State reconstruction**: Rebuild service state from events
2. **Bug recovery**: Replay events after bug fix
3. **New service bootstrap**: Initialize new service from events

## Monitoring

### Key Metrics

- **Consumer lag**: Messages behind producer
- **Throughput**: Messages per second
- **Error rate**: Failed message processing
- **Partition distribution**: Even distribution across brokers

### Alerts

```yaml
groups:
    - name: kafka
      rules:
        - alert: KafkaConsumerLagHigh
          expr: kafka_consumer_group_lag > 1000
          for: 5m

        - alert: KafkaConsumerLagCritical
          expr: kafka_consumer_group_lag > 10000
          for: 1m
```

## Best Practices

1. **Idempotent consumers**: Handle duplicate messages
2. **Ordering**: Use partition keys for ordering
3. **Schema evolution**: Version events properly
4. **Monitoring**: Track consumer lag and errors
5. **DLQ handling**: Process dead letter messages
6. **Retention**: Set appropriate retention periods
