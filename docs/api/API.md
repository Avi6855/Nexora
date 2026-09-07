# API Reference

## Overview

Nexora provides REST/JSON APIs for client applications and gRPC APIs for internal service communication.

## Base URL

```
http://localhost:8000
```

## Authentication

### JWT Tokens

All authenticated endpoints require a valid JWT token in the Authorization header:

```
Authorization: Bearer <access_token>
```

### Token Generation

```http
POST /api/v1/auth/login
Content-Type: application/json

{
    "email": "user@example.com",
    "password": "securePassword123",
    "device_id": "device-abc"
}
```

**Response:**
```json
{
    "success": true,
    "data": {
        "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "expires_at": 1735689600
    }
}
```

## Endpoints

### Auth Endpoints

#### Register
```http
POST /api/v1/auth/register
Content-Type: application/json

{
    "email": "user@example.com",
    "password": "securePassword123",
    "first_name": "John",
    "last_name": "Doe",
    "phone": "+447700900000"
}
```

**Response:**
```json
{
    "success": true,
    "data": {
        "user_id": "usr-123",
        "email": "user@example.com",
        "status": "PENDING_VERIFICATION"
    }
}
```

#### Verify OTP
```http
POST /api/v1/auth/verify-otp
Content-Type: application/json

{
    "email": "user@example.com",
    "otp": "123456"
}
```

#### Refresh Token
```http
POST /api/v1/auth/refresh
Content-Type: application/json

{
    "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

### Account Endpoints

#### List Accounts
```http
GET /api/v1/accounts
Authorization: Bearer <token>
```

**Response:**
```json
{
    "success": true,
    "data": [
        {
            "account_id": "acc-123",
            "user_id": "usr-456",
            "name": "Main Account",
            "type": "PERSONAL",
            "currency": "GBP",
            "status": "ACTIVE",
            "balance": 10000,
            "created_at": "2024-01-01T00:00:00Z"
        }
    ]
}
```

#### Get Account
```http
GET /api/v1/accounts/:account_id
Authorization: Bearer <token>
```

#### Get Balance
```http
GET /api/v1/accounts/:account_id/balance
Authorization: Bearer <token>
```

**Response:**
```json
{
    "success": true,
    "data": {
        "account_id": "acc-123",
        "balance": 10000,
        "pending": 3000,
        "available": 7000,
        "currency": "GBP",
        "total_debits": 5000,
        "total_credits": 15000
    }
}
```

### Payment Endpoints

#### Create Payment
```http
POST /api/v1/payments
Authorization: Bearer <token>
Content-Type: application/json

{
    "idempotency_key": "unique-key-123",
    "account_id": "acc-123",
    "payment_type": "CARD",
    "amount": 10000,
    "currency": "GBP",
    "counterparty_id": "cp-456",
    "counterparty_name": "John Smith",
    "reference": "Invoice payment",
    "metadata": {
        "source": "mobile_app",
        "version": "1.0"
    }
}
```

**Response:**
```json
{
    "success": true,
    "data": {
        "payment_id": "pay-789",
        "account_id": "acc-123",
        "state": "CREATED",
        "amount": 10000,
        "currency": "GBP",
        "counterparty_id": "cp-456",
        "reference": "Invoice payment",
        "created_at": "2024-01-01T00:00:00Z"
    }
}
```

#### Get Payment
```http
GET /api/v1/payments/:payment_id
Authorization: Bearer <token>
```

#### Process Payment
```http
POST /api/v1/payments/:payment_id/process
Authorization: Bearer <token>
```

#### Cancel Payment
```http
POST /api/v1/payments/:payment_id/cancel
Authorization: Bearer <token>
```

### Transaction Endpoints

#### List Transactions
```http
GET /api/v1/accounts/:account_id/transactions?limit=20&offset=0
Authorization: Bearer <token>
```

**Response:**
```json
{
    "success": true,
    "data": [
        {
            "transaction_id": "txn-123",
            "account_id": "acc-456",
            "type": "TRANSFER",
            "status": "COMPLETED",
            "amount": 5000,
            "currency": "GBP",
            "description": "Test transfer",
            "created_at": "2024-01-01T00:00:00Z"
        }
    ],
    "pagination": {
        "total": 100,
        "limit": 20,
        "offset": 0,
        "has_more": true
    }
}
```

#### Get Transaction
```http
GET /api/v1/transactions/:transaction_id
Authorization: Bearer <token>
```

### Ledger Endpoints

#### Get Ledger Entries
```http
GET /api/v1/ledger/accounts/:account_id/entries?limit=50
Authorization: Bearer <token>
```

Optional filters (Monzo-style search & categories):
```http
GET /api/v1/ledger/accounts/:account_id/entries?query=tesco&category=GROCERIES&type=DEBIT&limit=50
```
- `query` — case-insensitive substring match on the description
- `category` — one of `GROCERIES`, `EATING_OUT`, `TRANSPORT`, `SHOPPING`, `BILLS`, `ENTERTAINMENT`, `TRAVEL`, `HEALTH`, `SAVINGS`, `TRANSFERS`, `INCOME`, `OTHER` (inferred from the description at booking time)
- `type` — `DEBIT` or `CREDIT`

Entries include a `category` and any user `note`.

#### Update Transaction Note
```http
PUT /api/v1/ledger/entries/:entry_id/note
Authorization: Bearer <token>
Content-Type: application/json

