# ADR-023: Deterministic Financial Calculation Library & Verification Engine

**Status:** Accepted
**Date:** 2026-09-07

## Context

Fee, interest and proration arithmetic was implicit in each service. Two
services could compute "the same" fee differently and nobody would notice —
and per-day rounded interest accrual demonstrably drifts (the golden corpus
proved 21p/year on a five-figure balance) because half-pennies round
differently every day.

## Decision

1. `shared/calc` is the single source of financial arithmetic: int64 minor
   units, `math/big` intermediates (overflow is an error, never a truncation),
   explicit rounding policies, versioned calculators (`calc-v1`).
2. Ledger posting uses the exact `Accumulator`: accrue exactly, round once at
   realization. Display-grade per-day rounding remains available but is never
   used for posting.
3. Golden corpus (`shared/calc/golden_test.go`) is the deployment gate: DST
   days (23h/25h), leap years, month ends, exact-half rounding, negative
   adjustments, JPY zero-decimal. Any change that alters an expectation ships
   as a new Version and justifies the diff.
4. `shared/verify` runs critical calculations through two independent
   implementations; any mismatch quarantines the subject (refuses further
   operations) until human release. Canary data validation replays
   old-vs-new transformations with a hard aggregate drift budget (default
   0.01%) — breach stops the rollout.

## Consequences

- Support tooling, ledger and reporting can never disagree about money.
- Rate changes and rule changes cannot silently alter historical results
  (see ADR-025/026 for the time-interval discipline).
- Cost: dual execution only for critical paths; canary runs on samples.

# ADR-024: API Latency Budgets & Adaptive Concurrency

**Status:** Accepted
**Date:** 2026-09-07

## Context

A payment has a 1s budget; a login 500ms. Without explicit budgets, one slow
downstream (risk) silently consumes the whole request budget, and under
overload services queue blindly until timeouts cascade.

## Decision

1. `shared/latency`: every operation class has a documented budget. Budgets
   propagate in-band (`X-Nexora-Deadline-Ms` / `X-Nexora-Budget-Ms`); a
   downstream continues the caller's deadline rather than starting a fresh
   one. Allocations are charged/released; deadlines may be pulled earlier,
   never pushed beyond the original budget.
2. `shared/concurrency`: adaptive max-concurrency controller (AIMD). Shrinks
   multiplicatively on dependency errors, tail-latency divergence (p99 ≫ p50),
   queue depth, or timeout rate; grows additively after a calm window; hard
   floor/ceiling. This complements (does not replace) load shedding: shedding
   decides WHO, concurrency decides HOW MANY.

## Consequences

- Fail-fast with honest errors instead of queue-then-timeout.
- The controller converges to the knee of the latency curve; observability
  exposes limit, p50/p99 and last adjustment for tuning.

# ADR-025: Time-Travel Compliance Engine

**Status:** Accepted
**Date:** 2026-09-07

## Context

Auditors ask "why was THIS transaction allowed on 12 June 2024?" Live policy
tables answer with today's semantics — wrong for any historical date.

## Decision

1. Policy changes are append-only versions with `[valid_from, valid_to)`
   windows (`policy_versions` table). Old versions are never mutated.
2. Customer risk state is captured as timestamped snapshots; a replay binds
   the snapshot in force at the instant (newest `captured_at <= t`).
3. `TimeTravelService.EvaluateAtTime` re-runs the SAME rule engine on
   time-scoped inputs and emits a replay hash (SHA-256 over versions, rules,
   snapshot, outcome). Re-running the reconstruction must reproduce the hash.
4. Reconstructions without a covering snapshot are labelled
   `deterministic=false` — honest partial answers, never fake certainty.
5. Live evaluations archive a decision record binding the version IDs used.

## Consequences

- Every semantic change MUST ship as a new version with a validity window;
  editing history is impossible by construction.
- The replay engine shares the live evaluation code path, so "what would have
  happened" and "what did happen" cannot drift apart.

# ADR-026: Regulatory Impact Analysis & Evidence-Provenance Reporting

**Status:** Accepted
**Date:** 2026-09-07

## Context

A new regulation must map to affected services/APIs/data models/customers
before anyone starts coding, and every number in a regulatory submission must
survive the question "where exactly did this come from?".

## Decision

1. `regimpact`: services DECLARE capabilities (data classes, APIs, models).
   Rules match capabilities by domain semantics; impact propagates along the
   dependency graph (direct vs indirect, hops counted). New rules need no
   code change — only honest capability declarations.
2. `regreport`: figures are added WITH provenance (sources, query, transform
   version, row counts); a SHA-256 chain hash binds each chain. Validation
   gates (reconciliation, completeness, variance vs prior period) must pass
   before submission; submissions are immutable; evidence is hash-verified on
   every read so tampering is detected.

## Consequences

- Compliance becomes a graph query, not a meeting.
- Unexplained numbers cannot reach a submission; silently shifted figures
  cannot survive an audit re-hash.

# ADR-027: API Error Semantics & Retry Classification

**Status:** Accepted
**Date:** 2026-09-07

## Context

Services returned ad-hoc errors; clients could not distinguish "back off"
from "fix your request", and queue workers either retried poison messages
forever or dead-letttered transient blips.

## Decision

1. `shared/retry` is the central contract: every error classifies to
   `{code, retryable, class, customer_action}` with classes NEVER /
   IMMEDIATE / DELAYED / REAUTH / POISON / DUPLICATE.
2. Queue disposition is derived, not hand-rolled: POISON and exhausted
   DELAYED → dead-letter; DUPLICATE → suppress; NEVER → terminate.
3. Backoff is exponential with FULL JITTER, deterministic per attempt
   (reproducible tests), bounded by policy.
4. The library builds on `shared/errors` DomainError codes so HTTP surfaces
   and queue surfaces agree.

## Consequences

- Clients get predictable semantics; Kafka consumers get one dead-lettering
  policy across all services.
- New error codes must classify centrally — no per-service retry folklore.
