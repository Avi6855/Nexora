# Reviewer's Guide

Everything a reviewer needs in **five minutes**: what this platform is, the
exact commands to prove it runs, which tests cover which invariant, and the
limits of what it does *not* prove.

---

## 1. The point of this repository

Nexora is a Monzo-style banking platform built end to end on the stack that
real challenger banks run: **Go services, Cassandra as the system of record,
Kafka for asynchronous events, Envoy at the edge, Kubernetes for scheduling**,
plus a native Android app.

The interesting part is not the number of features — it is the **money
correctness machinery** underneath them:

| Invariant | Where it lives | Proven by |
|---|---|---|
| A payment state change and its event commit atomically (no dual write) | `services/payment-service/internal/service/payment_saga.go`, `internal/repository/payment_repository.go`, `shared/outbox` | `TestCreatePersistsStateAndEventInOneAtomicWrite` |
| One user action creates exactly one payment, even when two requests race with the same idempotency key | `ClaimIdempotencyKey` (Cassandra LWT), `ExecuteCreatePayment` | `TestConcurrentCreateWithSameIdempotencyKeyCreatesOnePayment` |
| An indeterminate provider outcome is never reported as FAILED or SETTLED | `markUnknown`, `handleProviderError` | `TestUnknownOutcomeIsNeverMarkedFailed` |
| Card authorization fails closed: no risk answer or no funds ⇒ decline | `services/card-service/internal/service/authorization_service.go` | `TestRiskEngineUnavailableFailsClosed`, `TestLedgerUnavailableDeclinesRatherThanApprovingUnfunded` |
| The customer's own spending controls (online/ATM/gambling) decline before anything is held | `channelBlocked` | `TestCustomerSpendingControlsDeclineBeforeTheRiskEngine` |
| A hold is settled exactly once; a duplicate capture or provider callback moves no money | `CaptureAuthorization`, `ExecuteHandleProviderCallback` | `TestCaptureSettlesTheHoldExactlyOnce`, `TestDuplicateProviderCallbackCannotDoubleBook` |
| Debits equal credits in every ledger transaction; reservations cannot overspend | `services/ledger-service/internal/service` | `services/ledger-service/tests/ledger_service_test.go` (22 invariants) |
| A refund credits the customer and debits the suspense account as one balanced pair, exactly once on its key | `BookRefund`, `CreateEntryPair` | `services/ledger-service/tests/refund_booking_test.go` |
| A PSD2 step-up holds **no** money until the cardholder authenticates, and one code authenticates one presentment exactly once | `evaluateSCA`, `raiseChallenge`, `CompleteSCAChallenge`, `shared/schemes` challenge state machine | `services/card-service/internal/service/sca_test.go` (11), `shared/schemes/sca_test.go` (11) |
| A refund can never exceed the captured amount, and a claim whose credit never landed is written back so the money is not lost | `RefundAuthorization`, `SaveRefund` (optimistic concurrency), `releaseRefundClaim` | `services/card-service/internal/service/refund_test.go` (6) |
| The customer sees the step-up: the challenged event becomes a confirm-payment feed item carrying the code | `notification-service/internal/service/realtime_consumer.go` | `realtime_consumer_test.go` (4) |
| A step-up nobody answers is declined at its deadline, and the sweep is idempotent and race-safe against a late answer | `SweepExpiredChallenges`, `card_sca_challenges_by_expiry` (hourly buckets) | `TestExpirySweepDeclinesAnUnansweredStepUp`, `TestExpirySweepLeavesLiveAndAnsweredStepUpsAlone` |

---

## 2. Prove it in two commands

No Docker, no Cassandra, no Kafka needed — the suites use in-memory doubles and
HTTP stubs, so the whole thing runs on a laptop CI runner:

```bash
# every module: root, shared platform library, and all 20 services
make test-all

# or explicitly, module by module
cd shared && go test ./...
for mod in services/*/; do (cd "$mod" && go test ./...); done
```

Current state: **856 Go test functions across 147 files pass**, plus 48 Kotlin
test functions in the Android app. `gofmt -l .` is clean and CI runs vet, build
and tests for **every module** (each service is its own Go module — a root-level
`go test ./...` silently skips them, which the pipeline now handles explicitly).

---

## 3. Scale of the codebase

| | Count |
|---|---|
| Go microservices | 20 |
| REST endpoints (`/v1/...`) | 545 |
| Go source files / lines | 525 / ~105,000 |
| Shared platform packages (`shared/`) | 72 (13 core infra + 59 domain capability packages) |
| Cassandra tables | 54 |
| Kafka topics (incl. 8 DLQ) | 70 |
| Go test files / test functions | 147 / 856 |
| Android feature modules / screens | 13 / 25 (48 test functions) |
| Architecture Decision Records | 30 |
| Kubernetes manifests | 12 |

