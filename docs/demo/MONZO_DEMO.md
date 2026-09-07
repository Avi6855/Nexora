# MonzoBank — Senior Backend Engineer Demo Runbook

Everything in this runbook reads and writes **real data**. There are no mocked
responses, no hardcoded balances, and no demo values baked into services: every
balance shown is computed by the ledger from double-entry rows, every fraud
decision is scored against history stored in Cassandra, every feed/SSE item is
a row the notification service persisted after consuming a Kafka event.

Watch it live:

- Grafana dashboard: http://localhost:3000 → **MonzoBank Platform** (admin / monzo)
- Prometheus: http://localhost:9090 · Kafka UI: http://localhost:8080

---

## 1. What this platform is

17 Go microservices + Cassandra + Kafka + Envoy. The demo focuses on the
surface Monzo is famous for — **the real-time card authorization pipeline** —
plus the correctness machinery underneath it:

1. **Transactional outbox** (`shared/outbox`) — payment state changes and
   their events commit atomically in one Cassandra logged batch; a relay
   drains to Kafka with retries + DLQ. No silently-lost events.
2. **Real-time card authorization** — tap → card checks → risk engine (real
   history + real limits) → balance-checked fund reservation → durable
   decision row → Kafka → push + SSE to the app. Sub-second.
3. **Exactly-once money movement** — ledger booking gated by an LWT claim row;
   a payment can only book once even with duplicate/redelivered events.
4. **Ledger as the single source of truth** — the app-facing balance endpoint
   reads the ledger live (cleared / pending / available).
5. **Observability** — every instrumented service exposes Prometheus /metrics;
   Prometheus + Grafana with a provisioned platform dashboard.

Flow map:

```
 Android / curl
   │  POST /v1/cards/{id}/authorize  (card-service :8087)
   ▼
 card-service
   ├─ card row check (Cassandra: status/currency/limits)
   ├─ spend today = Σ real DEBIT ledger entries (ledger-service :8084)
   ├─ fraud-service /v1/fraud/authorization/evaluate (:8089)
   │     scores vs REAL history in Cassandra → APPROVE/REVIEW/DECLINE
   ├─ ledger /v1/ledger/reserve — balance-checked hold (real balance)
   ├─ persist card_authorizations row (decision, risk, latency, reservation)
   └─ Kafka: nexora.card.authorization.approved|declined (user_id attached)
         ▼
 notification-service (:8090)
   ├─ persist notification + feed row (Cassandra)
   └─ SSE /v1/stream?user_id=  +  feed GET /v1/feed?user_id=   → app
 capture/void later → ledger settles/releases hold → balance_after event → SSE
```

---

## 2. Pre-flight

```bash
# Docker Desktop running (see Troubleshooting). Start the platform:
docker-compose up -d
docker ps --format "{{.Names}}\t{{.Status}}" | sort      # all healthy

# The schema lives in cassandra/init/schema.cql and is applied by the
# cassandra container on first boot. If the DB predates schema changes:
docker exec nexora-cassandra cqlsh -e "DROP KEYSPACE IF EXISTS nexora;"
docker cp cassandra/init/schema.cql nexora-cassandra:/tmp/schema.cql
docker exec nexora-cassandra cqlsh -f /tmp/schema.cql

# Ports: account 8083 · ledger 8084 · payment 8085 · card 8087 · fraud 8089 ·
# notification 8090 · prometheus 9090 · grafana 3000 (admin/monzo) · kafka-ui 8080
```

---

## 3. The 60-second live demo (automated)

```bash
bash scripts/demo_card_e2e.sh
```

It prints PASS lines for every assertion. What it actually does, end to end:

| Step | What happens | Real data proof |
|---|---|---|
| 1. Create account | `accounts` row for the demo user | row exists |
| 2. Fund £1,000 | **double-entry** credit (clearing account debited) | `ledger_entries` 2 rows, `balance_after` = 100000 |
| 3. Balance | account-service → ledger REST → **ledger truth** | available_balance 100000 |
| 4. Virtual card | `cards` row (currency/limits persisted) | row exists |
| 5. Open SSE | `GET /v1/stream?user_id=` | `event: ready` |
| 6. Authorize £35 @ TESCO | card checks → risk engine → real fund reservation | APPROVED, risk LOW, reservation_id set, latency ms |
| 7. Balance mid-hold | ledger sums ACTIVE reservations | available drops to **96500**, current stays 100000 |
| 8. Feed | notification consumed the approved event | feed JSON shows TESCO, real merchant/amount |
| 9. Overdraw £20k | risk engine declines (daily-limit breach scored live) | DECLINED, risk_reasons `["VELOCITY_BREACH"]` |
| 9b. £1,500 under limit, over balance | risk passes → real available-balance check | DECLINED `insufficient_funds` |
| 10. Capture £35 | settle reservation → ledger books the debit | status CAPTURED |
| 11. Balance post-capture | ledger `balance_after` | current = **96500**, reserved 0 |
| 12. SSE pushes | live pushes for every decision incl. "New balance £965.00" | real rows + real balance |
| 13. Roundup | captured payment sweeps spare change into the round-up pot (only when the spend isn't already on a pound boundary — authorise £35.20, not £35.00, to see an 80p sweep) | real pot deposit via `pot.deposit` |

### Watching it in Grafana

Open http://localhost:3000 → **MonzoBank Platform**. During/after the script:

- `Card authorization decisions` — approved vs declined bars tick up.
- `Authorization endpoint latency p95` — the real decision latency (tens of ms).
- `Requests per second`, `p95 latency per service`, `Top endpoints`.

Prometheus directly:

```bash
curl -s "http://localhost:9090/api/v1/query?query=card_authorization_decisions_total" | head
curl -s "http://localhost:9090/api/v1/query?query=histogram_quantile(0.95,sum by(le)(rate(http_request_duration_seconds_bucket{service=\"card-service\",route=\"/v1/cards/:id/authorize\"}[5m])))"
```

---

## 4. Verifying the money is real (Cassandra)

```bash
CQL="docker exec nexora-cassandra cqlsh -e"

# Double-entry rows (balance_before → balance_after):
$CQL "USE nexora; SELECT account_id, entry_type, amount, balance_before, balance_after, created_at FROM ledger_entries LIMIT 20;"

# The fund hold + its settle (status transitions):
$CQL "USE nexora; SELECT reservation_id, amount, status, created_at, settled_at FROM reservations LIMIT 20;"

# Authorization decision records (real risk context, latency):
$CQL "USE nexora; SELECT merchant, amount, decision, decline_reason, risk_level, latency_ms FROM card_authorizations LIMIT 20;"

# Notifications = feed items (pushed + persisted):
$CQL "USE nexora; SELECT notification_type, title, status, created_at FROM notifications LIMIT 20;"

# Outbox: every payment state change was atomically committed with its event,
# then drained to Kafka (state = PUBLISHED):
$CQL "USE nexora; SELECT event_type, topic, state, attempts, created_at FROM outbox_events LIMIT 20;"
```

### Roundups (new)

```bash
# 1. Create a pot with round-ups enabled (or toggle an existing one):
curl -s -X PUT -H "Content-Type: application/json" -H "X-User-ID: <USER>" \
  -d '{"enabled":true}' http://localhost:8088/v1/pots/<POT_ID>/roundup

# 2. Authorise + capture a non-round amount (£35.20 → 80p round-up):
#    (run the authorize + capture steps from the table above)

# 3. The pot-service roundups consumer books a REAL ledger transfer:
$CQL "USE nexora; SELECT pot_id, current_amount FROM pots WHERE pot_id = <POT_ID>;"
# pot balance includes the 80p spare change, swept exactly once
# (dupes are blocked by the roundup_processed LWT claim)

# 4. Spending controls (new):
curl -s -X PUT -H "Content-Type: application/json" -H "X-User-ID: <USER>" \
  -d '{"online_enabled":false}' http://localhost:8087/v1/cards/<CARD_ID>/controls
# next e-commerce presentment declines with reason online_payments_disabled
```

### Payment lifecycle through the outbox (money-in + exactly-once)

```bash
# 1. Create a payment for the demo account (X-User-ID header; GBP pennies)
PAY=$(curl -s -X POST -H "Content-Type: application/json" \
  -H "X-User-ID: 5f0c2e10-3c4a-4a5b-9c6d-7e8f9a0b1c2d" \
  -d "{\"account_id\":\"<ACCOUNT_ID>\",\"amount\":5000,\"currency\":\"GBP\",\"payment_type\":\"CARD\",\"idempotency_key\":\"demo-$(date +%s)\"}" \
  http://localhost:8085/v1/payments)
PID=$(echo "$PAY" | grep -o '"payment_id":"[^"]*"' | cut -d'"' -f4)

# 2. Drive the state machine (each transition = one atomic outbox row):
curl -s -X POST http://localhost:8085/v1/payments/$PID/authorize >/dev/null
curl -s -X POST http://localhost:8085/v1/payments/$PID/process  >/dev/null
# process auto-confirms + settles on provider success → ledger books the credit

# 3. Exactly once: confirmed AND settled fired, but the ledger booked once:
$CQL "USE nexora; SELECT account_id, entry_type, amount, balance_after FROM ledger_entries WHERE account_id = <ACCOUNT_ID> ALLOW FILTERING;"
$CQL "USE nexora; SELECT payment_id, event_type FROM ledger_payment_marks;"
```

---

## 4c. The intelligence demo (Financial Intelligence Platform + security)

After running a few card payments through the 60-second demo (the captured
authorizations feed insights-service):

```bash
TOKEN="..."          # from the demo login
ACCOUNT="..."        # the account the card spends against

# Detected subscriptions + price hikes (Netflix twice => active; a higher
# second amount emits a PRICE_HIKE alert)
curl -s "http://localhost:8000/v1/insights/subscriptions?account_id=$ACCOUNT" -H "Authorization: Bearer $TOKEN"

# Safe-to-spend: balance - upcoming bills - forecast spend - buffer
curl -s "http://localhost:8000/v1/insights/safe-to-spend?account_id=$ACCOUNT" -H "Authorization: Bearer $TOKEN"

# Intelligence alerts (also arrive in the app feed via SSE)
curl -s "http://localhost:8000/v1/insights/alerts?account_id=$ACCOUNT" -H "Authorization: Bearer $TOKEN"

# Outbound transfer scam check (run inside the network; needs internal token)
docker exec nexora-payment-service sh -c 'true' # gate runs automatically on POST /v1/payments
# A BLOCK decision surfaces in the app as "Payment stopped for your safety" (403
# blocked_by_risk_engine on POST /v1/payments).

# Emergency lockdown: block all money out, keep money in working
curl -s -X PUT "http://localhost:8000/v1/accounts/$ACCOUNT/lockdown" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"lockdown_enabled": true}'
# Card presentments now decline with account_locked; transfers out get 403;
# the ledger refuses money-OUT bookings as defense-in-depth. Flip the flag
# back to false to unlock. In the app: Security -> Emergency Lockdown.
```

### 4d. The orchestration demo (disputes, delegation, reliability)

```bash
# Report a problem with a card payment (get ENTRY from a recent CARD_PAYMENT)
curl -s -X POST "http://localhost:8000/v1/disputes" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"entry_id":"'"$ENTRY"'","reason":"NOT_RECEIVED","description":"Never arrived"}'
# -> case JSON with stage + eligibility verdict. Add evidence, then walk it:
curl -s -X POST "http://localhost:8000/v1/disputes/$CASE_ID/evidence" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"evidence_type":"RECEIPT","filename":"receipt.png","content":"..."}'
curl -s "http://localhost:8000/v1/disputes/$CASE_ID/events" -H "Authorization: Bearer $TOKEN"
# In the app: Transaction detail -> "Report a problem"; Profile -> Disputes.

# Delegated access: give Avi view-only access for 7 days (never money movement)
curl -s -X POST "http://localhost:8000/v1/consent/grants" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"delegate_email":"avi@example.com","label":"Babysitter","scopes":["VIEW_BALANCE","VIEW_TRANSACTIONS"],"duration_days":7}'
curl -s "http://localhost:8000/v1/consent/grants" -H "Authorization: Bearer $TOKEN"
# Every scoped access is audited: GET /v1/consent/grants/{id}/audit
# In the app: Profile -> Delegated access (or Security -> Delegated access).

# Platform reliability: the live dependency health graph
curl -s "http://localhost:8000/v1/control/graph" | python -m json.tool
# Nodes, depends_on edges, blast radius, overall status + auto-throttle
# advisories. In the app: Profile -> System status.

# Time travel: what was the balance at 14:37 on 1 Sep?
curl -s -X POST "http://localhost:8000/v1/replay/time-travel" \
  -H "Content-Type: application/json" \
  -d '{"account_id":"'"$ACCOUNT"'","at_time":"2026-09-01T14:37:22Z"}' | python -m json.tool
# Two independent reconstructions (backwards balance_after vs forward replay)
# plus every entry that produced the state — divergence field set if they differ.

# Salary intelligence + runway (needs detected income + category history)
curl -s "http://localhost:8000/v1/insights/salary/status?account_id=$ACCOUNT" -H "Authorization: Bearer $TOKEN"
curl -s "http://localhost:8000/v1/insights/runway?account_id=$ACCOUNT&extra_monthly=30000&horizon_months=6" -H "Authorization: Bearer $TOKEN"
# Runway strip lives on Home; salary alerts (increase/late) arrive via notifications.
```

---

## 5. Senior-level talking points (say these, briefly, when asked)

**Distributed transactions, no dual write.** Payment state + event commit in
one Cassandra logged batch (outbox). Relay publishes `acks=all`, retries on
every poll, DLQ after max attempts. Delivery is at-least-once — so consumers
dedupe, which is why booking is gated by an LWT claim row.

**Exactly-once money movement.** `ledger_payment_marks` LWT means the ledger
books a payment once even if confirmed+settled race or Kafka redelivers.
Per-account single-writer lock makes reserve→settle atomic. Cassandra alone
cannot give serializable cross-row transactions — you get it by scoping writes
per account and claiming idempotency keys with CAS.

**Fail-closed real-time authorization.** The decision path is synchronous and
sub-second: risk engine unavailable or funds unverifiable → decline, never
silently approve. Fraud inputs (spend today, card limits, history) come from
the DB on every request — nothing cached or hardcoded.

**Balance is derived, not stored.** The app-facing balance endpoint reads the
ledger live (cleared = Σ entries, pending = Σ active reservations, available =
cleared − pending). Card hold at tap → available drops instantly; capture →
cleared drops with the real `balance_after`.

**SSE over polling.** One open HTTP connection per device; every push is also
persisted, so reconnect replays from the feed — both surfaces agree. (The
original ADR-020 polling design is superseded by ADR-021.)

**Observability from day one.** Bounded-cardinality route labels (UUIDs →
`:id`), request counters + latency histograms on every instrumented service,
domain counters (authorization decisions by outcome/reason).

### Honest limits (volunteer these — it reads as senior)

- Secondary indexes / `ALLOW FILTERING` are dev-scale; production would add
  explicit lookup tables and per-account materialized balance rows.
- The SSE hub is in-process → single notification-service instance; scale-out
  needs a pub/sub or Redis fan-out.
- `/v1/stream` and `/v1/feed` are demo-auth'd via `X-User-ID`; wire real
  tokens + per-user ACLs (see ADR-019 Android architecture).
- Outbox relay polls; production would back it with CDC or a bounded-delay
  scheduler and publish via a partition-keyed producer.
- No auth on Grafana/Prometheus ports (demo only).

---

## 6. What was changed / added (repo tour)

| Area | Files |
|---|---|
| Outbox | `shared/outbox/*`, `cassandra/init/schema.cql` (`outbox_events`), `services/payment-service/internal/{repository,events,service}/*`, `docs/adr/ADR-005-outbox.md` |
| Card auth | `services/card-service/internal/{domain/authorization.go, repository/authorization_repository.go, clients, service/authorization_service.go, events}`, `services/fraud-service/internal/{domain/authorization.go, service/authorization_evaluator.go, repository}`, `cassandra/init/schema.cql` (`card_authorizations`) |
| Ledger truth | `services/ledger-service/internal/{repository,service,events}/*`, `services/account-service/internal/{clients/ledger.go, service/account_service.go}` |
| Realtime | `services/notification-service/internal/{realtime/hub.go, events/consumer.go, domain/feed.go, service/realtime_consumer.go}`, `/v1/stream` + `/v1/feed` |
| Financial Intelligence Platform | `services/insights-service/**` (consumer + detectors + projections + API), `cassandra/init/schema.cql` (`subscriptions`, `account_baselines`, `account_income`, `insights_alerts`), notification-service `nexora.insights.alerts` consumer, Android `feature/insights/**` |
| Scam intelligence + lockdown | `services/fraud-service/internal/service/transfer_evaluator.go` + `/v1/fraud/transfer/evaluate`, payment-service pre-flight gate (`internal/clients`), account lockdown (`accounts.lockdown_enabled`, `PUT /v1/accounts/{id}/lockdown`), enforcement in transfer-service / card-service / ledger `BookTransfer` |
| Observability | `shared/telemetry/{prom.go,httpmw.go}`, `/metrics` in service mains, `observability/` (prometheus.yml + grafana provisioning + dashboard), `docker-compose.yml` |
| Docs | `docs/adr/ADR-021-*`, `docs/adr/ADR-022-*`, `docs/demo/MONZO_DEMO.md`, `scripts/demo_card_e2e.sh` |

---

## 7. Troubleshooting

**Docker Desktop stuck ("Insufficient system resources" vpnkit error).**

```bash
taskkill //F //IM "Docker Desktop.exe" //IM "com.docker.backend.exe" //IM "com.docker.build.exe" //IM "vpnkit.exe" //IM "com.docker.service.exe" 2>/dev/null
wsl --shutdown
# reopen Docker Desktop; wait for the engine:
for i in $(seq 1 24); do docker ps >/dev/null 2>&1 && break; sleep 5; done
```

**Rebuild a service after code changes.**

```bash
docker-compose build card-service && docker-compose up -d card-service
```

**Metrics empty in Grafana?** Give Prometheus ~15s; `curl localhost:9090/api/v1/targets`
should show all targets `health: "up"`.
