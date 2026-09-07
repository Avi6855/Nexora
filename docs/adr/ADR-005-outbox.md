# ADR-005: Outbox Pattern

## Status

Accepted — **implemented** (see `shared/outbox`).

## Context

Nexora must ensure atomicity between state changes and event publishing. Dual
writes (separate database and event store) can lead to inconsistencies: a
payment can settle without its event ever being published, or an event can be
published for a state transition that never happened. Both break downstream
consumers (notifications, reconciliation, analytics).

Earlier code published to Kafka fire-and-forget and swallowed publish errors,
so events were silently lost whenever Kafka was unavailable or slow.

## Decision

We implement the **transactional outbox** pattern:

1. Every payment state transition and its domain event are written to
   Cassandra in **one logged batch** (`payments` row + `outbox_events` row).
   Either both commit or neither does — the dual-write problem is eliminated
   at the write site, not papered over.
2. A **relay** (`shared/outbox.Relay`) polls the outbox, publishes PENDING
   events to Kafka with a synchronous producer (`acks=all`), and marks them
   PUBLISHED.
3. Failed publishes are **retried on every poll** (attempt count tracked in
   the row). An event whose attempts reach `MaxAttempts` is moved to a
   **dead-letter topic** (`nexora.<domain>.dlq`) and marked FAILED, so a
   poison event can never wedge the relay.
4. Delivery is **at-least-once**. Consumers must deduplicate on `event_id` /
   `idempotency_key` — the relay may crash between the Kafka ack and the
   "published" marker, causing a redelivery.

## Implementation

### Outbox Table

```cql
CREATE TABLE nexora.outbox_events (
    event_id UUID PRIMARY KEY,
    aggregate_id TEXT,
    event_type TEXT,
    topic TEXT,
    correlation_id TEXT,
    causation_id TEXT,
    producer TEXT,
    payload BLOB,
    status TEXT,          -- PENDING | PUBLISHED | FAILED
    attempts INT,
    last_error TEXT,
    created_at TIMESTAMP,
    published_at TIMESTAMP
);
CREATE INDEX idx_outbox_status ON nexora.outbox_events (status);
```

### Atomic dual-write

The payment repository implements `repository.AtomicEventWriter`
(`CreateWithEvents` / `UpdateWithEvents`): the aggregate `UPDATE`/`INSERT` and
the `outbox_events` insert go in a single **logged batch** via
`session.NewBatch(gocql.LoggedBatch)`. The payment saga routes every state
transition through `PaymentSaga.persistTransition`, which type-asserts the
repository and uses the atomic path when available (falling back to a
sequential write+enqueue for in-memory test doubles).

This is a cross-partition logged batch (`payments` keyed by `payment_id`,
`outbox_events` keyed by `event_id`), which Cassandra supports at lower
throughput than a same-partition batch — an acceptable trade for correctness
in a financial system. A future optimisation could co-locate the keys.

### Relay

```go
store     := outbox.NewCassandraStore(session)      // shared/outbox
publisher, _ := outbox.NewKafkaPublisher(outbox.KafkaPublisherConfig{Brokers: cfg.Kafka.Brokers, Logger: logger})
relay     := outbox.NewRelay(store, publisher, logger, outbox.DefaultRelayConfig())
relay.Start(ctx)   // drains immediately, then every 1s
```

`Relay.process`:

- `attempts >= MaxAttempts` → publish to the DLQ topic, mark FAILED (terminal).
- `Publish` error → `MarkFailed` (increments attempts, stores `last_error`),
  event stays PENDING and is retried next poll.
- `Publish` success → `MarkPublished`. If the marker write fails, the event is
  redelivered on restart — at-least-once.

### Producer wiring

`events.OutboxEventPublisher` replaces the direct Kafka publisher: it builds
the same `PaymentEventEnvelope` JSON and persists it to the outbox. The relay
publishes that exact payload to `nexora.payment.<state>`, so consumers are
unchanged. Kafka record headers carry `event_type`, `event_id`,
`correlation_id`, `causation_id`, `producer`, `timestamp`.

## Alternatives

### Dual Writes
- **Pros**: Simple implementation
- **Cons**: Inconsistent state possible, no atomicity — rejected.

### Change Data Capture
- **Pros**: No application changes
- **Cons**: Complex setup, limited flexibility — rejected.

### Two-Phase Commit
- **Pros**: Strong consistency
- **Cons**: Performance overhead, complexity — rejected; logged batches give
  the same guarantee at the write site without a transaction coordinator.

## Trade-offs

### Gained
- Atomicity between state and events
- At-least-once delivery guarantee, no silently lost events
- Retry + DLQ handling for poison events
- Restart safety: the relay drains immediately on start

### Lost
- Additional storage for the outbox table
- Polling overhead (1s ticker; `ALLOW FILTERING` claim query at dev scale)
- One extra hop of latency (~1s worst case) between transition and Kafka
- Logged-batch throughput cost for cross-partition writes

## Consequences

### Positive
- State and events are always consistent
- No lost events during failures
- Simple recovery from failures; replay is a natural by-product
- Demonstrable in the demo: kill Kafka, run a payment, restart Kafka, watch
  the relay drain the backlog

### Negative
- Must poll the outbox for publishing
- Must handle outbox cleanup (terminal FAILED rows; TTL candidates)
- Consumers must be idempotent (at-least-once)

## Future Work

- Shard `outbox_events` by a status-bucket key so claims never use
  `ALLOW FILTERING` at scale.
- Add TTLs to terminal rows and a purge job.
- Adopt the same pattern in transfer/account services (they still publish
  directly).