---

## 4. Where to read (in this order)

1. **`services/card-service/internal/service/authorization_service.go`** — the
   whole real-time card decision path in one function: card state → customer
   controls → risk engine over stored history → balance-checked ledger hold →
   durable authorization row → event. Every failure branch declines.
2. **`services/payment-service/internal/service/payment_saga.go`** — payment
   state machine, idempotency claim, outbox persistence, compensation.
3. **`services/payment-service/internal/domain/payment.go`** — the legal state
   transitions as data (`validTransitions`), so illegal moves are unrepresentable
   rather than defended against ad hoc.
4. **`docs/adr/ADR-005-outbox.md`**, **`ADR-007-unknown-state.md`**,
   **`ADR-021-realtime-card-authorization.md`**,
   **`ADR-036-money-path-invariants.md`**, **`ADR-037-psd2-sca-step-up.md`** —
   why, not what. ADR-036 is the one to
   read if you want the money-path invariants and, candidly, what is still open.
5. **`docs/demo/MONZO_DEMO.md`** — the live end-to-end runbook, including a
   section of the platform's own honest limits.

---

## 5. Test map — what is proven, and what is not

Covered by executable tests:

- **Payment service layer** (`internal/service/payment_service_test.go`):
  idempotency under concurrency, atomic state+event write, claim compensation on
  write failure, event ordering, terminal-state enforcement, unknown-state
  policy, cancel guards, duplicate-callback rejection. Authorisation holds are
  covered in `internal/service/hold_test.go`: the hold is taken for the payment's
  account/amount/currency/id, an unaffordable payment is refused and stays
  CREATED, a ledger outage fails closed, the write-failure path gives the hold
  straight back, and the hold is released exactly once on failure, cancellation
  and settlement — but deliberately **not** on an unknown outcome.
- **Ledger payment booking** (`services/ledger-service/tests/payment_booking_test.go`):
  the payer is DEBITed and the counterparty credited (a regression test for a
  sign bug that paid the payer), a booking the account cannot cover is refused
  instead of overdrawing, a refused booking hands its exactly-once claim back so
  the retry can book, and duplicate/concurrent deliveries of the same settlement
  move money exactly once.
- **Settlement balance and refusal visibility**
  (`services/ledger-service/tests/settlement_balance_test.go`): settling a hold
  writes a *balanced pair* (customer debit + clearing credit) so system-wide
  debits still equal credits, one settlement debits the customer exactly once,
  releasing a hold writes no entries at all, a refused booking is recorded as a
  `BOOKING_REFUSED` integrity event (and does **not** freeze the account or break
  the retry path).
- **Card authorization** (`internal/service/authorization_service_test.go`):
  real HTTP clients against stub risk/ledger servers — approve-with-hold,
  fail-closed on risk outage, insufficient funds, ledger outage, REVIEW is not
  approval, frozen card, currency mismatch, three spending controls, capture
  exactly once, void guards, cross-card mismatch.
- **PSD2 step-up** (`internal/service/sca_test.go`, `shared/schemes/sca_test.go`):
  every exemption path, a card-not-present presentment above the low-value
  ceiling going to CHALLENGED with **no** reservation, the correct code taking
  the hold, a replayed code taking nothing, attempt exhaustion and expiry
  declining without a hold, a challenge authenticating only its own presentment,
  a challenged presentment being uncapturable/unvoidable, and the code never
  being stored, serialised or readable in the clear.
- **Card refunds** (`internal/service/refund_test.go`,
  `services/ledger-service/tests/refund_booking_test.go`): partial→full refunds
  totalling exactly the captured amount, over-refund refused before any money
  moves, only captured presentments refundable, a failed ledger credit rolled
  back so the refund can be retried, and a refund claim from a stale read
  rejected (optimistic concurrency on the refundable balance).
- **Ledger** (`services/ledger-service/tests/`): double entry, debits = credits,
  no overspend under concurrent reservations, reservation settle/release,
  integrity monitor freeze + self-heal, currency and balance checks.
- **Shared platform suites** (`shared/*/`): golden-corpus financial arithmetic,
  dual-implementation verification, latency budgets, adaptive concurrency, retry
  classification, handover fencing, card network messages, KYC tiers, open
  finance, data lifecycle.

**Not covered (deliberately stated, not hidden):**