{
    "note": "Team lunch"
}
```
Notes live in `transaction_notes` (the ledger stays append-only). An empty
string clears the note. Max 500 characters; only the entry's owner may write.

#### Create Double Entry
```http
POST /api/v1/ledger/transactions
Authorization: Bearer <token>
Content-Type: application/json

{
    "debit_account_id": "acc-123",
    "credit_account_id": "acc-456",
    "amount": 5000,
    "currency": "GBP",
    "description": "Transfer",
    "idempotency_key": "unique-key-456",
    "transaction_type": "TRANSFER"
}
```

### Card Endpoints

#### Update Spending Controls
```http
PUT /api/v1/cards/:card_id/controls
Authorization: Bearer <token>
Content-Type: application/json

{
    "online_enabled": false,
    "atm_enabled": true,
    "gambling_block_enabled": true
}
```
All fields optional — only provided flags change. Controls are enforced live
during card authorization (before the risk engine):
- `online_enabled: false` → e-commerce presentments decline with `online_payments_disabled`
- `atm_enabled: false` → ATM presentments decline with `atm_withdrawals_disabled`
- `gambling_block_enabled: true` → gambling merchants decline with `gambling_block_active`

The decision record and the SSE push surface the specific reason, like the
real Monzo app ("we declined it because you turned off online payments").

### Pot Endpoints

#### Toggle Round-ups
```http
PUT /api/v1/pots/:pot_id/roundup
Authorization: Bearer <token>
Content-Type: application/json

{
    "enabled": true
}
```
When enabled, pot-service consumes captured card authorization events and
sweeps the spare change (spend rounded up to the next £1) into the pot as a
real ledger transfer. Exactly-once via the `roundup_processed` claim table;
only one pot per user may have round-ups on.

### Account Endpoints

#### Emergency Lockdown (money out blocked, money in allowed)
```http
PUT /api/v1/accounts/:account_id/lockdown
Authorization: Bearer <token>
Content-Type: application/json

{
    "lockdown_enabled": true
}
```
Enforced at three layers: payment-service refuses outbound payments from
locked accounts (403 `blocked_by_risk_engine`), transfer-service refuses
transfers out (403), and the ledger independently refuses money-OUT bookings
against locked accounts (403 `account_locked`) as defense-in-depth. Card
presentments against locked accounts decline with `account_locked`.

### Insights Endpoints (Financial Intelligence Platform)

#### Safe to Spend
```http
GET /api/v1/insights/safe-to-spend?account_id=<uuid>
Authorization: Bearer <token>
```
Computes safe-to-spend from the real ledger balance, detected subscriptions
due before payday, 30-day category baselines and detected income:

```json
{
    "account_id": "…",
    "available_now": 125000,
    "upcoming_bills": 3599,
    "forecast_spend_month": 88000,
    "forecast_daily": 2933,
    "buffer": 12000,
    "safe_to_spend": 74000,
    "safe_to_spend_daily": 2642,
    "days_to_payday": 28,
    "expected_income": 240000,
    "category_totals": [{"category": "GROCERIES", "total_30d": 32000, "daily_avg": 1066}]
}
```

#### Detected Subscriptions
```http
GET /api/v1/insights/subscriptions?account_id=<uuid>
```
Returns recurring payments detected over the event stream, with price-hike
flags (`price_hike_pct >= 10` on an active subscription emits a one-time
`PRICE_HIKE` alert).

#### Insight Alerts
```http
GET /api/v1/insights/alerts?account_id=<uuid>&limit=50
```
Alert types: `NEW_SUBSCRIPTION`, `PRICE_HIKE`, `INCOME_DETECTED`,
`SPENDING_ANOMALY`. Alerts are also published to `nexora.insights.alerts`
and surface in the app notification feed + SSE stream.

### Fraud Endpoints (outbound transfers)

#### Evaluate Transfer Risk
```http
POST /api/v1/fraud/transfer/evaluate
X-Internal-Token: <internal token>
Content-Type: application/json

