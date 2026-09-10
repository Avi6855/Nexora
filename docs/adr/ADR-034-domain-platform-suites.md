# ADR-034: Domain Platform Suites (Cards, Credit, Mortgage, Investments, Life Events, Identity, Open Banking)

**Status:** Accepted
**Date:** 2026-09-08

## Context

Rounds of feature work had covered infrastructure (workflows, compliance,
consistency, reliability) but the *banking domains themselves* — cards,
credit, mortgage, investments, life events, identity, open banking — still
carried their hardest problems implicitly. Each domain has one or two
invariants that, if violated, create either customer harm or regulatory
exposure.

## Decision

Seven domain packages, each built around its binding invariant:

1. **Cards (`shared/cards`)** — merchant-locked virtual cards,
   programmable authorization rules (unfiltered BLOCK rules match
   everything — a fall-through bug that would have declined the world),
   credential continuity across reissue, a card lifecycle saga, and
   courier delivery-exception recovery.
2. **Credit (`shared/credit`)** — limit simulation, repayment strategy
   optimisation (avalanche/snowball/balanced with exact interest maths),
   bureau data correction cases, a decision-policy sandbox, and fairness
   monitoring that alarms on *both* sides of a segment split.
3. **Mortgage (`shared/mortgage`)** — affordability workspace, an
   asynchronous document state machine that queues behind unavailable
   providers, and offer-expiry protection with dependency-aware workflows.
4. **Investments (`shared/investments`)** — the HMRC-shared ISA allowance
   as ONE gateway across wrappers (corrections release exactly what the
   original consumed — applying the *net* figure after an explicit release
   refunds money the customer never had), joint goals over individual
   wrappers, tax-lot selection (FIFO/LIFO/HIFO with exact partial-lot
   basis), and deterministic corporate-action application.
5. **Life events (`shared/lifeevents`)** — the bereavement state machine
   whose first act is blocking outgoing payments (protection cannot wait
   for verification; fraudsters exploit exactly that window), gated
   stages, disputability everywhere except closure; plus auto-expiring
   life-event workspaces with DAG-ordered tasks and beneficiary shares
   that must sum to exactly 100%.
6. **Identity (`shared/identity`)** — progressive recovery where weak
   signals are *capped* by missing strong evidence (ten weak signals never
   synthesize one strong one), a device trust graph whose revocation
   CASCADES along MFA pairings, and passkey challenges that retire
   unconditionally (consume-at-most-once even on failure paths).
7. **Open banking (`shared/openbanking`)** — failure classification
   routed to recovery lanes (silent refresh is silent; schema drift is an
   engineering lane, never a customer prompt), explicit freshness SLAs
   where UNKNOWN is distinct from STALE, and a capability matrix where
   observed reality overrides declared support.

## Consequences

Every domain states its invariant in code and enforces it in tests. The
hard parts — allowance corrections, trust propagation, recovery ceilings,
freshness semantics — are the tested behaviours, not comments.
