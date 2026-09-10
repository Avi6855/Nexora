# ADR-028: Stand-in Compatibility Certification & Safe Handover Protocol

**Status:** Accepted
**Date:** 2026-09-07

## Context

The platform runs a primary cloud and an independent stand-in with a reduced
service set — both can make payment decisions. Two failure modes dominate:

1. The stand-in silently diverges from the primary (a deploy changes decision
   semantics only on one side), so activating it during an outage changes
   payment outcomes.
2. After a handover, the old primary keeps accepting writes — split brain:
   two systems acting on the same money movement.

## Decision

1. **Compatibility certification** (`control-plane/internal/standin`): the
   same request is replayed against both platforms and RESPONSE SEMANTICS are
   compared — status class, decision, state transition, error code, limits.
   Any DECISION or STATE_TRANSITION divergence is categorically BREAKING
   regardless of score: a stand-in that approves what the primary declines is
   a different bank, not a degradation. Verdicts: COMPATIBLE / PARTIAL /
   BREAKING. Critical changes gate on certification (see ADR-031).
2. **Capability declaration**: the active platform publishes what it can do
   (`GET /platform-capabilities`); clients call `CanServe(name)` which fails
   CLOSED on undeclared capabilities — no hard-coded outage-mode logic.
3. **Safe handover** (`shared/handover`): PRIMARY → DRAINING → FROZEN →
   HANDED_OVER. Freeze refuses while work is in flight; the handover token
   lists completed state captures; the stand-in refuses to activate on an
   incomplete token. Epochs are monotone: every write carries the writer's
   epoch, stores fence writes below their highest-seen epoch, and the old
   primary self-fences by adopting epoch N+1 on handover. A partitioned old
   primary cannot write its way back in.

## Consequences

- Outage activation becomes a verified procedure, not a hope.
- The worst banking failure (double authority over money movement) is
  structurally impossible rather than procedurally discouraged.

# ADR-029: Card Network Gateway & ISO 8583 Lab

**Status:** Accepted
**Date:** 2026-09-07

## Context

Network messages arrive in multiple format versions; support needs the full
identity lifecycle of a card transaction from ANY identifier; asynchronous
advices arrive out of order and duplicated; terminals present transactions
minutes after going offline.

## Decision

1. `shared/cardnet` parses v1/v2/v3 wire formats into ONE canonical message.
   New network versions are new parsers, not platform migrations. Validation
   enforces mandatory correlation fields (STAN, RRN) and non-negative amounts.
2. The **Replay Lab** replays recorded/synthetic message sets (AUTH →
   CAPTURE → REVERSAL at configurable rates) through the gateway offline;
   malformed messages are counted, never fatal — the lab exists to find them.
3. The **Correlation Engine** maintains a bidirectional identity graph
   (app payment ↔ internal payment ↔ network trace ↔ processor ref ↔
   settlement ref); full lifecycle reachable from any node.
4. The **Advice Processor** sequences asynchronous messages by network
   sequence numbers: stale (≤ last applied) is suppressed, gaps are held and
   drained in order. Arrival order is never trusted.
5. **Offline presentment** is validated in strict order: duplicate STAN
   suppression → amount ceiling → presentment age.
6. **Contactless counters** track cumulative count/amount with monotone
   timestamps for delayed updates, and reconcile against authoritative
   issuer counters taking the HIGHER observed values (never reduce below
   locally observed state).

## Consequences

- One canonical event model for every card consumer.
- Out-of-order network reality is handled by design, not by retry folklore.

# ADR-030: Data Platform Health Intelligence

**Status:** Accepted
**Date:** 2026-09-07

## Context

Traffic-based hot-partition detection exists (shared/cassandra). Storage
defects it cannot see: a 500MB partition among 2MB siblings is a design
defect even at zero traffic; tombstone density degrades reads long before
alerts; Kafka hotspots and lag need diagnosis, not just dashboards.

## Decision

`shared/datainfra` provides:

1. **Partition size skew** with signature-based remediation: few giant
   partitions + healthy p99 → time bucketing; uniformly oversized →
   composite key redesign; borderline → monitor. Thresholds encode the
   ~100MB/100k-cell guidance.
2. **Tombstone pressure**: density = tombstones/(tombstones+live); ≥5%
   ELEVATED, ≥20% CRITICAL with coordinator read-drop warning
   (tombstone_fail_threshold) and compaction/TTL advice.
3. **Kafka hotspot detection** with a uniform-share baseline: a partition is
   hot only above max(absolute threshold, 2×uniform share) — a perfectly even
   4-partition topic must not false-positive at 25%.
4. **Lag advisor** with ordered diagnosis: poison partitions first (data
   problem → DLQ), then SKEW (topology — adding consumers cannot help one hot
   partition because Kafka assigns whole partitions), then concurrency
   (capacity → size the consumer count from lag/ceiling).
5. **Topic ownership registry**: producer → consumers → criticality;
   `DependentsOf(team)` and `CriticalTopics()` power blast-radius prechecks.

## Consequences

- Storage defects surface at CI/planning time with a named fix, not as 3am
  read-latency mysteries.

# ADR-031: Engineering Change Risk Scoring

**Status:** Accepted
**Date:** 2026-09-07

## Context

Reviewers weight by recency and confidence; risk is structural. A PR touching
a payment path with schema changes and 80 transitive dependents must carry
more evidence than a rename in a deferred service.

## Decision

`control-plane/internal/changerisk` scores changes from documented weights:
payment-path criticality (30), schema change (20), blast radius scaled to
transitive dependents (≤20, saturating), incident history (≤15), coverage
deficit below 80% (≤10), unfamiliar author (5), AI-authored provenance (5).
Score maps to LOW/MEDIUM/HIGH/CRITICAL; each level maps to concrete gates —
standard CI → canary with auto-rollback → shadow verification → stand-in
compatibility certification — plus schema migration plans and financial
invariant tests where relevant.

AI-authored changes are not banned; they carry the same gates plus an
explicit provenance review — engineering controls, not prohibitions.

## Consequences

- Risk becomes a number with named drivers, and gates are derived, not
  negotiated per PR.
