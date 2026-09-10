# Nexora Banking Platform

A production-style digital banking platform built with event-driven architecture, featuring a native Android application and a Go microservices backend.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Android App                              │
│  Kotlin · Jetpack Compose · Material 3 · Hilt · Retrofit       │
└─────────────────────────────┬───────────────────────────────────┘
                              │
                              v
┌─────────────────────────────────────────────────────────────────┐
│                      Envoy Gateway                              │
│                  (API Gateway :8000)                             │
└─────────────────────────────┬───────────────────────────────────┘
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
          v                   v                   v
┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
│   REST APIs     │ │   gRPC APIs     │ │   WebSocket     │
└────────┬────────┘ └────────┬────────┘ └────────┬────────┘
         │                   │                   │
         └───────────────────┼───────────────────┘
                             │
                             v
┌─────────────────────────────────────────────────────────────────┐
│                    Go Microservices                              │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐          │
│  │ Identity │ │   User   │ │ Account  │ │  Ledger  │          │
│  │ Service  │ │ Service  │ │ Service  │ │ Service  │          │
│  │  :8081   │ │  :8082   │ │  :8083   │ │  :8084   │          │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘          │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐          │
│  │ Payment  │ │Transfer  │ │   Card   │ │   Pot    │          │
│  │ Service  │ │ Service  │ │ Service  │ │ Service  │          │
│  │  :8085   │ │  :8086   │ │  :8087   │ │  :8088   │          │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘          │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐          │
│  │  Fraud   │ │Notification│ │Reconcil- │ │  Replay  │          │
│  │ Service  │ │ Service  │ │iation Svc│ │ Service  │          │
│  │  :8089   │ │  :8090   │ │  :8091   │ │  :8092   │          │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘          │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐          │
│  │Simulation│ │  Policy  │ │  Audit   │ │Incident  │          │
│  │ Service  │ │ Service  │ │ Service  │ │ Service  │          │
│  │  :8093   │ │  :8094   │ │  :8095   │ │  :8097   │          │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘          │
│  ┌──────────────────────────────┐                              │
│  │     Control Plane Service    │                              │
│  │           :8096              │                              │
│  └──────────────────────────────┘                              │
└─────────────────────────────┬───────────────────────────────────┘
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
          v                   v                   v
┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
│    Apache Kafka │ │    Apache       │ │   Supporting    │
│   :9092         │ │    Cassandra   │ │   State         │
│                 │ │    :9042       │ │                 │
└─────────────────┘ └─────────────────┘ └─────────────────┘
```

## Prerequisites

- **Docker** & **Docker Compose** (v2.20+)
- **Go** 1.22+
- **Android Studio** (Hedgehog or later)
- **JDK** 17
- **Android SDK** 34

## Quick Start

```bash
# Clone the repository
git clone https://github.com/nexora/nexora.git
cd nexora

# Start infrastructure
docker-compose up -d

# Initialize database
make init-db

# Build and run services
make build

# Run tests
make test