{
    "request_id": "idempotency-key",
    "user_id": "…",
    "account_id": "…",
    "amount": 25000,
    "counterparty_name": "Netflix",
    "reference": "subscription"
}
```
Scam-intelligence decision over the user's real decision history (new payee,
p95 amount pattern, 24h velocity, rapid succession, late-night timing).
Actions: `ALLOW`, `REVIEW` (in-app warning), `STEP_UP`, `BLOCK` (payment
refused before any state is persisted).

### Health Endpoints

#### Service Health
```http
GET /health
```

**Response:**
```json
{
    "status": "healthy",
    "service": "payment-service",
    "version": "1.0.0",
    "timestamp": "2024-01-01T00:00:00Z"
}
```

### Consent Endpoints (Delegated Access)

#### Create Grant
```http
POST /api/v1/consent/grants
Authorization: Bearer <token>

{
    "delegate_email": "avi@example.com",
    "label": "Babysitter",
    "scopes": ["VIEW_BALANCE", "VIEW_TRANSACTIONS"],
    "duration_days": 7
}
```
Creates a time-bound (≤90 days), capability-scoped grant. Scopes:
`VIEW_BALANCE`, `VIEW_TRANSACTIONS`, `DOWNLOAD_STATEMENTS` — money movement is
never delegable. The delegate sees scoped data whenever their own session
overlaps an ACTIVE grant window; every evaluation is audited.

#### List / Revoke / Audit
```http
GET    /api/v1/consent/grants
POST   /api/v1/consent/grants/{id}/revoke
GET    /api/v1/consent/grants/{id}/audit
```

#### Evaluate (internal)
```http
POST /api/v1/consent/evaluate
X-Internal-Token: <internal token>

{"grant_id": "…", "owner_user_id": "…", "scope": "VIEW_BALANCE", "resource_id": "…"}
```
Returns `{"allowed": true|false, "reason": "…"}` and writes an audit record
for both allowed and refused access.

### Dispute Endpoints (chargeback orchestration)

#### Report a Problem
```http
POST /api/v1/disputes
Authorization: Bearer <token>

{
    "entry_id": "<ledger entry uuid>",
    "reason": "NOT_RECEIVED",
    "description": "Merchant says delivered, it never arrived"
}
```
The eligibility engine applies scheme rules at creation: category must be
disputable (card payments, direct debits), amount positive, transaction
inside the 120-day chargeback window. Response includes the case with its
`stage`, deadlines and `provisional_credit` eligibility. Duplicate disputes
on one transaction → `409`.

#### Case Lifecycle
```http
GET  /api/v1/disputes?limit=50
GET  /api/v1/disputes/{id}
POST /api/v1/disputes/{id}/evidence      {"evidence_type":"RECEIPT","filename":"…","content":"…"}
GET  /api/v1/disputes/{id}/evidence
GET  /api/v1/disputes/{id}/events
POST /api/v1/disputes/{id}/resolve       (internal: {"resolution":"REFUNDED","note":"…"})
POST /api/v1/disputes/tick               (internal: advance deadlines)
```
Stages: `SUBMITTED → ELIGIBILITY → AWAITING_EVIDENCE → MERCHANT_RESPONSE →
UNDER_REVIEW → RESOLVED`. Evidence upload advances
`AWAITING_EVIDENCE → MERCHANT_RESPONSE`; the deadline ticker escalates stalled
cases. A `REFUNDED` resolution books the money back through the ledger and
emits `nexora.dispute.resolved`.

### Control Endpoints (dependency health graph)

```http
GET  /api/v1/control/graph          (live dependency graph + blast radius + advisories)
POST /api/v1/control/reports        (internal: per-service health report)
GET  /api/v1/control/throttle/{svc} (internal: adaptive shedding decision)
```
The graph propagates health downstream→upstream: when a critical dependency
degraded, dependent services get `shed_pct` instructions — non-critical
traffic (analytics, notifications) is shed before the failure cascades. The
payment- and ledger-service enforce these via the shared shedding middleware.

### Replay Endpoints (time-travel debugging)

```http
POST /api/v1/replay/time-travel

