# Cassandra Data Model

## Overview

Nexora uses Apache Cassandra as the primary state store, following a query-first data modeling approach optimized for read patterns.

## Query-First Design

Cassandra data models are designed around query patterns, not normalization. Each table is optimized for specific access patterns.

### Key Principles

1. **Denormalize for reads**: Duplicate data to avoid joins
2. **Partition for distribution**: Even data distribution across nodes
3. **Cluster for ordering**: Use clustering keys for sort order
4. **Minimize partitions**: Keep related data in the same partition

## Keyspace Design

```cql
CREATE KEYSPACE nexora WITH replication = {
    'class': 'NetworkTopologyStrategy',
    'dc1': 3
} AND durable_writes = true;
```

## Tables

### Users Table

```cql
CREATE TABLE nexora.users (
    user_id UUID,
    email TEXT,
    phone TEXT,
    first_name TEXT,
    last_name TEXT,
    status TEXT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    PRIMARY KEY (user_id)
);

CREATE INDEX idx_users_email ON nexora.users (email);
CREATE INDEX idx_users_phone ON nexora.users (phone);
```

### Accounts Table

```cql
CREATE TABLE nexora.accounts (
    account_id UUID,
    user_id UUID,
    name TEXT,
    account_type TEXT,
    currency TEXT,
    status TEXT,
    balance BIGINT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    PRIMARY KEY (account_id)
);

CREATE INDEX idx_accounts_user ON nexora.accounts (user_id);
```

### Ledger Entries Table

```cql
CREATE TABLE nexora.ledger_entries (
    account_id UUID,
    entry_id UUID,
    transaction_id UUID,
    entry_type TEXT,
    entry_direction TEXT,
    amount BIGINT,
    currency TEXT,
    balance_before BIGINT,
    balance_after BIGINT,
    description TEXT,
    correlation_id TEXT,
    causation_id TEXT,
    event_version INT,
    created_at TIMESTAMP,
    PRIMARY KEY (account_id, created_at, entry_id)
) WITH CLUSTERING ORDER BY (created_at DESC, entry_id ASC);
```

### Ledger Transactions Table

```cql
CREATE TABLE nexora.ledger_transactions (
    transaction_id UUID,
    idempotency_key TEXT,
    transaction_type TEXT,
    status TEXT,
    total_amount BIGINT,
    currency TEXT,
    description TEXT,
    correlation_id TEXT,
    causation_id TEXT,
    event_version INT,
    created_at TIMESTAMP,
    completed_at TIMESTAMP,
    PRIMARY KEY (transaction_id)
);

CREATE INDEX idx_ledger_tx_idempotency ON nexora.ledger_transactions (idempotency_key);
```

### Reservations Table

```cql
CREATE TABLE nexora.reservations (
    reservation_id UUID,
    account_id UUID,
    transaction_id UUID,
    amount BIGINT,
    currency TEXT,
    status TEXT,
    expires_at TIMESTAMP,
    created_at TIMESTAMP,
    released_at TIMESTAMP,
    settled_at TIMESTAMP,
    PRIMARY KEY (account_id, reservation_id)
);

CREATE INDEX idx_reservations_status ON nexora.reservations (status);
CREATE INDEX idx_reservations_account_status ON nexora.reservations (account_id, status);
```

### Payments Table

```cql
CREATE TABLE nexora.payments (
    payment_id UUID,
    idempotency_key TEXT,
    account_id UUID,
    user_id UUID,
    payment_type TEXT,
    amount BIGINT,
    currency TEXT,
    state TEXT,
    failure_reason TEXT,
    counterparty_id TEXT,
    counterparty_name TEXT,
    reference TEXT,
    fraud_score DOUBLE,
    fraud_action TEXT,
    ledger_transaction_id TEXT,
    reservation_id TEXT,
    metadata MAP<TEXT, TEXT>,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    authorized_at TIMESTAMP,
    settled_at TIMESTAMP,
    PRIMARY KEY (payment_id)
);

CREATE INDEX idx_payments_idempotency ON nexora.payments (idempotency_key);
CREATE INDEX idx_payments_account ON nexora.payments (account_id);
CREATE INDEX idx_payments_user ON nexora.payments (user_id);
```

### Idempotency Records Table

```cql
CREATE TABLE nexora.idempotency_records (
    key TEXT,
    request_hash TEXT,
    status TEXT,
    response BYTES,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    expires_at TIMESTAMP,
    PRIMARY KEY (key)
);
```

## Partition Strategy

### Account-Based Partitioning

Most financial data is partitioned by `account_id`:

- **Benefits**: All account data on same node, efficient account queries
- **Trade-offs**: Hot partitions for high-volume accounts

### Mitigation for Hot Partitions

1. **Virtual partitions**: Split high-volume accounts
2. **Time-based clustering**: Recent data first
3. **Read replicas**: Distribute read load

## Consistency Levels

### Write Consistency

- **Default**: `LOCAL_QUORUM`
- **Financial writes**: `ALL` for critical operations
- **Non-critical**: `ONE` for audit logs

### Read Consistency

- **Default**: `LOCAL_QUORUM`
- **Balance queries**: `LOCAL_QUORUM`
- **Historical reads**: `ONE`

```cql
-- Example: Financial write with strong consistency
INSERT INTO nexora.ledger_entries (...)
VALUES (...)
USING CONSISTENCY LOCAL_QUORUM AND TTL 86400;

-- Example: Balance query with strong consistency
SELECT balance FROM nexora.accounts
WHERE account_id = ?
USING CONSISTENCY LOCAL_QUORUM;
```

## TTL (Time-To-Live)

### Automatic Expiration

```cql
-- Reservations expire after 30 minutes
INSERT INTO nexora.reservations (...)
VALUES (...)
USING TTL 1800;

-- Idempotency records expire after 24 hours
INSERT INTO nexora.idempotency_records (...)
VALUES (...)
USING TTL 86400;
```

## Data Compaction

### Strategy

- **TimeWindowCompactionStrategy** for time-series data
- **LeveledCompactionStrategy** for user data

```cql
ALTER TABLE nexora.ledger_entries WITH compaction = {
    'class': 'TimeWindowCompactionStrategy',
    'compaction_window_unit': 'HOURS',
    'compaction_window_size': '4'
};
```

## Backup Strategy

### Snapshots

```bash
# Full snapshot
nodetool snapshot nexora -t $(date +%Y%m%d)

# Incremental backup
nodetool disableautocompaction nexora ledger_entries
```

### Recovery

```bash
# Restore from snapshot
nodetool restore nexora -t 20240101
```

## Monitoring

### Key Metrics

- **Partition size**: Alert if > 100MB
- **Read latency**: Alert if > 50ms
- **Write latency**: Alert if > 100ms
- **Pending compactions**: Alert if > 100

### Query Optimization

```cql
-- Use ALLOW FILTERING sparingly
-- Prefer secondary indexes or materialized views

-- Good: Direct partition access
SELECT * FROM nexora.ledger_entries
WHERE account_id = ?
AND created_at > ?

-- Bad: Full table scan
SELECT * FROM nexora.ledger_entries
WHERE amount > 1000;
```