# Access services
# API Gateway: http://localhost:8000
# Kafka UI: http://localhost:8080
# Envoy Admin: http://localhost:9901
```

## Service Overview

| Service | Port | Description |
|---------|------|-------------|
| Identity Service | 8081 | Authentication, JWT tokens, OTP |
| User Service | 8082 | User profiles, KYC |
| Account Service | 8083 | Bank accounts, balance queries |
| Ledger Service | 8084 | Double-entry bookkeeping, reservations |
| Payment Service | 8085 | Payment processing, state machine |
| Transfer Service | 8086 | Internal transfers |
| Card Service | 8087 | Card management, virtual cards |
| Pot Service | 8088 | Savings pots, goals |
| Fraud Service | 8089 | Fraud detection, risk scoring |
| Notification Service | 8090 | Push notifications, emails |
| Reconciliation Service | 8091 | Transaction reconciliation |
| Replay Service | 8092 | Event replay, state reconstruction |
| Simulation Service | 8093 | Payment simulation, testing |
| Policy Service | 8094 | Business rules, compliance |
| Audit Service | 8095 | Audit logging, compliance |
| Control Plane | 8096 | Service orchestration |
| Incident Service | 8097 | Incident management |

### Platform Capabilities (shared libraries)

| Package | Purpose |
|---------|---------|
| `shared/calc` | Deterministic, versioned financial arithmetic — exact accrual accumulator, day counts, DST-safe calendars, effective-dated rate schedules, golden-corpus gated |
| `shared/verify` | Dual-implementation calculation verification with quarantine + canary data validation with drift budgets |
| `shared/latency` | API latency budget manager with in-band deadline propagation |
| `shared/concurrency` | Adaptive concurrency controller (AIMD) — the knee of the latency curve, not the cliff |
| `shared/retry` | API error semantics + retry classification (NEVER/IMMEDIATE/DELAYED/REAUTH/POISON/DUPLICATE) with full-jitter backoff |
| `shared/handover` | Safe handover protocol: monotone epochs, fencing tokens, drain/freeze/capture state machine — split-brain prevention between primary and stand-in |
| `shared/cardnet` | Card network message gateway (ISO 8583-flavoured, v1/v2/v3 → canonical), replay/simulation lab, transaction correlation graph, out-of-order advice sequencing, offline presentment, contactless counters |
| `shared/kyc` | Progressive KYC verification tiers (risk→evidence), evidence freshness engine, provider arbitration with failover, review-queue balancing (urgent SLA ⇒ skill-match outranks load), regulatory evidence expiration guard (legal holds + retention floors) |
| `shared/openfinance` | Connected-account link health with weakest-component floor, cross-provider semantic normalizer, per-field transaction confidence scoring |
| `shared/cardnet` | Card network message gateway (ISO 8583-flavoured, v1/v2/v3 → canonical), replay/simulation lab, transaction correlation graph, out-of-order advice sequencing, offline presentment, contactless counters, race-safe PIN try counter (CAS/versioned) |
| `shared/datainfra` | Cassandra partition-size skew (signature-based remediation), tombstone pressure, hot/cold tiering with access override, adaptive bucketing (coarsest-fitting), repair orchestration (windowed, concurrency-capped, 24h node spacing), retention optimizer bound by compliance/lag/replay, Kafka hotspot detection, consumer-lag advisor, topic ownership registry |
| `shared/neteng` | Cross-cloud path health scoring, physical circuit capacity forecasting (linear trend), cross-cloud traffic cost optimizer |
| `shared/engplatform` | Service bootstrapper, blocking golden-path deploy gate, weighted maturity scoring with weakest-dimension floor, dependency upgrade intelligence, reachability-first vulnerability prioritisation, TTL-capped ephemeral environments, runtime dependency contract registry |
| `shared/economics` | Journey cost attribution (unit costs → per-journey price + top cost driver), reliability tier optimiser (infra + support + churn×LTV + reputational vs. nines) |
| `shared/cash` | Deposit smart routing (capability + limits + health + risk), dynamic AML risk tiering, provider failover with reservations that return headroom on expiry |
| `shared/cheques` | OCR intelligence with confidence-aware routing, fraud scoring, clearing lifecycle with exception workflows (RETURNED/MISMATCH/DUPLICATE/DAMAGED) |
| `shared/payees` | Sort-code/account validation gateway, payee name resolution with match confidence, recipient directory, change history, safe payment templates |
| `shared/statements` | Statement versioning with correction diffs, tamper-evident hash-chain verification (VALID/ALTERED/UNKNOWN) |
| `shared/export` | GDPR export job platform (redaction, encryption, archive) with incremental delta exports from checkpoints |
| `shared/search` | Transaction search with explicit consistency lag (reconciled watermark + boundary set), explainable matching, resumable grid-aligned backfill |
| `shared/support` | Purpose-based customer context API, case state machine with SLA, queue fairness ranking, deterministic case deduplication |
| `shared/vendors` | External SLA monitor (per-observation breach semantics), capability registry, zero-downtime credential rotation, maintenance-window coordination |
| `shared/compliance` | Regulatory rule distribution with acknowledgements, time-travel compliance, reporting evidence chains, impact analyzer, config lifecycle/drift, service safe mode |
| `shared/cards` | Merchant-locked virtual cards, programmable authorization rules, credential continuity after reissue, lifecycle saga, delivery-exception recovery |
| `shared/credit` | Credit limit simulator, repayment strategy optimiser, bureau correction cases, decision-policy sandbox, fairness monitoring on both sides of a split |
| `shared/mortgage` | Affordability workspace, async document state machine (provider-outage tolerant), offer-expiry protection |
| `shared/investments` | HMRC-shared ISA allowance as one gateway (correction-exact), joint goals over individual wrappers, tax-lot engine (FIFO/LIFO/HIFO), deterministic corporate actions |
| `shared/lifeevents` | Bereavement workflow (protection-first, gated, disputable), auto-expiring life-event workspaces with DAG tasks and exact beneficiary shares |
| `shared/identity` | Progressive recovery with strong-evidence ceilings, device trust graph with cascading revocation, passkey challenges (single-use, replay-proof) + rotation-safe recovery credentials |
| `shared/openbanking` | Failure classification → recovery lanes (silent refresh / backoff / customer / engineering), freshness SLAs (UNKNOWN ≠ STALE), runtime capability matrix |
| `shared/dataplatform` | Data quality contracts with volume corridor, data-product SLA dashboard (UNOWNED is visible), privacy-aware query gateway, hash-chained access recording, migration safety pipeline with expand-migrate-contract gate |

The control plane also hosts **Primary↔Stand-in Compatibility Certification**
(decision-level semantic comparison — decision divergence is categorically
BREAKING) and **Engineering Change Risk Scoring** (structural risk weights →
derived rollout gates).

The policy service also hosts the **Time-Travel Compliance Engine**
(`/v1/compliance/time-travel`): historical decision reconstruction from
append-only policy versions and customer-state snapshots, with a verifiable
replay hash. The control plane hosts the **Regulatory Change Impact Analyzer**
(`/v1/control/regulatory/impact`) and the **Regulatory Reporting Pipeline**
with evidence provenance (`/v1/control/regulatory/reports/...`) — every
submitted figure carries a hash-verified chain from source datasets through
transformation to submission.

## API Documentation

See [docs/api/API.md](docs/api/API.md) for complete API reference.

## Android App

```bash
cd android
./gradlew assembleDebug
```

### Features
- Secure authentication with biometrics
- Real-time balance updates
- Payment processing with state tracking
- Savings pots with goals
- Card management
- Push notifications

## Development

```bash
# Run linter
make lint

