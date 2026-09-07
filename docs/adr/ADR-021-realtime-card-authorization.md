# ADR-021: Real-Time Card Authorization Pipeline (authorize → capture/void)

## Status

Accepted — **implemented and verified end-to-end**.

## Context

Card payments are the real-time surface of a digital bank: from the moment a
card is tapped at a terminal the platform has a few hundred milliseconds to
decide approve/decline, reserve funds, and notify the app. The platform had a
generic payment lifecycle but no card-presentment path at all, and money
movement was unreliable (see ADR-005 and the ledger fixes).

ADR-020 chose polling for payment status updates; the card surface has
stricter latency expectations and polling every second is not good enough for
a "transaction happened in the app seconds after the tap" demo.

## Decision

Implement a **synchronous, decision-first authorization pipeline**:

1. **card-service** owns the lifecycle. `POST /v1/cards/{id}/authorize`
   receives the merchant presentment (ISO-8583-shaped request), then:
   - fast local checks: card status and currency;
   - real spend today is computed from actual ledger DEBIT entries;
   - **fraud-service** scores the request against the user's real stored
     decision history (velocity window, amount pattern, geo, merchant) plus
     the real card limits — no hardcoded demo values;
   - a **funds reservation** is created in ledger-service only if the risk
     decision approves — the reservation is balance-checked against the real
     ledger (available = cleared − active reservations);
   - the durable `card_authorizations` row records decision, risk score,
     reservation and the measured latency.
2. Approve/decline events are published to Kafka
   (`nexora.card.authorization.approved|declined`) with the user id, so
   notification-service can push and persist in real time.
3. Capture/void settle or release the hold in the ledger later; the captured
   event carries the real `balance_after` from the double-entry book.

### Exactly-once money movement

Lifecycle events for the same payment (confirmed + settled) can arrive twice.
Ledger booking is gated by an **LWT claim row** (`ledger_payment_marks`,
`INSERT ... IF NOT EXISTS`) so a payment books exactly once even if Kafka
redelivers or both lifecycle events race. The per-account single-writer lock
makes reserve/settle/release atomic within an account.

### Mobile realtime delivery

ADR-020's polling model is superseded: notification-service consumes card and
payment events from Kafka, persists a notification, and fans out to
**Server-Sent Events** (`GET /v1/stream?user_id=`) plus the home-screen feed
(`GET /v1/feed`). Every item is a real row in Cassandra; the payloads carry
the real merchant, amount and post-transaction balance.

### Balance answers from the ledger

The account-service balance endpoint now reads the ledger's live balance
breakdown (cleared, pending reservations, available) instead of a possibly
stale column on the account row. The ledger is the single source of truth.

## Consequences

- Sub-second decision path (typical authorizations complete in tens of
  milliseconds) with all persistence durable.
- Exactly-once booking removes double-credit/double-debit classes of bugs that
  plagued the earlier fire-and-forget design.
- SSE keeps one open connection per device; pushes and feed read from the same
  persisted rows, so both surfaces agree.
