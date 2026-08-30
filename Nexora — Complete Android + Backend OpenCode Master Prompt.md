# NEXORA — COMPLETE ANDROID + BACKEND MASTER PROMPT

## ROLE

You are a Staff+ Android Engineer, Staff+ Backend Engineer, Distributed Systems Architect, Financial Systems Engineer, Cloud Engineer, SRE, Security Engineer, QA Engineer, UI/UX Engineer and Platform Engineer.

You must build a complete, production-style, end-to-end digital banking platform named:

# NEXORA

This project is a serious engineering portfolio project intended to demonstrate advanced Android engineering and backend/distributed-systems engineering capability.

This is an ORIGINAL banking platform.

Do NOT copy:
- Monzo branding
- Monzo logo
- Monzo proprietary UI
- Monzo proprietary source code
- Revolut branding/UI
- Wise branding/UI
- Starling branding/UI
- Barclays/HSBC/Lloyds/NatWest/JPMorgan/Chase proprietary assets
- any proprietary implementation of any real bank

Build an original product identity, original UI and original architecture.

The final application must feel like a premium modern fintech application on Android, backed by a serious event-driven financial platform.

---

# 1. PRIMARY OBJECTIVE

Build these two major parts:

1. Native Android application
2. Production-style backend platform

The Android application MUST connect to the real backend.

Do NOT use fake hardcoded data for normal application functionality.

Only use mock/simulated providers inside explicitly isolated development/test modules.

The project must demonstrate:

- distributed systems
- financial correctness
- event-driven architecture
- concurrency control
- idempotency
- fault tolerance
- event replay
- reconciliation
- observability
- resilience
- secure authentication
- scalable architecture
- operational safety
- excellent Android engineering
- excellent UI/UX

The final Android app is the user-facing client.

The backend is the source of truth.

---

# 2. TECHNOLOGY STACK

## Android

Use:

- Kotlin
- Jetpack Compose
- Material 3
- Navigation Compose
- ViewModel
- StateFlow
- Kotlin Coroutines
- Hilt
- Retrofit
- OkHttp
- Kotlin Serialization or Moshi
- gRPC where appropriate
- Room for non-authoritative local cache only
- DataStore
- Android BiometricPrompt
- WorkManager
- Secure storage abstraction
- Firebase Cloud Messaging abstraction
- Coil if image loading is required

## Backend

Use:

- Go
- REST/JSON public APIs
- gRPC internal APIs
- Protocol Buffers
- Apache Kafka
- Apache Cassandra
- Envoy Proxy
- Docker
- Kubernetes
- OpenTelemetry
- Prometheus-compatible metrics
- Grafana-compatible dashboards
- structured logging

## Infrastructure

Production-oriented:

- AWS for primary application infrastructure
- GCP-oriented analytics/data architecture
- Kubernetes
- Docker

Local development must NOT require AWS or GCP credentials.

---

# 3. HIGH-LEVEL ARCHITECTURE

Use this architecture:

Android
    |
    v
Envoy Gateway
    |
    +-----------------------------+
    |                             |
    v                             v
REST APIs                    gRPC APIs
    |                             |
    +-------------+---------------+
                  |
                  v
            Go Microservices
                  |
      +-----------+------------+
      |           |            |
      v           v            v
   Kafka      Cassandra     Supporting State
      |
      +------------------------------+
      |       |        |             |
      v       v        v             v
   Ledger   Fraud   Notification  Reconciliation
      |
      +----------------------------------+
      |       |        |        |        |
      v       v        v        v        v
    Replay  Simulation Policy  Audit  Incident