# Format code
make fmt

# Run specific test
go test ./tests/unit/...
go test ./tests/integration/...
go test ./tests/financial/...
go test ./tests/contract/...

# View logs
make logs
```

## Testing

```bash
# Unit tests
make test-unit

# Integration tests
make test-integration

# Financial invariant tests
make test-financial

# API contract tests
make test-contract

# Platform correctness suites (calc golden corpus, verify, latency, concurrency, retry)
make test-shared

# All tests
make test-all
```

## Deployment

```bash
# Build Docker images
make rebuild

# Deploy to Kubernetes
kubectl apply -f k8s/

# Check status
make status
```

## Documentation

- [Architecture Overview](docs/architecture/OVERVIEW.md)
- [Cassandra Data Model](docs/architecture/CASSANDRA.md)
- [Kafka Event Design](docs/architecture/KAFKA.md)
- [Ledger Design](docs/architecture/LEDGER.md)
- [Payment Flow](docs/architecture/PAYMENT.md)
- [API Reference](docs/api/API.md)

### Architecture Decision Records

- [ADR-001: Cassandra](docs/adr/ADR-001-cassandra.md)
- [ADR-002: Kafka](docs/adr/ADR-002-kafka.md)
- [ADR-003: Ledger](docs/adr/ADR-003-ledger.md)
- [ADR-004: Idempotency](docs/adr/ADR-004-idempotency.md)
- [ADR-005: Outbox Pattern](docs/adr/ADR-005-outbox.md)
- [ADR-006: Saga Pattern](docs/adr/ADR-006-saga.md)
- [ADR-007: Unknown State](docs/adr/ADR-007-unknown-state.md)
- [ADR-008: Event Replay](docs/adr/ADR-008-replay.md)
- [ADR-009: Snapshotting](docs/adr/ADR-009-snapshotting.md)
- [ADR-010: Consistency](docs/adr/ADR-010-consistency.md)
- [ADR-023: Calc Library & Verification](docs/adr/ADR-023-calc-verification.md)
- [ADR-024: Latency Budgets & Adaptive Concurrency](docs/adr/ADR-023-calc-verification.md)
- [ADR-025: Time-Travel Compliance](docs/adr/ADR-023-calc-verification.md)
- [ADR-026: Regulatory Impact & Evidence Provenance](docs/adr/ADR-023-calc-verification.md)
- [ADR-027: Error Semantics & Retry Classification](docs/adr/ADR-023-calc-verification.md)
- [ADR-028: Stand-in Compatibility & Safe Handover](docs/adr/ADR-028-standin-handover.md)
- [ADR-029: Card Network Gateway & ISO 8583 Lab](docs/adr/ADR-028-standin-handover.md)
- [ADR-030: Data Platform Health Intelligence](docs/adr/ADR-028-standin-handover.md)
- [ADR-031: Engineering Change Risk Scoring](docs/adr/ADR-028-standin-handover.md)
- [ADR-032: Open-Finance, Data-Lifecycle and Storage Intelligence](docs/adr/ADR-032-openfinance-data-lifecycle.md)
- [ADR-033: Engineering Platform as Product & Operational Economics](docs/adr/ADR-033-engplatform-economics.md)
- [ADR-034: Domain Platform Suites (Cards, Credit, Mortgage, Investments, Life Events, Identity, Open Banking)](docs/adr/ADR-034-domain-platform-suites.md)
- [ADR-035: Data Platform Governance at 100+ Teams](docs/adr/ADR-035-dataplatform-governance.md)

## License

MIT License
