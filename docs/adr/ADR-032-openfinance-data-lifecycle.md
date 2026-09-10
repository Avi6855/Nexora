# ADR-032: Open-Finance, Data-Lifecycle and Storage Intelligence

**Status:** Accepted
**Date:** 2026-09-07

## Context

Open-finance integrations, Cassandra/Kafka estates and retention policies
were governed by ad-hoc rules. Links degraded silently; partitions skewed
until reads timed out; retention was "whatever the topic was created with".

## Decision

1. **Open-finance (`shared/openfinance`)**: connected accounts get a
   weighted composite health score with a **weakest-component floor** — a
   collapsed component caps the headline state so composites can never mask
   a dead freshness pipeline. Provider descriptors normalise to one canonical
   schema via data mappings with keyword-heuristic fallback and UNKNOWN on
   no-match (never a wrong guess). Enriched transactions carry per-field
   confidence so downstream consumers gate on evidence.

2. **Data lifecycle (`shared/datainfra`, part 2)**: hot/cold tiering
   overrides age with **access** (still-queried old data stays hot);
   bucketing picks the **coarsest** granularity whose worst live partition
   fits the size target (coarse partitions make queries cheap); repairs are
   windowed, concurrency-capped and never repair a node twice inside 24h;
   retention recommendations are bound by compliance floor, max consumer lag
   and observed replay depth — and a floor **above** current retention
   returns "increase", never "no change".

3. **Vulnerability & maturity scoring**: reachability dominates CVSS — an
   unreachable finding is scaled to ~30% of its base score. Composite
   maturity scores expose their weights and call out the weakest dimension.

## Consequences

- Data-platform health became a queryable API, not a tribal dashboard.
- Adding a bank/provider/repair-window is a config change reviewed like code.
- Retention changes below a compliance floor are structurally unrepresentable.

## Alternatives considered

Pure age-based tiering (rejected: 6-month-old hot partitions go cold while
still serving reads); CVSS-only prioritisation (rejected: buries reachable
7.5s under unreachable 9.8s).
