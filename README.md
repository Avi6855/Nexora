# Nexora

**A digital-banking platform built end to end: 20 Go microservices, Cassandra as the
system of record, Kafka for events, Envoy at the edge, Kubernetes for scheduling, and a
native Android app.**

The feature list is not the interesting part. The interesting part is the **money
correctness machinery** underneath it — the part that decides what happens when a
provider times out, when two requests race, or when the same settlement event is
delivered twice. That machinery is enforced in code and proven by tests, and this README
points at both.

> **Reviewing this in a hurry?** Start with
> [docs/REVIEWERS_GUIDE.md](docs/REVIEWERS_GUIDE.md) — a five-minute path through the
> invariants, naming the test that proves each one, plus an honest list of what this
> platform does *not* prove yet.

**Scale** — every number below is counted from this repository, not estimated:

| | Count |
|---|---|
| Go microservices (each its own Go module) | **20** (22 modules incl. root + `shared`) |
| REST endpoint bindings (`/v1/...`, method + path) | **553** across 531 distinct paths |
| Shared platform packages | **72** |
| Cassandra tables | **54** |
| Kafka topics (incl. 8 dead-letter topics) | **73** |
| Go test functions in 153 test files | **894** — and the whole suite runs **without Docker, Cassandra or Kafka** |
| Android feature modules / Kotlin test functions | **13** / 48 |
| Architecture Decision Records | **30** |
| Kubernetes manifests | **12** |
| Go source | 549 files, ~115,600 lines |

---

## 1. Why this is worth five minutes

- **Money movement is exactly-once, and the code says how.** Booking is gated by a
  Cassandra LWT claim row, so a payment books once even when `confirmed` and `settled`
  race or Kafka redelivers. There is a test that fires **16 concurrent creates with the
  same idempotency key** and asserts exactly one payment exists.