```

The architecture must support both synchronous and asynchronous workflows.

---

# 4. ARCHITECTURAL PRINCIPLE

IMPORTANT:

Do not make this a CRUD banking application.

The engineering depth must live in:

- immutable financial state
- event-driven processing
- deterministic state transitions
- failure recovery
- distributed consistency
- reconciliation
- replay
- observability
- operational safety
- concurrency correctness

The Android app should look simple and premium.

The backend should be sophisticated.

---

# 5. REPOSITORY STRUCTURE

Create:

nexora/

├── android/
│
├── services/
│   ├── identity-service/
│   ├── user-service/
│   ├── account-service/
│   ├── ledger-service/
│   ├── payment-service/
│   ├── transfer-service/
│   ├── card-service/
│   ├── pot-service/
│   ├── fraud-service/
│   ├── limits-service/
│   ├── notification-service/
│   ├── reconciliation-service/
│   ├── replay-service/
│   ├── simulation-service/
│   ├── policy-service/
│   ├── audit-service/
│   ├── control-plane-service/
│   └── incident-service/
│
├── proto/
├── shared/
├── kafka/
├── cassandra/
├── envoy/
├── infrastructure/
│   ├── docker/
│   ├── kubernetes/
│   ├── aws/
│   └── gcp/
│
├── tests/
│   ├── unit/
│   ├── integration/
│   ├── contract/
│   ├── e2e/
│   ├── load/
│   ├── chaos/
│   └── security/
│
├── docs/
│   ├── architecture/
│   ├── adr/
│   ├── api/
│   ├── security/
│   └── operations/
│
├── scripts/
├── .github/
├── docker-compose.yml
├── Makefile
└── README.md

---

# 6. ANDROID ARCHITECTURE

Use clean, feature-based architecture.

Structure:

android/

core/
    common/
    ui/
    design-system/
    network/
    database/
    security/
    logging/

feature-auth/
feature-home/
feature-accounts/
feature-transactions/
feature-payments/
feature-cards/
feature-pots/
feature-security/
feature-notifications/
feature-profile/
feature-settings/

Flow:

UI
↓
ViewModel
↓
UseCase
↓
Repository
↓
RemoteDataSource
↓
Retrofit/gRPC
↓
Envoy
↓
Go Backend

Keep business logic out of Composables.

Keep UI state separate from domain state.

---

# 7. ANDROID APP BRAND

Application name:

NEXORA

Create an original visual identity.

Design language:

- premium fintech
- modern
- minimalist
- trustworthy
- elegant
- high readability
- subtle depth
- sophisticated motion
- strong typography
- clean spacing

Do not copy any existing bank's color identity.

---

# 8. ANDROID DESIGN SYSTEM

Create a reusable Nexora design system.

Define:

- semantic colors
- typography
- spacing
- corner radius
- elevation
- surfaces
- button styles
- input styles
- card styles
- tabs
- chips
- navigation
- dialogs
- bottom sheets
- status badges
- transaction components
- loading components
- shimmer components
- error components

Support:

- light theme
- dark theme
- system theme

Do not hardcode visual values throughout the app.

Use theme tokens.

---

# 9. ANDROID SCREEN LIST

Create these screens:

## Authentication

Splash
Welcome
Onboarding
Registration
Login
OTP Verification
Biometric Setup
PIN Setup

## Banking

Home
Accounts
Account Details
Transactions
Transaction Details
Send Money
Select Recipient
Enter Amount
Review Payment
Payment Authentication
Payment Processing
Payment Unknown
Payment Success
Payment Failed
Payment Receipt

## Cards

Cards
Card Details
Virtual Card
Card Controls
Freeze Card
Unfreeze Card
Card Security

## Savings

Pots
Create Pot
Pot Details
Deposit
Withdraw
Savings Goal

## User

Notifications
Security Center
Profile
Settings
Support

---

# 10. ANDROID HOME

The Home screen must load data from the backend.

Display:

- total balance
- available balance
- pending funds
- reserved funds
- accounts
- recent transactions
- cards
- pots
- upcoming payments
- security messages

Do not calculate the authoritative balance inside Android.

Backend is authoritative.

---

# 11. SHIMMER — MANDATORY

Every data-driven screen must have a polished shimmer/skeleton state.

Create reusable:

ShimmerBalance
ShimmerAccount
ShimmerTransaction
ShimmerCard
ShimmerPot
ShimmerProfile
ShimmerNotification
ShimmerDashboard

Do not show plain "Loading..." for normal content loading.

Use skeleton layouts that closely match final content.

When data arrives:

Shimmer
→ crossfade
→ content

Avoid layout jumps.

The existing specification explicitly requires shimmer and animation validation as part of completion.

---

# 12. ANIMATION — MANDATORY

The application must contain meaningful, polished animations.

Implement:

- splash animation
- screen enter transition
- screen exit transition
- fade
- slide
- scale
- shared-axis where appropriate
- animated content size
- list item placement
- balance number animation
- transaction insertion animation
- payment state transition
- success animation
- failure animation
- card freeze animation
- card unfreeze animation
- button press feedback
- bottom-sheet animation
- dialog transition
- tab transition
- icon morphing
- pull-to-refresh animation
- skeleton-to-content transition
- shimmer-to-content transition
- progress animation

Do not over-animate.

Animations must never block interaction.

Support reduced-motion behavior.

---

# 13. ANDROID STATE MODEL

Every important screen must have explicit states:

Loading
Success
Empty
Error
Retry
Offline
Degraded
Refreshing

For payment:

Created
Authorized
Processing
Unknown
Confirmed
Settled
Failed
Reversed
Cancelled

The UI must reflect the actual backend state.

Never infer financial success from local UI state.

---

# 14. AUTHENTICATION

Implement:

- registration
- login
- OTP
- access tokens
- refresh tokens
- token rotation
- logout
- session expiry
- session revocation
- device registration
- device revocation
- PIN
- biometric authentication
- login throttling
- secure session handling

Password hashing:

Use Argon2id or bcrypt.

Never store raw passwords.

Never store secrets in source code.

---

# 15. USER SERVICE

Implement:

- user creation
- profile
- contact information
- device registration
- security preferences
- notification preferences
- user status
- session metadata

Use UUID identifiers.

---

# 16. ACCOUNT SERVICE

Support:

- current accounts
- savings accounts
- account metadata
- currency
- account status

Expose:

ledger_balance
available_balance
reserved_balance
pending_balance
spendable_balance

BUT:

Account Service is not the authoritative financial source.

Ledger Service is authoritative.

---

# 17. MONEY REPRESENTATION

Absolutely NO floating point for monetary calculations.

Never use:

float
double

for financial amounts.

Use:

integer minor units

or

exact decimal representation.

Example:

GBP 10.50

store as:

1050 minor units

Every monetary operation must preserve currency.

---

# 18. IMMUTABLE DOUBLE-ENTRY LEDGER

Create a true double-entry ledger.

Example:

Transfer £100:

Debit:
Account A = -10000 minor units

Credit:
Account B = +10000 minor units

Invariant:

TOTAL DEBITS == TOTAL CREDITS

Ledger entries are immutable.

Never update old financial entries.

Corrections use compensating entries.

Every financial entry must contain:

transaction_id
entry_id
account_id
amount
currency
direction
timestamp
source
correlation_id
causation_id
idempotency_key
event_version

---

# 19. LEDGER AUTHORITATIVENESS

Any operation affecting money MUST eventually pass through the Ledger Service.

Examples:

payment
transfer
card settlement
pot deposit
pot withdrawal
refund
reversal
fee
adjustment

No service may secretly maintain a second source of financial truth.

---

# 20. MONEY RESERVATION ENGINE

Implement:

ledger balance
pending funds
reserved funds
available funds
spendable funds

Concurrent operations must not double-spend.

Example:

Available:
£4,000

Request A:
reserve £3,000

Request B:
reserve £3,000

Only one may succeed.

Create concurrency tests.

---

# 21. PAYMENT SERVICE

Implement state machine:

CREATED
→ AUTHORIZED
→ PROCESSING
→ UNKNOWN
→ CONFIRMED
→ SETTLED

Alternative branches:

PROCESSING → FAILED
PROCESSING → UNKNOWN
CONFIRMED → REVERSED

Reject illegal transitions.

---

# 22. PAYMENT API

Implement:

POST /v1/payments

GET /v1/payments/{id}

GET /v1/accounts/{id}/payments

POST /v1/payments/{id}/cancel

All financial mutation endpoints must support idempotency.

---

# 23. IDEMPOTENCY ENGINE

Implement reusable idempotency logic.

Store:

idempotency_key
request_hash
actor_id
operation_type
status
result
created_at
expires_at

Rules:

Same key + same request:
return same logical operation result.

Same key + different request:
return conflict.

Test:

- same request 100 times
- concurrent identical requests
- retry after timeout
- retry after server crash
- duplicate Kafka event

Expected:

ONE logical financial operation.

---

# 24. UNKNOWN PAYMENT

UNKNOWN must be a first-class financial state.

Scenario:

Backend sends payment externally.

External provider processes it.

Network connection times out.

The backend does not know whether provider succeeded.

Do:

PROCESSING → UNKNOWN

Do NOT assume failed.

Then reconciliation resolves the final state.

Android must show:

"We're confirming this payment."

Do not show "Payment failed" while state is UNKNOWN.

---

# 25. MOCK EXTERNAL PAYMENT PROVIDER

Create only for development/testing.

Supported behaviors:

SUCCESS
FAILURE
TIMEOUT
UNKNOWN
DELAYED_SUCCESS
DUPLICATE_RESPONSE
503
NETWORK_ERROR

This simulator is intentionally unreliable.

It must help demonstrate recovery and reconciliation.

Do not pretend it is a real financial network.

---

# 26. KAFKA

Implement Kafka event architecture.

Topics:

user.created
account.created

payment.created
payment.authorized
payment.processing
payment.unknown
payment.confirmed
payment.settled
payment.failed
payment.reversed

transfer.created
transfer.completed

funds.reserved
funds.released

ledger.transaction.created
ledger.entry.created

fraud.alert
notification.created

reconciliation.required
reconciliation.completed

policy.decision

audit.event

incident.detected
incident.resolved

---

# 27. EVENT ENVELOPE

Every event:

event_id
event_type
event_version
aggregate_id
correlation_id
causation_id
producer
timestamp
payload

Support schema evolution.

Do not break consumers with incompatible event changes.

---

# 28. KAFKA PARTITIONING

Document:

- partition key
- consumer groups
- ordering requirements
- retry behavior
- dead-letter behavior
- replay behavior

Do NOT assume global ordering across Kafka partitions.

---

# 29. OUTBOX PATTERN

Implement an outbox pattern.

Handle:

Database commit succeeds
Kafka publish fails

Business change and outbox record must be committed together where appropriate.

Background publisher:

outbox
→ Kafka

Implement:

retry
backoff
deduplication

---

# 30. KAFKA DUPLICATE HANDLING

Kafka may redeliver messages.

Consumers must be idempotent.

Example:

payment.settled event delivered 3 times.

Expected:

one effective financial mutation.

Test duplicate events explicitly.

---

# 31. DEAD LETTER SYSTEM

If an event cannot be safely processed:

move it to a dead-letter topic/store.

Record:

event_id
event_type
service
consumer
error
attempt_count
first_failure
last_failure

Support controlled replay.

Never blindly replay financial events.

---

# 32. DISTRIBUTED PAYMENT SAGA

Implement:

Validation
↓
Authentication
↓
Policy
↓
Fraud
↓
Limits
↓
Reservation
↓
Ledger
↓
External Provider
↓
Settlement
↓
Notification

No global distributed database transaction.

Use asynchronous/event-driven coordination where appropriate.

---

# 33. COMPENSATING ACTIONS

If:

funds reserved
but
external payment fails

then:

release reservation

If a financial reversal is required:

create compensating ledger transaction

Never edit original ledger entry.

---

# 34. TRANSACTION FLIGHT RECORDER

Create a financial transaction execution history.

For each important transaction record:

trace_id
span_id
request_id
transaction_id
event_id
service
service_version
timestamp
decision
status
latency
correlation_id
causation_id
request_hash
response_hash

It must be possible to reconstruct how a payment moved through the system.

---

# 35. TRANSACTION TRACE API

Implement:

GET /v1/engineering/transactions/{id}/trace

Response must show stages:

REQUEST
AUTHENTICATION
POLICY
FRAUD
LIMITS
RESERVATION
LEDGER
KAFKA
PROVIDER
SETTLEMENT
NOTIFICATION

This is for protected engineering/admin access only.

---

# 36. TRANSACTION REPLAY ENGINE

Create Replay Service.

Capabilities:

- transaction replay
- payment replay
- account replay
- ledger replay
- historical state reconstruction

Inputs:

transaction/account
time range
snapshot
event version
implementation version

Output:

original result
replayed result
differences

Replay must be deterministic.

---

# 37. HISTORICAL BALANCE / TIME MACHINE

Implement:

GET /v1/accounts/{id}/state?at={timestamp}

System:

latest compatible snapshot
+
events after snapshot

returns:

balance
available_balance
reserved_balance
pending_balance
state_version
transaction_count
source_events

---

# 38. SNAPSHOT ENGINE

Do not replay millions of events for every request.

Create periodic snapshots.

Store:

snapshot_id
aggregate_id
state_version
timestamp
serialized_state
checksum

Historical reconstruction:

snapshot
+
subsequent events

Benchmark:

full replay
versus
snapshot replay

---

# 39. FINANCIAL STATE PROOF

Implement:

GET /v1/accounts/{id}/balance-proof

Generate:

opening balance
credits
debits
refunds
adjustments
reservations
result

Each component must reference ledger transactions.

The proof endpoint must be read-only.

---

# 40. LEDGER CONSISTENCY VERIFIER

Continuously check:

TOTAL DEBITS == TOTAL CREDITS

Also compare:

stored account state
versus
reconstructed ledger state

Detect:

- duplicate entries
- missing entries
- orphan events
- unbalanced transactions
- aggregate mismatch
- replay mismatch

Statuses:

HEALTHY
WARNING
MISMATCH
CRITICAL

Never silently rewrite financial history.

---

# 41. RECONCILIATION SERVICE

Handle:

UNKNOWN payments
external provider mismatch
ledger mismatch
settlement mismatch
missing events
duplicate provider results

Support:

event-driven reconciliation
scheduled reconciliation
manual reconciliation

Every reconciliation action must be auditable.

---

# 42. CONFLICT RESOLUTION

Support conflicting payment observations.

Example:

Primary:
AUTHORIZED

External:
SETTLED

Another subsystem:
DECLINED

Create deterministic conflict resolution.

Store all observations.

Do not silently discard evidence.

Possible final state:

SETTLED

or:

REQUIRES_RECONCILIATION

---

# 43. DIGITAL TWIN

Create a read-only simulation environment.

Input:

real account state
+
historical events
+
current policies

Create isolated simulation state.

Support:

- future payment simulation
- transfer simulation
- policy simulation
- failure simulation

Production state MUST remain unchanged.

---

# 44. COUNTERFACTUAL PAYMENT ENGINE

Implement:

POST /v1/simulations/payment

Input:

source account
destination
amount
currency

Simulate:

authentication
policy
fraud
limits
reservation
ledger effect
future state

Return:

would_succeed
predicted_balance
predicted_available_balance
risk
policy decisions
reasons
future impact

Never mutate production state.

---

# 45. POLICY ENGINE

Create policy-as-code service.

Example:

IF amount > threshold
AND recipient_is_new = true
THEN require_step_up_authentication

Policy lifecycle:

DRAFT
TESTING
SHADOW
ACTIVE
DISABLED

Store:

policy_id
version
scope
rules
created_by
approved_by
created_at

---

# 46. SHADOW POLICY

New policy executes alongside current policy without affecting customer outcome.

Compare:

current decision
versus
new decision

Calculate:

total evaluated
decision divergence
estimated impact
false-positive estimate
false-negative estimate

Activation must require authorization.

---

# 47. MULTI-PARTY AUTHORIZATION

Sensitive operations require approval.

Examples:

- payment controls
- high-value limits
- emergency shutdown
- security lockdown
- production policy changes

Workflow:

REQUESTED
→ APPROVAL_REQUIRED
→ APPROVED
→ EXECUTED

Record:

requester
approver
reason
timestamp
expiry
target
change

Implement separation of duties.

---

# 48. FINANCIAL FIREWALL

Implement server-side transaction controls:

- transfer limit
- beneficiary restrictions
- transaction velocity
- international payment controls
- time windows
- step-up authentication

These controls must NOT depend only on Android logic.

---

# 49. FRAUD ENGINE

Create a development fraud/risk service.

Inputs:

transaction amount
recipient history
device
velocity
account state
transaction frequency
risk signals

Output:

ALLOW
STEP_UP
BLOCK
REVIEW

Do not expose sensitive internal fraud signals to the client.

Android receives only safe user-facing explanation.

---

# 50. SECURITY CENTER

Android Security Center:

- active sessions
- devices
- last login
- suspicious activity
- card controls
- transaction protection
- biometric status
- PIN status

Real data from backend.

---

# 51. CARD SERVICE

Create simulated cards:

- physical card representation
- virtual card
- activation
- freeze
- unfreeze
- spending controls
- card status
- transaction association

All card financial behavior must eventually integrate with the Ledger.

---

# 52. POTS / SAVINGS SERVICE

Implement:

create
rename
delete
deposit
withdraw
target
progress

Money movements must go through Ledger.

Pot Service must not become a second financial source of truth.

---

# 53. NOTIFICATION SERVICE

Event-driven.

Consume:

payment events
transfer events
fraud events
security events
reconciliation events

Support:

in-app notification
push abstraction

Do not directly couple every business service to notification delivery.

---

# 54. CORRELATION ID SYSTEM

Propagate:

request_id
trace_id
transaction_id
correlation_id
causation_id

Through:

Android
Envoy
Go services
Kafka
Cassandra
reconciliation
replay

This must enable full transaction tracing.

---

# 55. ENVOY

Use Envoy for:

- routing
- retries
- timeout
- circuit breaking
- load balancing
- tracing headers
- request IDs
- rate limiting integration
- gRPC routing

Never configure infinite retries.

Be careful with retries around financial operations.

---

# 56. gRPC

Use protobuf for internal communication.

Services:

IdentityService
UserService
AccountService
LedgerService
PaymentService
TransferService
FraudService
LimitsService
ReconciliationService
ReplayService
SimulationService
PolicyService
IncidentService

Version APIs.

Generate clients/stubs.

Add compatibility tests.

---

# 57. GO SERVICE STRUCTURE

Each service:

cmd/
internal/
    domain/
    application/
    repository/
    transport/
    events/
    service/
tests/
README.md

Requirements:

- idiomatic Go
- context.Context
- explicit errors
- structured logging
- graceful shutdown
- timeout handling
- bounded retries
- clean interfaces
- dependency injection

Do not put business logic in transport handlers.

---

# 58. CASSANDRA DATA MODEL

Use query-first Cassandra modeling.

Create tables such as:

users_by_id
devices_by_user
accounts_by_user
account_by_id

ledger_transactions_by_account
ledger_entries_by_transaction

payments_by_id
payments_by_account

transfers_by_id

cards_by_user

pots_by_user

notifications_by_user

idempotency_records

outbox_events

reconciliation_cases

audit_events

policy_versions

transaction_execution_events

snapshots

incident_events

Document:

partition keys
clustering keys
query patterns
consistency considerations

Do not model Cassandra like a relational database.

---

# 59. ACCOUNT CONSISTENCY

Document and implement carefully:

- when balances are derived
- when balance cache is updated
- what is authoritative
- what happens during partial failure
- how replay reconstructs the state
- how reconciliation detects mismatches

---

# 60. ADAPTIVE CONSISTENCY

Classify operations:

CRITICAL_FINANCIAL
HIGH_VALUE_TRANSFER
CARD_AUTHORIZATION
BALANCE_READ
ANALYTICS
NOTIFICATION

Define appropriate consistency/retry behavior per category.

Document trade-offs.

Do not blindly use a single consistency strategy everywhere.

---

# 61. FINANCIALLY-AWARE BACKPRESSURE

Classify workloads:

CRITICAL:
money movement
ledger
authorization

IMPORTANT:
balance reads

DEFERABLE:
notifications
analytics

Under overload:

- protect money movement
- queue noncritical work
- reduce optional work
- prevent retry storms
- preserve financial correctness

---

# 62. ADAPTIVE BANKING CONTINUITY

Create an experimental standby architecture.

States:

PRIMARY
STANDBY
DEFERRED
RECONCILING

Support controlled continuity during:

- provider outages
- service outages
- network failure
- partial infrastructure failure

Do not create conflicting financial mutations across independent systems without strict protection.

---

# 63. SELF-HEALING RECOVERY

Create a bounded recovery controller.

Detect:

- high error rate
- latency
- service failure
- Kafka lag
- provider errors

Flow:

Detect
→ Classify
→ Mitigate
→ Recover
→ Verify

Possible actions:

- pause unnecessary retries
- increase Kafka consumers
- route traffic away from degraded component
- queue noncritical tasks
- resume after verification

Every automated action must be auditable.

---

# 64. CASSANDRA HOT PARTITION DETECTOR

Detect:

- high read rate
- high write rate
- high latency
- partition skew

Show:

partition
traffic
latency
risk

Create benchmark tooling to compare partitioning strategies.

---

# 65. CHAOS TEST FRAMEWORK

Build only for local/development/staging.

Fault injection:

- kill payment service
- kill ledger service
- delay Kafka
- duplicate Kafka messages
- delay Cassandra
- make Cassandra unavailable
- provider timeout
- provider 503
- network timeout
- network partition
- random pod termination

Each experiment:

experiment_id
target
fault_type
duration
expected_result
actual_result
recovery_time
financial_invariant_result

---

# 66. CRITICAL CHAOS REQUIREMENT

A chaos experiment involving financial operations is successful only when:

the system recovers

AND

financial invariants remain correct.

Example:

Kill payment service during processing.

Expected:

no duplicate payment
no incorrect balance
no unbalanced ledger
event eventually reconciled

---

# 67. INCIDENT ENGINE

Create incident service.

Collect:

logs
metrics
traces
transaction events
deployment data
configuration changes
policy versions

Build incident timeline.

Incident states:

OPEN
INVESTIGATING
MITIGATING
RESOLVED
CLOSED

---

# 68. INCIDENT ANALYSIS

Provide an optional AI analysis layer.

AI may:

- summarize incident
- correlate telemetry
- identify likely root cause
- suggest next investigation step
- suggest remediation

AI MUST NOT automatically:

- move money
- change balances
- activate financial policy
- modify production configuration
- disable payment rails

Human approval is mandatory.

---

# 69. BLAST RADIUS ANALYZER

Given:

service change
configuration change
policy change

calculate:

affected services
affected APIs
affected Kafka topics
affected payment paths
estimated risk
dependency impact

---

# 70. CAPACITY ENGINE

Collect:

CPU
memory
request rate
payment rate
Kafka lag
consumer throughput
Cassandra latency
error rate

Provide:

current utilization
estimated safe capacity
potential bottleneck
recommended scale

Do not fabricate prediction accuracy.

---

# 71. AUDIT SERVICE

Record sensitive actions:

actor
role
action
target
reason
request_id
trace_id
timestamp
previous_state
new_state
authorization
approval

Audit records should be append-only.

---

# 72. OBSERVABILITY

Implement OpenTelemetry.

Trace:

Android
→ Envoy
→ Go service
→ Kafka
→ downstream service
→ Cassandra

Metrics:

request count
P50
P95
P99
error rate
payment success rate
UNKNOWN rate
Kafka lag
Cassandra latency
ledger verification failures
reconciliation queue
recovery time

Use structured logs.

Never log:

passwords
access tokens
refresh tokens
private keys
secrets

---

# 73. ANDROID NETWORKING

Create central network layer.

Handle:

- API errors
- timeouts
- retries
- token refresh
- unauthorized state
- connectivity state
- server errors

For financial mutation retries:

ALWAYS use server-issued or client-generated idempotency keys.

Do not blindly retry payment requests.

---

# 74. REAL-TIME ANDROID UPDATES

Where appropriate, use:

- WebSocket
- SSE
- push notification
- safe polling

Use real-time updates for:

payment state
transfer state
security events

Avoid aggressive polling.

Android UI must update without requiring page refresh for important asynchronous states.

---

# 75. ANDROID PAYMENT FLOW

Flow:

Recipient
→ amount
→ review
→ authentication
→ submit
→ processing
→ success / unknown / failure

During processing:

show animated state.

When backend changes state:

update UI without restarting activity/fragment.

---

# 76. ANDROID UNKNOWN PAYMENT UX

When backend returns UNKNOWN:

show a dedicated screen:

Payment status pending confirmation.

Show:

amount
recipient
time
transaction reference
what happens next

Never show false failure.

---

# 77. ANDROID ERROR UX

Map backend errors to safe user-facing messages.

Examples:

INSUFFICIENT_FUNDS
→ "You don't have enough available funds."

PAYMENT_UNKNOWN
→ "We're confirming this payment."

RATE_LIMITED
→ "Too many attempts. Please try again shortly."

SERVICE_UNAVAILABLE
→ "The service is temporarily unavailable."

Do not expose stack traces or infrastructure internals.

---

# 78. OFFLINE MODE

When offline:

Allow:

- cached balance display
- cached transaction viewing
- safe profile/settings access where appropriate

Do NOT blindly queue financial transfers.

Do not automatically retry money movement after reconnect unless protected by explicit idempotency and correct backend semantics.

---

# 79. LOCAL CACHE

Room is allowed only for non-authoritative local data.

Examples:

cached transactions
cached profile
cached display preferences

Never treat cached balance as financial truth.

---

# 80. ANDROID SECURITY

Implement:

- encrypted/secure token storage
- biometric authentication
- PIN
- session expiration
- device revocation
- secure API transport
- input validation
- no secrets in APK
- no sensitive information in logs

---

# 81. ANDROID ACCESSIBILITY

Support:

- TalkBack
- content descriptions
- semantic Compose nodes
- dynamic font size
- touch targets
- sufficient contrast
- reduced motion

Animations must be disabled/reduced gracefully where user preferences require it.

---

# 82. ANDROID PERFORMANCE

Avoid:

- unnecessary recompositions
- blocking main thread
- memory leaks
- unbounded lists
- excessive network requests

Use:

- LazyColumn/LazyRow
- stable keys
- lifecycle-aware StateFlow collection
- efficient image loading
- optimized state management

---

# 83. BACKEND PERFORMANCE

Use:

- bounded goroutines
- context cancellation
- connection pooling
- efficient Kafka consumers
- efficient Cassandra access
- timeouts
- bounded retries

Do not create unbounded background workers.

---

# 84. SECURITY ARCHITECTURE

Implement:

- TLS-ready architecture
- authentication
- authorization
- RBAC
- rate limiting
- input validation
- audit logging
- token rotation
- session management
- device management
- secret management abstraction

Never hardcode secrets.

---

# 85. API DESIGN

Public API:

/v1/auth/*
/v1/users/*
/v1/accounts/*
/v1/payments/*
/v1/transfers/*
/v1/cards/*
/v1/pots/*
/v1/transactions/*
/v1/security/*
/v1/notifications/*

Engineering endpoints must be separately protected:

/v1/engineering/*
/v1/replay/*
/v1/simulations/*
/v1/policies/*
/v1/reconciliation/*
/v1/incidents/*
/v1/chaos/*

---

# 86. API PAGINATION

For large collections use cursor-based pagination.

Especially:

transactions
payments
events
notifications

Avoid offset pagination for large financial datasets unless justified.

---

# 87. API VERSIONING

Use:

/v1/...

Do not introduce breaking changes without explicit versioning.

---

# 88. CORRELATION HEADERS

Support:

X-Request-ID
X-Correlation-ID
X-Causation-ID

And backend-generated:

trace_id
transaction_id

---

# 89. SERVICE-TO-SERVICE AUTH

Internal services must authenticate to one another.

Design for:

mTLS-ready communication
service identities
authorization policies

Do not trust service identity solely based on network location.

---

# 90. DOCKER

Create Dockerfiles for all Go services.

Use multi-stage builds.

Keep images small.

Do not place credentials in Docker images.

---

# 91. DOCKER COMPOSE

Create local environment with:

Cassandra
Kafka
Kafka UI
Envoy
all required Go services

Provide:

./scripts/dev-up
./scripts/dev-down

Android connects to configured local backend.

---

# 92. KUBERNETES

Create manifests or Helm-style structure for:

- Namespace
- Deployment
- Service
- ConfigMap
- Secret templates
- HPA
- PDB
- NetworkPolicy
- ServiceAccount
- Gateway/Ingress

Set resource requests and limits.

---

# 93. CI/CD

Create GitHub Actions for:

Go tests
Android tests
integration tests
contract tests
security scans
Docker builds

Pipeline:

format
→ lint
→ unit tests
→ integration tests
→ contract tests
→ security checks
→ build

No automatic production deployment without approval.

---

# 94. TESTING ARCHITECTURE

Implement:

- unit tests
- repository tests
- integration tests
- Kafka tests
- Cassandra tests
- API tests
- gRPC tests
- contract tests
- Android unit tests
- Android UI tests
- Android integration tests
- E2E tests
- load tests
- chaos tests
- security tests

---

# 95. FINANCIAL INVARIANTS

Mandatory tests:

1. debits equal credits
2. no duplicate financial mutation from duplicate Kafka event
3. no duplicate mutation from repeated API request
4. concurrent reservations cannot overspend
5. invalid payment state transition fails
6. UNKNOWN is not silently converted to FAILED
7. replay is deterministic
8. compensating transactions preserve ledger correctness
9. sensitive operations generate audit entries
10. unbalanced transactions cannot settle

---

# 96. CONCURRENCY TESTS

Test:

- concurrent transfers
- concurrent reservations
- concurrent idempotent requests
- concurrent Kafka consumers
- duplicate provider callback
- payment timeout plus retry
- simultaneous account operations

Ensure no race conditions.

Use Go race detector where appropriate.

---

# 97. FAILURE SCENARIOS

Test:

Android timeout
API timeout
Envoy failure
payment service crash
ledger service crash
Kafka duplicate
Kafka delay
consumer crash
Cassandra latency
Cassandra outage
provider timeout
provider 503
provider duplicate callback
network partition
partial deployment
reconciliation failure

For every scenario:

expected result
actual result
financial correctness
recovery method
recovery duration

---

# 98. LOAD TESTING

Provide scripts for:

1,000 concurrent users
5,000 concurrent users
10,000 concurrent users

Payment scenarios:

1,000 payments/sec
5,000 payments/sec
10,000 payments/sec

Measure actual:

P50
P95
P99
error rate
Kafka lag
Cassandra latency
CPU
memory
recovery time

NEVER fabricate performance results.

---

# 99. BENCHMARK RULE

Do not claim:

"10K TPS supported"

until an actual benchmark proves it.

Instead document:

"10K TPS benchmark scenario configured."

Only publish actual measurements after execution.

---

# 100. TEST DATA

Create deterministic synthetic test data.

Include:

- demo users
- accounts
- cards
- pots
- transactions
- payments
- UNKNOWN payments
- failed payments
- reconciliation cases
- policy versions
- audit records
- incidents

Clearly identify all test data as synthetic.

---

# 101. API DOCUMENTATION

Generate:

OpenAPI
protobuf documentation
Kafka event documentation

Document:

- authentication
- authorization
- headers
- idempotency
- pagination
- error codes
- versioning
- retries

---

# 102. ERROR MODEL

Standard API error:

code
message
request_id
trace_id
details

Examples:

INSUFFICIENT_FUNDS
PAYMENT_UNKNOWN
IDEMPOTENCY_CONFLICT
POLICY_BLOCKED
AUTH_REQUIRED
RATE_LIMITED
SERVICE_UNAVAILABLE
RECONCILIATION_REQUIRED

---

# 103. ANDROID DEEP LINKING

Support deep links where useful.

Examples:

nexora://payment/{paymentId}
nexora://transaction/{transactionId}

Ensure authorization before displaying sensitive content.

---

# 104. ANDROID STATE RESTORATION

Support:

- configuration changes
- process recreation
- navigation restoration
- state restoration where appropriate

Do not lose safe UI state unnecessarily.

---

# 105. ANDROID STARTUP

Flow:

Splash
→ initialization
→ secure session restore
→ authentication decision
→ animated transition
→ Home

Do not create unnecessary long loading.

---

# 106. ANDROID VISUAL POLISH

Use:

- consistent spacing
- premium cards
- elegant typography
- subtle depth
- meaningful animations
- accurate loading skeletons
- polished error states
- polished empty states
- responsive layouts

Every screen should look intentionally designed.

Do not make any screen feel like an unfinished developer prototype.

---

# 107. BACKEND LOGGING

Use structured JSON logs.

Every important financial request should include:

request_id
trace_id
transaction_id
correlation_id

Never expose:

credentials
tokens
passwords
private keys

---

# 108. OBSERVABILITY HEALTH ENDPOINTS

Every service must expose:

/health
/ready
/live
/metrics

Where appropriate.

Health checks must distinguish:

alive
ready
degraded

---

# 109. GRACEFUL SHUTDOWN

Every Go service must:

- handle SIGTERM
- stop accepting new work
- finish safe in-flight work
- close connections
- flush logs/telemetry
- stop consumers safely

---

# 110. RETRY POLICY

Retries must be:

- bounded
- exponential/backoff
- jittered
- operation-aware

Never blindly retry financial writes.

Payment retries must be idempotency-aware.

---

# 111. CIRCUIT BREAKERS

Use circuit breakers where appropriate for external/unreliable dependencies.

States:

CLOSED
OPEN
HALF_OPEN

Measure:

failure rate
latency
recovery

---

# 112. RATE LIMITING

Implement server-side rate limits for:

login
OTP
payment creation
transfer creation
sensitive APIs

Return correct rate-limit errors.

---

# 113. SECURITY EVENTS

Generate security events for:

new device
login failure
login success
session revoke
biometric setup
sensitive payment
step-up authentication
card freeze
card unfreeze
security policy decision

---

# 114. PRODUCT SAFETY

This project uses simulated banking/payment infrastructure.

Never connect to real financial accounts.

Never expose actual credentials.

Never process real customer data.

Use synthetic/demo data only.

---

# 115. ENGINEERING QUALITY

Do not create:

- giant classes
- giant files
- god services
- duplicate domain logic
- hidden global mutable state
- fake APIs
- fake metrics
- fake traces
- dead buttons

Keep code readable and maintainable.

---

# 116. NO FAKE FEATURE RULE

Every visible Android feature must have one of:

- working backend integration
- working local functionality
- clearly isolated development-only simulator

Never create:

"Coming Soon"

buttons for core features.

The prior specification explicitly requires no dummy core functionality and real Android/backend validation.

---

# 117. ENGINEERING DEMO MODE

Create protected Engineering Mode only for authorized development/admin users.

Features:

Transaction Trace
Replay
Historical Balance
Financial Proof
Digital Twin
Payment Simulation
Policy Simulation
Shadow Policy
Reconciliation
Incident Timeline
System Health
Chaos Testing

Do NOT expose Engineering Mode to normal users.

---

# 118. END-TO-END PAYMENT FLOW

Implement:

Android
→ authentication
→ payment creation
→ idempotency
→ Envoy
→ Payment Service
→ policy
→ fraud
→ limits
→ reservation
→ Ledger
→ outbox
→ Kafka
→ provider simulator
→ settlement
→ reconciliation
→ notification
→ Android update

Every transition must be observable.

---

# 119. END-TO-END DUPLICATE FLOW

Send the same request 20-100 times concurrently.

Expected:

1 logical payment
1 ledger mutation
1 canonical result

Prove this with automated tests.

---

# 120. END-TO-END UNKNOWN FLOW

Provider:

processes payment

network:

times out

System:

PROCESSING
→ UNKNOWN

Reconciliation:

UNKNOWN
→ CONFIRMED
→ SETTLED

No double debit.

---

# 121. END-TO-END CRASH FLOW

Kill Payment Service after funds reservation.

Expected:

system detects incomplete operation
→ recovery/reconciliation
→ final valid financial state

No money duplication.

---

# 122. END-TO-END KAFKA DUPLICATE FLOW

Publish identical event multiple times.

Expected:

consumer detects effective duplication

No additional ledger mutation.

---

# 123. END-TO-END REPLAY FLOW

Select historical transaction.

Run Replay.

Show:

original state
replayed state
current implementation state

Any divergence must be visible.

---

# 124. END-TO-END SIMULATION FLOW

Create:

"What if this account sends £2,000 tomorrow?"

Run:

policy
fraud
limits
reservation
ledger simulation

Return predicted state.

Production state must not change.

---

# 125. ANDROID/BACKEND CONNECTIVITY VALIDATION

Do not mark the project complete until all these real flows work:

registration
login
OTP
session restore
account retrieval
balance retrieval
transaction retrieval
payment creation
payment status
UNKNOWN payment
payment completion
cards
pots
notifications
security

All must communicate with the backend.

The source specification similarly requires real backend data and explicit Android/backend connectivity validation.

---

# 126. ANDROID UI VALIDATION

Verify:

- all navigation
- all animations
- shimmer
- loading state
- empty state
- error state
- retry
- dark mode
- dynamic font
- large font
- state restoration
- back navigation
- deep links

The UI validation requirements in the existing specification explicitly call for navigation, animation, shimmer, loading/error/empty states, dark mode and state restoration.

---

# 127. ACCESSIBILITY VALIDATION

Verify:

TalkBack
font scaling
reduced motion
contrast
touch target size

Do not allow animations to prevent interaction.

---

# 128. FINAL BACKEND COMPLETION GATE

Do not declare backend complete until:

- all services compile
- tests pass
- Cassandra starts
- Kafka starts
- Envoy starts
- services register/connect
- REST APIs work
- gRPC works
- Kafka events work
- ledger works
- idempotency works
- UNKNOWN flow works
- reconciliation works
- replay works
- snapshotting works
- financial proof works
- chaos framework works
- observability works
- Docker works
- Kubernetes configuration validates
- no secrets are committed
- no floating-point money calculations exist

---

# 129. FINAL ANDROID COMPLETION GATE

Do not declare Android complete until:

- app builds
- app launches
- authentication works
- backend connectivity works
- real account data works
- real transaction data works
- real payment flow works
- UNKNOWN payment UI works
- notifications work
- cards work
- pots work
- shimmer works
- animations work
- dark mode works
- accessibility works
- state restoration works
- network failure states work
- no screen has dummy functionality

---

# 130. FINAL PROJECT COMPLETION GATE

The project is complete only when:

1. Android connects to real backend.
2. Backend runs using Docker.
3. Cassandra works.
4. Kafka works.
5. Envoy works.
6. Go services work.
7. Internal gRPC works.
8. Public REST APIs work.
9. Ledger is immutable.
10. Ledger remains balanced.
11. Duplicate payments are prevented.
12. Duplicate Kafka events are handled.
13. UNKNOWN payments are supported.
14. Reconciliation works.
15. Replay is deterministic.
16. Historical state works.
17. Financial proof works.
18. Digital Twin works.
19. Counterfactual simulation works.
20. Policy engine works.
21. Shadow policy works.
22. Multi-party authorization works.
23. Audit works.
24. Chaos tests work.
25. Recovery logic works.
26. Observability works.
27. Android animations work.
28. Android shimmer works.
29. Dark mode works.
30. Accessibility works.
31. Automated tests pass.
32. Load-test framework works.
33. Security checks pass.
34. No hardcoded secrets exist.
35. No fabricated metrics exist.
36. No core feature is a fake button.
37. Documentation is complete.

The existing project specification likewise defines completion around working Android/backend connectivity, replay, financial proof, Digital Twin, policy simulation, reconciliation, observability, shimmer/animations, tests and absence of fake metrics/features.

---

# 131. IMPLEMENTATION STRATEGY

Work in phases.

## Phase 1 — Foundation

Create:

- monorepo
- Android app
- Go service framework
- Docker
- Cassandra
- Kafka
- Envoy
- configuration system
- shared libraries

Run everything locally.

---

## Phase 2 — Identity

Implement:

- registration
- login
- OTP
- token management
- device registration
- Android auth screens

Connect Android to backend.

---

## Phase 3 — Accounts

Implement:

- users
- accounts
- balances
- transactions read APIs

Connect Home and Transactions screens.

---

## Phase 4 — Ledger

Implement:

- immutable double-entry ledger
- reservations
- balance reconstruction
- financial invariants

Write extensive tests.

---

## Phase 5 — Payments

Implement:

- payment state machine
- idempotency
- outbox
- Kafka
- provider simulator
- UNKNOWN
- settlement

Connect Android payment flow.

---

## Phase 6 — Cards + Pots

Implement:

cards
pots
security controls

Connect Android.

---

## Phase 7 — Reconciliation + Replay

Implement:

reconciliation
time machine
snapshots
replay
financial proof

---

## Phase 8 — Simulation

Implement:

Digital Twin
Counterfactual Payment Engine
simulation APIs

---

## Phase 9 — Policy

Implement:

policy engine
shadow policy
multi-party authorization
financial firewall

---

## Phase 10 — Reliability

Implement:

backpressure
circuit breakers
adaptive consistency
continuity
recovery controller
chaos engine

---

## Phase 11 — Observability

Implement:

OpenTelemetry
metrics
logs
traces
health checks
transaction flight recorder

---

## Phase 12 — Hardening

Run:

unit tests
integration tests
contract tests
E2E tests
load tests
chaos tests
security tests

Fix all failures.

---

# 132. DOCUMENTATION REQUIREMENT

Create:

README.md

Include:

Project overview
Architecture
Android architecture
Backend architecture
Service boundaries
Cassandra modeling
Kafka design
Ledger design
Idempotency
Outbox
Saga
UNKNOWN state
Reconciliation
Replay
Snapshotting
Digital Twin
Policy engine
Chaos engineering
Observability
Security
Testing
Load testing
Failure scenarios
Trade-offs
Known limitations

---

# 133. ARCHITECTURE DECISION RECORDS

Create:

ADR-001 Cassandra
ADR-002 Kafka
ADR-003 Ledger
ADR-004 Idempotency
ADR-005 Outbox
ADR-006 Saga
ADR-007 UNKNOWN
ADR-008 Replay
ADR-009 Snapshotting
ADR-010 Consistency
ADR-011 Envoy
ADR-012 Kubernetes
ADR-013 Multi-party authorization
ADR-014 Shadow policy
ADR-015 Chaos engineering
ADR-016 Digital Twin
ADR-017 Financial backpressure
ADR-018 Reconciliation
ADR-019 Android architecture
ADR-020 Real-time update strategy

Each ADR must contain:

Context
Decision
Alternatives
Trade-offs
Consequences

---

# 134. IMPORTANT OPENCODE EXECUTION RULES

Before editing:

1. Inspect repository.
2. Understand current files.
3. Do not blindly overwrite working code.
4. Reuse existing functionality.
5. Keep changes modular.

After each phase:

1. Compile.
2. Run tests.
3. Fix errors.
4. Run integration tests.
5. Verify Android/backend connectivity.
6. Update documentation.

Never stop at skeleton generation.

Do not leave core functionality as TODO/FIXME/Coming Soon.

---

# 135. IMPORTANT DATA RULE

All banking data displayed to normal Android screens must come from backend APIs.

The Android app must NEVER fabricate:

- balances
- payment status
- transaction history
- card status
- financial totals

Backend is authoritative.

---

# 136. IMPORTANT FAILURE RULE

Assume:

- requests can timeout
- Kafka can duplicate
- Kafka can delay
- services can crash
- Cassandra can become slow
- providers can respond late
- provider callbacks can duplicate
- network can partition
- Android can lose connectivity

Design accordingly.

---

# 137. IMPORTANT MONEY SAFETY RULE

Under no circumstances should the system:

- double debit
- create unbalanced ledger
- settle an unknown transaction without evidence
- edit historical financial entries
- bypass idempotency
- trust Android for financial truth

---

# 138. FINAL PRODUCT DEFINITION

Nexora must be:

BEAUTIFUL ON THE SURFACE.

RIGOROUS UNDERNEATH.

The Android application should feel like a premium world-class fintech application.

The backend should feel like a serious distributed financial platform.

Do not optimize for maximum number of screens.

Optimize for:

ENGINEERING DEPTH
CORRECTNESS
RELIABILITY
SECURITY
SCALABILITY
OBSERVABILITY
OPERABILITY
DEMONSTRABLE RESULTS

Build the complete Nexora Android application and backend end-to-end.

Do not stop after scaffolding.

Do not fabricate results.

Do not use fake functionality.

Make the Android application genuinely communicate with the backend and make the backend genuinely execute the financial workflows.
