# Nexora Architecture Overview

## High-Level Architecture

Nexora follows an event-driven microservices architecture designed for financial correctness, fault tolerance, and operational safety.

### Core Principles

1. **Event Sourcing**: All state changes are captured as immutable events
2. **CQRS**: Command and Query Responsibility Segregation for read/write optimization
3. **Saga Pattern**: Distributed transactions via compensating actions
4. **Idempotency**: Every operation is idempotent to prevent duplicate processing
5. **Double-Entry Bookkeeping**: Financial accuracy through balanced ledgers

## Component Diagram

```
┌────────────────────────────────────────────────────────────────────┐
│                         Client Layer                                │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐            │
│  │ Android App  │  │   Web App    │  │  Third Party │            │
│  │ (Kotlin)     │  │  (React)     │  │    APIs      │            │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘            │
└─────────┼─────────────────┼─────────────────┼──────────────────────┘
          │                 │                 │
          v                 v                 v
┌────────────────────────────────────────────────────────────────────┐
│                       API Gateway (Envoy)                          │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐     │
│  │ Rate Limit │ │   Auth     │ │   Load     │ │   SSL      │     │
│  │            │ │  Middleware │ │ Balancing  │ │ Termination│     │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘     │
└─────────────────────────────┬──────────────────────────────────────┘
                              │
┌─────────────────────────────┼──────────────────────────────────────┐
│                       Service Layer                                 │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                  Core Services                               │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │   │
│  │  │ Identity │ │   User   │ │ Account  │ │  Ledger  │      │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                Financial Services                            │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │   │
│  │  │ Payment  │ │Transfer  │ │   Card   │ │   Pot    │      │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              Supporting Services                             │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐      │   │
│  │  │  Fraud   │ │Notification│ │Reconcil- │ │  Replay  │      │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘      │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────┬──────────────────────────────────────┘
                              │
┌─────────────────────────────┼──────────────────────────────────────┐
│                       Data Layer                                    │
│                                                                     │
│  ┌─────────────────────┐  ┌─────────────────────┐                 │
│  │   Apache Kafka      │  │   Apache Cassandra  │                 │
│  │   (Event Store)     │  │   (State Store)     │                 │
│  │                     │  │                     │                 │
│  │  ┌───────────────┐  │  │  ┌───────────────┐  │                 │
│  │  │ Payment Events│  │  │  │ Accounts      │  │                 │
│  │  │ Ledger Events │  │  │  │ Transactions  │  │                 │
│  │  │ User Events   │  │  │  │ Users         │  │                 │
│  │  └───────────────┘  │  │  └───────────────┘  │                 │
│  └─────────────────────┘  └─────────────────────┘                 │
└────────────────────────────────────────────────────────────────────┘
```

## Data Flow

### Payment Processing Flow

```
1. Client → API Gateway → Payment Service
   ┌─────────────────────────────────────────────────────────────┐
   │  Create Payment Request                                      │
   │  - Idempotency key                                           │
   │  - Account ID                                                │
   │  - Amount, Currency                                          │
   │  - Counterparty details                                      │
   └─────────────────────────────────────────────────────────────┘

2. Payment Service → Ledger Service → Reserve Funds
   ┌─────────────────────────────────────────────────────────────┐
   │  Create Reservation                                          │
   │  - Deduct from available balance                             │
   │  - Set TTL for reservation                                   │
   │  - Prevent double spend                                      │
   └─────────────────────────────────────────────────────────────┘

3. Payment Service → Payment Provider → Process Payment
   ┌─────────────────────────────────────────────────────────────┐
   │  External Provider Call                                      │
   │  - Timeout handling                                          │
   │  - UNKNOWN state for uncertain results                       │
   │  - Retry with backoff                                        │
   └─────────────────────────────────────────────────────────────┘

4. Payment Service → Kafka → Event Published
   ┌─────────────────────────────────────────────────────────────┐
   │  payment.processing → payment.confirmed → payment.settled   │
   │  - Correlation ID for tracing                                │
   │  - Causation ID for event lineage                            │
   └─────────────────────────────────────────────────────────────┘

5. Ledger Service → Settle Reservation → Create Double Entry
   ┌─────────────────────────────────────────────────────────────┐
   │  Debit sender account                                        │
   │  Credit receiver account                                     │
   │  Verify: debits == credits                                   │
   └─────────────────────────────────────────────────────────────┘
```

## Technology Decisions

### Go Microservices
- **Why Go**: Fast compilation, excellent concurrency, strong typing
- **Why not Java**: Lighter footprint, faster startup, simpler deployment

### Apache Kafka
- **Why Kafka**: Durable event log, exactly-once semantics, scalable
- **Why not RabbitMQ**: Better for event sourcing, stronger durability guarantees

### Apache Cassandra
- **Why Cassandra**: Linear scalability, tunable consistency, fault tolerant
- **Why not PostgreSQL**: Better for write-heavy workloads, horizontal scaling

### Envoy Gateway
- **Why Envoy**: Modern proxy, gRPC support, observability built-in
- **Why not NGINX**: Better for microservices, native gRPC support

## Financial Correctness

### Double-Entry Bookkeeping
Every financial transaction creates balanced entries:
- **Debit**: Money leaving an account
- **Credit**: Money entering an account
- **Invariant**: Total debits == Total credits

### Reservation Engine
Funds are reserved before payment processing:
- Prevents double spending
- Time-limited reservations (TTL)
- Automatic expiration for safety

### Idempotency
Every operation uses idempotency keys:
- Prevents duplicate processing
- Safe retries
- Consistent state across failures

## Observability

- **Structured Logging**: zerolog with correlation IDs
- **Distributed Tracing**: OpenTelemetry integration
- **Metrics**: Prometheus-compatible metrics
- **Health Checks**: Service health endpoints

## Security

- **JWT Tokens**: Short-lived access tokens
- **bcrypt Password Hashing**: Cost factor 12
- **Encrypted Storage**: Sensitive data encryption
- **Rate Limiting**: API throttling
- **Input Validation**: Request sanitization
