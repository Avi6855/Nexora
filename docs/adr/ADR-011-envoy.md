# ADR-011: Envoy API Gateway

## Status

Accepted

## Context

Nexora needs an edge API gateway to handle routing, retries, circuit breaking, rate limiting, and distributed tracing across all services. The gateway must support both HTTP/1.1 and gRPC, provide rich observability, and integrate with the existing authentication layer.

## Decision

We will use Envoy Proxy as the edge gateway for all inbound traffic to Nexora services.

## Alternatives

### Nginx
- **Pros**: Mature, lightweight, huge ecosystem
- **Cons**: Limited native gRPC support, less extensible, weaker observability

### Kong
- **Pros**: Plugin-based, admin UI, commercial support
- **Cons**: Lua-based plugins harder to maintain, added licensing cost

### Traefik
- **Pros**: Easy configuration, auto-discovery, Let's Encrypt integration
- **Cons**: Less control over low-level proxy behavior, limited gRPC support

## Trade-offs

### Gained
- First-class gRPC and HTTP/2 support
- Rich observability with built-in stats, tracing, and access logging
- Fine-grained circuit breaking, retries, and rate limiting via configuration
- Extensibility via Lua and WebAssembly filters
- Industry-standard service mesh compatibility for future growth

### Lost
- Simpler configuration model
- Lower operational overhead for small teams
- Readymade admin UI for route management

## Consequences

### Positive
- All traffic flows through Envoy, providing a unified observability layer
- Circuit breaking prevents cascade failures across payment, transfer, and ledger services
- gRPC support enables efficient inter-service communication
- Consistent rate limiting and retry policies across all endpoints
- Foundation for future service mesh adoption without gateway replacement

### Negative
- Envoy configuration is YAML-heavy and harder to learn
- Additional infrastructure component to operate and monitor
- Debugging proxy-level issues requires Envoy-specific expertise

## Implementation Notes

### Gateway Configuration
```yaml
static_resources:
  listeners:
    - address:
        socket_address:
          address: 0.0.0.0
          port_value: 8443
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                route_config:
                  virtual_hosts:
                    - name: nexora
                      routes:
                        - match: { prefix: "/nexora.payment" }
                          route:
                            cluster: payment-service
                            retry_policy:
                              retry_on: 5xx
                              num_retries: 3
```

### Circuit Breaking
```yaml
circuit_breakers:
  thresholds:
    - max_connections: 1024
      max_pending_requests: 512
      max_requests: 2048
      max_retries: 3
```
