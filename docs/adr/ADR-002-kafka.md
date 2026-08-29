# ADR-002: Apache Kafka

## Status

Accepted

## Context

Nexora requires an event-driven architecture with durable message ordering, exactly-once semantics, and the ability to replay events for state reconstruction.

## Decision

We will use Apache Kafka as the event backbone for all asynchronous communication between services.

## Alternatives

### RabbitMQ
- **Pros**: Flexible routing, multiple protocols, mature
- **Cons**: No durable log, limited replay capability, single point of failure

### Amazon SQS/SNS
- **Pros**: Fully managed, auto-scaling, AWS integration
- **Cons**: Vendor lock-in, limited ordering guarantees, expensive at scale

### NATS
- **Pros**: Lightweight, fast, simple
- **Cons**: Limited durability, no replay capability, weaker ordering guarantees

## Trade-offs

### Gained
- Durable, ordered event log
- Exactly-once semantics
- Event replay capability
- High throughput
- Decoupled services

### Lost
- Simple message queue semantics
- Easy message deletion
- Simple routing patterns
- Lower operational complexity

## Consequences

### Positive
- Events are durably stored and can be replayed
- Services can rebuild state from events
- Strong ordering guarantees per partition
- High throughput for event streaming

### Negative
- Must manage topic partitions
- Must handle consumer lag
- Must implement idempotent consumers
- Must manage dead letter queues

## Implementation Notes

### Topic Design
```bash
# Payment events
kafka-topics --create --topic payment.events --partitions 6

# Ledger events
kafka-topics --create --topic ledger.events --partitions 6
```

### Partition Strategy
- Partition by aggregate ID for ordering
- Use consistent partitioning for related events

### Consumer Groups
- One consumer group per service
- Rebalance strategy: RoundRobin