- **Indeterminate is a state, not an error.** A provider timeout does not become
  `FAILED`. It becomes `UNKNOWN`, the customer's hold stays in place, a case is filed,
  and a scheduled sweep decides the outcome from evidence — on both money paths (external
  rail and internal ledger booking). See [§3](#3-the-unknown-bucket--how-it-is-actually-closed).
- **Fail-closed is the default, not a branch someone remembered.** On the card
  authorization path, no risk answer or no verifiable funds means *decline*. The decision
  path is synchronous and sub-second.

---

## 2. Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│  Android app — Kotlin · Compose · Hilt · Retrofit                    │
│  13 feature modules · live feed over SSE                             │
└───────────────────────────────┬──────────────────────────────────────┘
                                │  HTTPS
                                v
┌──────────────────────────────────────────────────────────────────────┐
│  Envoy gateway  :8000   (routing, retries, timeouts, metrics)         │
└───────────────────────────────┬──────────────────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────┐
        v                       v                       v
┌───────────────┐     ┌───────────────┐     ┌───────────────────────┐
│ money paths   │     │ product       │     │ platform / control    │
│ ledger  8084  │     │ account  8083 │     │ control-plane  8096   │
│ payment 8085  │     │ pot      8088 │     │ policy         8094   │
│ transfer 8086 │     │ insights 8099 │     │ audit          8095   │
│ card    8087  │     │ dispute  8098 │     │ incident       8097   │
│ fraud   8089  │     │ consent  8100 │     │ simulation     8093   │
│ reconcil.8091 │     │ user     8082 │     │ replay         8092   │
└───────┬───────┘     │ identity 8081 │     └───────────┬───────────┘
        │             └───────┬───────┘                 │
        └─────────────────────┼─────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        v                     v                     v
┌───────────────┐   ┌───────────────┐   ┌───────────────────────┐
│ Apache Kafka  │   │  Cassandra    │   │ Prometheus + Grafana  │
│  :9092        │   │  :9042        │   │  :9090 / :3000        │
│  73 topics    │   │  keyspace per │   │  per-service /metrics │
│  8 DLQs       │   │  service      │   │                       │
└───────────────┘   └───────────────┘   └───────────────────────┘
```

Every service is a separate Go module with its own `go.mod`. That is deliberate — it
mirrors a real multi-team repository, and it is why CI builds and vets **each module from
inside its own directory** instead of trusting a root-level `go build ./...`.

### Services

| Service | Port | Responsibility |
|---|---|---|
| `identity-service` | 8081 | Authentication, JWT issuance/rotation, OTP |
| `user-service` | 8082 | Profiles, KYC state |
| `account-service` | 8083 | Accounts, balances (read from the ledger, not stored) |
| `ledger-service` | 8084 | Double-entry bookkeeping, reservations, exactly-once booking, refund legs |
| `payment-service` | 8085 | Payment saga, transactional outbox, UNKNOWN resolution |
| `transfer-service` | 8086 | Account-to-account transfers, indeterminate-booking sweep |
| `card-service` | 8087 | Card lifecycle, real-time authorization, PSD2 step-up, refunds |
| `pot-service` | 8088 | Savings pots, goals, round-ups |
| `fraud-service` | 8089 | Risk scoring over stored history, transfer scam checks |
| `notification-service` | 8090 | Persisted feed, SSE realtime push |
| `reconciliation-service` | 8091 | Discrepancy cases, the UNKNOWN sweep, write-back |
| `replay-service` | 8092 | Event replay, state reconstruction, time travel |
| `simulation-service` | 8093 | Scheme and payment simulation lab |
| `policy-service` | 8094 | Business rules, time-travel compliance engine |
| `audit-service` | 8095 | Append-only audit log and evidence chains |
| `control-plane-service` | 8096 | Dependency graph, failover, change-risk, regulatory impact |
| `incident-service` | 8097 | Incident management, compensation policies |
| `dispute-service` | 8098 | Chargeback orchestration, evidence, adjustments |
| `insights-service` | 8099 | Subscriptions, safe-to-spend, runway |
| `consent-service` | 8100 | Consent centre, delegated access, open banking, AI gateway |

---

## 3. The UNKNOWN bucket — how it is actually closed

Money leaves an account here in two ways, and **both can be answered indeterminately**.
The same gap turned up on both paths, so the same invariant is now enforced on both:
**an indeterminate answer must never be recorded as a definite one.**

Marking a timeout as `FAILED` tells the customer the money did not move when it may have
— and worse, it makes the retry path refuse to resume, because a failed movement is not
resumable.

**External rail (card / bank transfer out).** The provider times out. The payment goes
`UNKNOWN` and the authorisation hold is deliberately **not** released — releasing it would
make the same money spendable a second time. `reconciliation-service` consumes
`nexora.payment.unknown`, files one `PENDING` case per payment (idempotent, because
delivery is at-least-once), and a scheduled sweep (`RECONCILIATION_INTERVAL`, default 60s,
plus a boot sweep) reads the payment back from `payment-service`. If a late provider
callback already concluded it, the case closes against that state instead of retrying work
that is done. When the case holds a definite outcome, the sweep calls the internal-only
`POST /v1/payments/{id}/resolve-unknown` and only then closes. A case marked resolved
while the payment is still `UNKNOWN` would hide held money, so that is not allowed.

**Internal ledger booking (account to account).** The booking call may time out *after*
the ledger committed. The transfer goes `UNKNOWN` rather than `FAILED`, and the recovery
is a retry with the **same** ledger idempotency key: the ledger is exactly-once on that
key, so the retry either settles the booking that already happened or books the one that
never did. It cannot move the money twice. Past the escalation window the sweep publishes
`transfer.reconciliation.required` instead of retrying forever.

Two `UNKNOWN` states are deliberately **not** a match. Both sides agreeing that nobody
knows anything is not evidence about what the rail did.

Read it: [`ADR-007`](docs/adr/ADR-007-unknown-state.md) ·
[`ADR-018`](docs/adr/ADR-018-reconciliation.md) ·
[`payment_saga.go`](services/payment-service/internal/service/payment_saga.go) ·
[`transfer_service.go`](services/transfer-service/internal/service/transfer_service.go)

---

## 4. Invariants, and the test that proves each one

| Invariant | Proven by |
|---|---|
| A payment state change and its event commit in one atomic write (no dual write) | `TestCreatePersistsStateAndEventInOneAtomicWrite` |
| One user action creates exactly one payment under 16 concurrent callers sharing a key | `TestConcurrentCreateWithSameIdempotencyKeyCreatesOnePayment` |
| An indeterminate outcome is never reported as `FAILED` or `SETTLED` | `TestUnknownOutcomeIsNeverMarkedFailed` |
| An `UNKNOWN` payment stays `UNKNOWN` **and held** until it is resolved | `TestUnknownPaymentStaysUnknownAndHeldUntilItIsResolved` |
| A late provider callback concludes the `UNKNOWN` payment | `TestLateProviderCallbackConcludesTheUnknownPayment` |
| Reconciliation resolves an `UNKNOWN` payment over HTTP exactly once | `TestReconciliationResolvesUnknownOverHTTPExactlyOnce` |
| A timed-out ledger booking is `UNKNOWN`, not `FAILED` | `TestIndeterminateBookingIsRecordedAsUnknown` |
| A retry resumes an indeterminate transfer with the **same** ledger key | `TestRetryResumesAnIndeterminateTransferWithTheSameLedgerKey` |
| A duplicate provider callback cannot double-book | `TestDuplicateProviderCallbackCannotDoubleBook` |
| Card authorization fails closed when risk is unreachable | `TestRiskEngineUnavailableFailsClosed` |
| Card authorization declines rather than approving unfunded | `TestLedgerUnavailableDeclinesRatherThanApprovingUnfunded` |
| A hold is settled exactly once; a duplicate capture moves no money | `TestCaptureSettlesTheHoldExactlyOnce` |
| A PSD2 step-up holds **no** money until the cardholder authenticates | `TestExpirySweepDeclinesAnUnansweredStepUp` (+ 15 step-up, 11 schemes tests) |
| Debits equal credits; reservations cannot overspend | `services/ledger-service/tests/ledger_service_test.go` (22 invariants) |

The full map — including the parts that are **not** covered — is in
[docs/REVIEWERS_GUIDE.md](docs/REVIEWERS_GUIDE.md#5-test-map--what-is-proven-and-what-is-not).

### Platform capability packages (`shared/`, 72 packages)

Beyond the service layer, `shared/` carries the reusable machinery: `calc` (deterministic,
versioned financial arithmetic gated by a golden corpus), `verify` (dual-implementation
calculation verification), `outbox`, `money`, `cassandra`, `kafka`, `telemetry`,
`retry` (error semantics + retry classification with full-jitter backoff),
`latency` (deadline propagation) and `concurrency` (adaptive AIMD control), `handover`
(fencing tokens and safe stand-in handover), `cardnet` (ISO 8583-flavoured scheme gateway),
`schemes` (incl. PSD2 SCA challenge lifecycle), `kyc`, `cards`, `credit`, `mortgage`,
`investments`, `lifeevents`, `identity`, `openbanking`, `openfinance`, `datainfra`,
`dataplatform`, `engplatform`, `economics`, `cash`, `cheques`, `payees`, `statements`,
`export`, `search`, `support`, `vendors`, `compliance`.

---

## 5. Quick start

Prerequisites: **Docker + Compose v2.20+**, **Go 1.25+**, **JDK 17**, **Android SDK 36**
(Android Studio for the app). Go 1.22+ works for most modules; the module files declare
`go 1.25`.

```bash
git clone https://github.com/Avi6855/Nexora.git
cd Nexora

# Infrastructure (Cassandra, Kafka, Kafka UI, Prometheus, Grafana)
docker compose up -d

# Apply the Cassandra schema
make init-db

# Build everything, module by module
make build

# Run the whole test suite — no Docker required for this part
make test-all
```

Then:

| What | Where |
|---|---|
| API gateway | http://localhost:8000 |
| Kafka UI | http://localhost:8080 |
| Prometheus | http://localhost:9090 |
| Grafana (dashboard: *MonzoBank Platform*) | http://localhost:3000 |

The live end-to-end runbook — card tap → risk → hold → capture → refund → step-up, all
against real Cassandra and Kafka — is [docs/demo/MONZO_DEMO.md](docs/demo/MONZO_DEMO.md).

## 6. Testing

Each service is its own module, so a root-level `go test ./...` silently skips them. The
Makefile and CI both handle that explicitly.

```bash
make test-all        # root unit + contract, shared platform suites, all 20 services
make test-modules    # every service module
make test-shared     # shared/ platform suites
make test-financial  # financial invariant tests
make test-contract   # API contract tests
```

The suite needs **no Docker, no Cassandra and no Kafka** — services are tested against
in-memory doubles and real HTTP stubs, so it runs on a laptop or a CI runner. CI
(`.github/workflows/ci.yml`) runs vet + `gofmt` + build + `go test -race` for **every
module**, plus an Android build and unit tests, Docker image builds, and a security scan
that fails on hardcoded passwords or `float64` in financial code.

## 7. Documentation

- [Reviewer's guide](docs/REVIEWERS_GUIDE.md) — the five-minute path, and the honest limits
- [Architecture overview](docs/architecture/OVERVIEW.md) · [Cassandra model](docs/architecture/CASSANDRA.md) · [Kafka design](docs/architecture/KAFKA.md) · [Ledger](docs/architecture/LEDGER.md) · [Payment flow](docs/architecture/PAYMENT.md)
- [API reference](docs/api/API.md)
- [Demo runbook](docs/demo/MONZO_DEMO.md)

### Architecture Decision Records (30)

| | | |
|---|---|---|
| [001 Cassandra](docs/adr/ADR-001-cassandra.md) | [002 Kafka](docs/adr/ADR-002-kafka.md) | [003 Ledger](docs/adr/ADR-003-ledger.md) |
| [004 Idempotency](docs/adr/ADR-004-idempotency.md) | [005 Outbox](docs/adr/ADR-005-outbox.md) | [006 Saga](docs/adr/ADR-006-saga.md) |
| [007 UNKNOWN state](docs/adr/ADR-007-unknown-state.md) | [008 Event replay](docs/adr/ADR-008-replay.md) | [009 Snapshotting](docs/adr/ADR-009-snapshotting.md) |
| [010 Consistency levels](docs/adr/ADR-010-consistency.md) | [011 Envoy gateway](docs/adr/ADR-011-envoy.md) | [012 Kubernetes](docs/adr/ADR-012-kubernetes.md) |
| [013 Multi-party auth](docs/adr/ADR-013-multi-party-auth.md) | [014 Shadow policy](docs/adr/ADR-014-shadow-policy.md) | [015 Chaos engineering](docs/adr/ADR-015-chaos-engineering.md) |
| [016 Digital twin](docs/adr/ADR-016-digital-twin.md) | [017 Financial backpressure](docs/adr/ADR-017-financial-backpressure.md) | [018 Reconciliation](docs/adr/ADR-018-reconciliation.md) |
| [019 Android architecture](docs/adr/ADR-019-android-architecture.md) | [020 Realtime updates](docs/adr/ADR-020-real-time-updates.md) | [021 Real-time card authorization](docs/adr/ADR-021-realtime-card-authorization.md) |
| [022 Observability](docs/adr/ADR-022-observability.md) | [023–027 Calc, latency, compliance, retry](docs/adr/ADR-023-calc-verification.md) | [028–031 Stand-in, cardnet, data platform, change risk](docs/adr/ADR-028-standin-handover.md) |
| [032 Open-finance & data lifecycle](docs/adr/ADR-032-openfinance-data-lifecycle.md) | [033 Engineering platform & economics](docs/adr/ADR-033-engplatform-economics.md) | [034 Domain platform suites](docs/adr/ADR-034-domain-platform-suites.md) |
| [035 Data platform governance](docs/adr/ADR-035-dataplatform-governance.md) | [036 Money-path invariants](docs/adr/ADR-036-money-path-invariants.md) | [037 PSD2 SCA step-up](docs/adr/ADR-037-psd2-sca-step-up.md) |

Some ADRs share a file where they were decided together: ADR-023 also contains ADR-024
(latency budgets), ADR-025 (time-travel compliance), ADR-026 (regulatory provenance) and
ADR-027 (error semantics); ADR-028 also contains ADR-029 (card network gateway), ADR-030
(data platform health) and ADR-031 (change risk scoring).

## 8. Honest limits

Stated deliberately, because knowing what a system does *not* do is part of the
engineering. The full list lives in
[REVIEWERS_GUIDE §6–7](docs/REVIEWERS_GUIDE.md#6-honest-limits):

- **This is not a bank and it has never handled real money or real customer data.** It is
  an independent engineering project, not affiliated with, endorsed by, or connected to
  any bank or payment provider.
- Secondary indexes / `ALLOW FILTERING` are dev-scale; production adds explicit lookup
  tables and materialised per-account balance rows.
- The SSE hub is in-process, so `notification-service` is single-instance until fan-out
  moves to pub/sub.
- The outbox relay polls; production would drive it from CDC or a bounded-delay scheduler.
- No load test is committed, so there are no p50/p99 figures for the authorization path —
  only a sub-second design budget and per-request `LatencyMs` recorded on every
  authorization row.
- The hold is released before the settlement debit is known to have been booked; the
  ledger refuses rather than overdrawing, but that window still needs an operator-visible
  reconciliation alarm.
- The git history is a series of large feature rounds, not the small reversible steps a
  reviewer would normally follow. The ADRs are the durable record of intent instead.

## License

MIT License.
