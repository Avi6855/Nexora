# ADR-035: Data Platform Governance at 100+ Teams

**Status:** Accepted
**Date:** 2026-09-08

## Context

At 100+ teams and thousands of models, the data platform's failure modes
are organisational: datasets without owners, contracts that "pass" while
silently wrong, engineers querying production PII with good intentions,
and migrations that drop columns still being read. Individual diligence
does not scale; governance must be executable.

## Decision

1. **Quality contracts (`shared/dataplatform`)**: consumers declare
   measurable expectations — freshness, null rate, duplicate rate, and a
   *row-volume corridor* (a floor catches upstream collapse, a ceiling
   catches duplicate ingestion; a partition at 3% of normal is an outage,
   not a quiet day). Breaches quarantine the dataset version.
2. **Data products with visible ownership**: an unowned dataset is a real
   dashboard state (UNOWNED), not a blank field. Schema hashes are
   canonical, so drift is diffable.
3. **Privacy-aware query gateway**: verdicts ALLOW / REDACT / AGGREGATE /
   DENY decided on *context* (purpose, environment, columns, ticket), not
   SQL text. Unapproved purpose + PII = DENY; production PII without a
   ticket = DENY; raw PII is never returned — keys or k-anonymous
   aggregates only.
4. **Session recording with a chain hash**: every sensitive access is
   recorded into a hash-chained log — editing one record breaks every
   subsequent hash.
5. **Migration safety as a pipeline**: SUBMITTED → VALIDATING →
   CAPACITY_ESTIMATED → CANARY → APPLYING → VERIFYING → COMPLETE, with
   PAUSED/ROLLBACK/REPAIR states. Contractive changes with live readers
   are refused at the door — expand-migrate-contract is enforced, not
   suggested.

## Consequences

Governance moved from documentation to gates. The tests encode the
organisation's non-negotiables: quarantines fire, unowned is visible, PII
has exactly three legal shapes, tampering is detectable, and nobody drops
a column someone is reading.