- `internal/transport` handlers for most services (they are thin
  request-decode/respond layers) and the Cassandra repositories themselves —
  those need a real or containerised Cassandra to test honestly.
- End-to-end tests across all 20 services in CI (the demo runbook covers this
  path against real Cassandra/Kafka locally).
- Android UI behaviour beyond unit tests (one instrumented login test exists).
- Load and latency numbers: see the next section.

---

## 6. Honest limits

Carried deliberately from `docs/demo/MONZO_DEMO.md`, because knowing what a
system does *not* do is part of the engineering:

- Secondary indexes / `ALLOW FILTERING` are dev-scale. Production adds explicit
  lookup tables and per-account materialized balance rows.
- The SSE hub is in-process, so notification-service is single-instance until
  the fan-out moves to pub/sub.
- The outbox relay polls; production would drive it from CDC or a
  bounded-delay scheduler with a partition-keyed producer.
- `/v1/stream` and `/v1/feed` authenticate with `X-User-ID` for demo purposes;
  the real path is JWT + per-user ACLs.
- No load test is committed yet, so there are no p50/p99 figures for the
  authorization path — only a sub-second design budget and per-request latency
  recorded on every authorization row (`LatencyMs`).
- Grafana/Prometheus run unauthenticated locally.

---

## 7. Known gaps and the next work

Found while hardening the money paths — listed because unfound bugs are worse
than admitted ones:

- **The hold is released before the settlement debit is known to have been
  booked.** On the success path the hold is freed once the settlement event is
  durably in the outbox, and the ledger books the real debit from that event. If
  a concurrent spend takes the funds in that window, the ledger now *refuses* the
  booking (fail loud) rather than overdrawing the account — but the payment is
  left SETTLED with its debit unbooked, so that window still needs an
  operator-visible reconciliation alarm. Settling the hold at the ledger instead
  of releasing it, and having the ledger mark the payment as booked at that
  point, is the design that removes the window entirely.
- **A refused booking is visible but not retried.** The ledger's Kafka consumer
  logs the error and advances the offset, so an insufficient-funds refusal is not
  redelivered. It is no longer silent — a `BOOKING_REFUSED` integrity event and a
  SEV1 incident are filed against the account — but nothing replays it yet: the
  next step is a DLQ with an explicit replay path (the claim compensation already
  makes redelivery safe).
- **The PSD2 low-value exemption omits its cumulative limits.** The exemption is
  evaluated per presentment against a £30 ceiling; PSD2 also caps the sequence
  (five low-value presentments or €100 since the last strong authentication),
  which needs a per-account counter row. The exemption policy takes the counters
  as inputs, so wiring them is a data change — but until then an unusual run of
  small online payments is exempt more often than the regulation allows.
- **The challenge expiry sweep runs per instance.** Each card-service instance
  sweeps the same hourly buckets every minute; that is safe (the challenge
  transition is single use, so one instance decides and the rest are no-ops) but
  wasteful, and the challenge row itself is never deleted — only its expiry-index
  entry is. A leader election or a Cassandra-side TTL on the challenge row would
  tighten both.
- **A merchant-initiated exemption trusts the merchant's own history.** The claim
  is only honoured when that card already has a captured presentment at the same
  merchant, which stops a merchant exempting itself out of nowhere — but a
  compromised acquirer that has transacted once can still assert the flag.
  Production links the follow-on to the initial authenticated transaction id.
- **No committed load test.** The authorization path records `LatencyMs` per
  decision, but there is no p50/p99 evidence under concurrency; that is the next
  measurement to add (k6 against the gateway, with Cassandra and Kafka down as a
  separate run to prove the fail-closed behaviour holds under load).
- **Bulk-incrementing the repository is no substitute for review history.**
  The git log shows feature rounds, not the small reversible steps a reviewer
  would normally follow. The ADRs are the durable record of intent instead.

## 8. If you only care about one thing, start here

| Your interest | Go to |
|---|---|
| Payments, money movement | `services/payment-service`, `services/ledger-service`, ADR-003/005/007 |
| Cards, real-time authorization | `services/card-service/internal/service/authorization_service.go`, ADR-021 |
| Strong customer authentication, card refunds | `services/card-service/internal/service/{sca_service,refund_service}.go`, `shared/schemes/sca.go`, ADR-037 |
| Financial crime | `services/fraud-service/internal/service`, `shared/kyc` |
| Platform / reliability | `services/control-plane-service`, `shared/engplatform`, `k8s/` |
| Data | `cassandra/init/schema.cql`, `shared/cassandra`, `shared/datainfra` |
| Customer product surface | `android/app/src/main/java/com/nexora/app/feature` (13 modules) |
