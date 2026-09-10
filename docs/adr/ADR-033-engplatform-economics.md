# ADR-033: Engineering Platform as Product & Operational Economics

**Status:** Accepted
**Date:** 2026-09-07

## Context

Platform teams ship infra; other engineers consume it. Without product
discipline the platform rots: services appear without owners, deploys break
undeclared dependencies, and nobody knows what a customer journey costs or
whether the fifth nine was worth buying.

## Decision

1. **Bootstrapper + golden path (`shared/engplatform`)**: `Bootstrap`
   emits the production-shaped skeleton (ownership, SLOs, metrics, tracing,
   CI) from birth — the golden path is enforced at **deploy** time as
   blocking checks, not advisory lint. Unowned services and scan-failing
   builds are structurally unrepresentable.

2. **Contract registry**: consumers declare runtime dependency contracts
   (min API version, p99 latency budget, schema version). Providers break a
   contract → failed deploy, not a 3am page.

3. **Ephemeral environments**: one live env per branch, TTL capped at 8h,
   auto-reaped, sanitised data mandatory. Forgotten environments are both a
   cost leak and a stale-data risk.

4. **Operational economics (`shared/economics`)**: journey cost attribution
   prices each customer flow from measured service unit costs and names the
   top cost driver. Reliability optimisation chooses the availability tier
   minimising **infra + expected customer impact** (support + churn×LTV +
   reputational), so buying the fifth nine is an explicit, quantified
   decision — cheap for an internal tool, mandatory for payments.

## Consequences

- Deploy gates moved from tribal knowledge to executable standards.
- Engineering economics became legible: journeys have prices, nines have
  marginal costs, and both are testable code.

## Alternatives considered

Advisory-only golden path (rejected: standards nobody enforces decay);
pure CVSS patching order (see ADR-032); per-team bespoke reliability
decisions (rejected: the trade-off maths is identical, only the impact model
differs).
