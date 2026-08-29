# ADR-001: Apache Cassandra

## Status

Accepted

## Context

Nexora needs a distributed database that can handle high write throughput, provide linear scalability, and maintain availability during network partitions.

## Decision

We will use Apache Cassandra as the primary state store for all services.

## Alternatives

### PostgreSQL
- **Pros**: ACID transactions, complex queries, mature ecosystem
- **Cons**: Vertical scaling only, single point of failure, write-heavy workloads performance

### MongoDB
- **Pros**: Flexible schema, horizontal scaling, good for documents
- **Cons**: Eventual consistency, weaker consistency guarantees, less suitable for financial data

### DynamoDB
- **Pros**: Fully managed, auto-scaling, AWS integration
- **Cons**: Vendor lock-in, limited query patterns, expensive at scale

## Trade-offs

### Gained
- Linear horizontal scalability
- High write throughput
- Fault tolerance with no single point of failure
- Tunable consistency levels
- Multi-datacenter replication

### Lost
- ACID transactions across multiple partitions
- Complex joins and aggregations
- Schema flexibility (Cassandra is schema-aware)
- Strong consistency by default (must use QUORUM)

## Consequences

### Positive
- System can scale to handle millions of transactions per second
- No single point of failure
- Can handle写-heavy workloads efficiently
- Tunable consistency for different use cases

### Negative
- Must design data models around query patterns
- Must handle eventual consistency in application logic
- Must implement application-level transactions
- Must manage tombstones and compaction

## Implementation Notes

### Data Model Design
```cql
-- Query-first approach
CREATE TABLE nexora.ledger_entries (
    account_id UUID,
    entry_id UUID,
    created_at TIMESTAMP,
    PRIMARY KEY (account_id, created_at, entry_id)
) WITH CLUSTERING ORDER BY (created_at DESC);
```

### Consistency Levels
- **Writes**: LOCAL_QUORUM for financial data
- **Reads**: LOCAL_QUORUM for balance queries
- **Non-critical**: ONE for audit logs

### Monitoring
- Track partition sizes
- Monitor read/write latency
- Alert on pending compactions