{"account_id": "…", "at_time": "2026-09-01T14:37:22Z"}
```
Reconstructs the account balance at instant T from the append-only ledger,
computed two independent ways (stored `balance_after` walking backwards vs a
forward signed replay) — deterministic, with a `divergence` field when the
two disagree. `ledger_is_live` reports whether any entries were booked after
the snapshot.

### Insights: income lifecycle + runway

```http
GET /api/v1/insights/salary/status?account_id=<uuid>
GET /api/v1/insights/runway?account_id=<uuid>&extra_monthly=30000&horizon_months=6
```
Salary status reports source, amounts, next expected payday, days late and
last increase % (a `SALARY_LATE` alert fires automatically when the expected
date passes by 2+ days). Runway answers "what if my income stopped": months
of coverage on essentials-only and all-in spend, a depletion date, verdict
text, and the optional what-if scenario projection.

## Error Responses

### Standard Error Format
```json
{
    "error": "ERROR_CODE",
    "message": "Human-readable error message"
}
```

### Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| VALIDATION_ERROR | 400 | Request validation failed |
| UNAUTHORIZED | 401 | Authentication required |
| NOT_FOUND | 404 | Resource not found |
| IDEMPOTENCY_CONFLICT | 409 | Request already processed with different payload |
| INSUFFICIENT_FUNDS | 402 | Insufficient funds for transaction |
| INVALID_STATE_TRANSITION | 422 | Invalid state transition |
| RATE_LIMITED | 429 | Too many requests |
| INTERNAL_ERROR | 500 | Internal server error |
| SERVICE_UNAVAILABLE | 503 | Service temporarily unavailable |

## Idempotency

All payment creation endpoints support idempotency via the `idempotency_key` field:

```json
{
    "idempotency_key": "unique-key-123"
}
```

If a request with the same idempotency key is received:
- Same payload: Returns original response
- Different payload: Returns 409 Conflict

## Pagination

List endpoints support pagination via query parameters:

```
GET /api/v1/accounts/:account_id/transactions?limit=20&offset=0
```

**Response includes pagination metadata:**
```json
{
    "pagination": {
        "total": 100,
        "limit": 20,
        "offset": 0,
        "has_more": true
    }
}
```

## Headers

### Request Headers
```
Authorization: Bearer <token>
Content-Type: application/json
X-Request-ID: <request-id>
X-Correlation-ID: <correlation-id>
```

### Response Headers
```
Content-Type: application/json
X-Request-ID: <request-id>
X-Correlation-ID: <correlation-id>
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1735689600
```

## Rate Limiting

API endpoints are rate limited:

- **Default**: 1000 requests per minute
- **Payment endpoints**: 100 requests per minute
- **Auth endpoints**: 10 requests per minute

Rate limit headers are included in responses:
```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1735689600
```

## Versioning

API version is included in the URL path:
```
/api/v1/...
```

Current version: **v1**

## Versioning

This API is versioned as **v1.1** — see the Changelog.

## Changelog

### v1.2.0
- **Dispute Orchestration Platform** (dispute-service): long-running chargeback
  cases over a scheme-rules eligibility engine, a lifecycle state machine
  (SUBMITTED → ELIGIBILITY → AWAITING_EVIDENCE → MERCHANT_RESPONSE →
  UNDER_REVIEW → RESOLVED), evidence upload, case event timeline, deadline
  ticker, provisional credit and ledger adjustment on REFUNDED.
- **Delegated Access + Consent Centre** (consent-service): time-bound,
  capability-scoped view grants (`VIEW_BALANCE`, `VIEW_TRANSACTIONS`,
  `DOWNLOAD_STATEMENTS`), grant/revoke/evaluate, and a full per-access audit
  trail. Money movement is never delegable.
- **Service Dependency Health Graph** (control-plane): live graph with
  blast-radius computation, downstream→upstream health propagation,
  per-service health reports, and business-aware auto-throttle decisions.
- **Adaptive load shedding** (shared middleware): profit-aware request
  shedding wired into payment- and ledger-service, controlled by the
  control-plane throttle decisions.
- **Time-travel replay** (replay-service): `POST /v1/replay/time-travel`
  reconstructs an account's balance at any instant T from the append-only
  ledger two independent ways (backwards from stored `balance_after` vs
  forward signed sum) and reports divergence — deterministic state
  reconstruction for debugging.
- **Income lifecycle intelligence** (insights-service): salary status with
  increase detection and late-salary alerts; **financial runway** simulator
  ("what if my income stopped tomorrow?") with essentials-only runway and
  what-if scenarios.

### v1.1.0
- Financial Intelligence Platform: insights-service (subscriptions, price
  hikes, income detection, safe-to-spend, spending anomalies)
- Outbound transfer scam intelligence: fraud-service transfer evaluation +
  payment-service pre-flight gate
- Emergency account lockdown enforced at payment, transfer, card and ledger
  layers
- Insights alerts routed through the notification/SSE pipeline

### v1.0.0
- Initial release
- Authentication endpoints
- Account endpoints
- Payment endpoints
- Transaction endpoints
- Ledger endpoints
