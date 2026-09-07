# ADR-022: Service Observability — Prometheus /metrics and Grafana

## Status

Accepted — **implemented**.

## Context

Each service previously owned a private, in-memory metrics collector that was
not wired to any HTTP endpoint, so nothing could actually be observed: no
request rates, no latency percentiles, no error rates, no way to watch a card
authorization decision live. Interview-grade and production-grade systems need
a single place to answer "is the platform healthy and how fast is it?"

## Decision

Expose **Prometheus text-format /metrics on every instrumented service** and
scrape with Prometheus, visualized in Grafana.

1. `shared/telemetry` (prom.go + httpmw.go) implements a small, dependency-
   free Prometheus exposition layer: label-dimensioned counters and cumulative
   histograms with Prometheus' default latency buckets, rendered in the text
   format. No client_golang dependency is added to the 17 service modules.
2. Each service registers `/metrics` and wraps its router with an HTTP
   middleware that records `http_requests_total{service,method,route,status}`
   and `http_request_duration_seconds{...}`. Dynamic path segments (UUIDs,
   ids) are collapsed into `:id`/`:n` before becoming label values so
   cardinality stays bounded.
3. Domain metrics complement the HTTP view — e.g.
   `card_authorization_decisions_total{outcome,reason}` on card-service.
4. `docker-compose` runs `prometheus` (scraping all instrumented services
   every 5s over the compose network) and `grafana` with a provisioned
   datasource and a "MonzoBank Platform" dashboard (requests/sec, p95
   latency, 5xx rate, authorization decisions, authorization p95).

## Consequences

- Real dashboards during the demo: run the card flow and watch approved vs
  declined decisions and the authorization latency render live in Grafana
  (http://localhost:3000, admin/monzo).
- The exposition subset (counters + histograms) covers the panels we use;
  adding gauges/quantiles later is a local change in one shared file.
