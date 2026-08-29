# ADR-012: Kubernetes Orchestration

## Status

Accepted

## Context

Nexora requires a production deployment platform that supports auto-scaling, self-healing, rolling updates, and service discovery across 18 microservices. The platform must handle varying load patterns (e.g., salary day spikes) and provide isolation between critical financial services and non-critical workloads.

## Decision

We will deploy Nexora on Kubernetes using Helm-style manifests for configuration management.

## Alternatives

### AWS ECS
- **Pros**: Simpler operational model, deep AWS integration, Fargate for serverless
- **Cons**: AWS lock-in, less flexible scheduling, no native gRPC load balancing

### HashiCorp Nomad
- **Pros**: Multi-runtime (VMs, containers, jobs), simpler architecture, HashiCorp ecosystem
- **Cons**: Smaller community, less ecosystem tooling, fewer managed offerings

### Docker Swarm
- **Pros**: Simple to set up, native Docker integration
- **Cons**: Limited scaling, weak service mesh, minimal production adoption

## Trade-offs

### Gained
- Industry-standard orchestration with broad tooling ecosystem
- Auto-scaling (HPA, VPA, Cluster Autoscaler) for traffic spikes
- Self-healing with pod restart policies and readiness/liveness probes
- Namespace isolation between critical and non-critical services
- Rolling updates with zero-downtime deployments
- Native service discovery and internal load balancing

### Lost
- Simpler operational model
- Lower cluster management overhead
- Fewer infrastructure abstractions

## Consequences

### Positive
- Each service scales independently based on its own metrics
- Critical payment services can be isolated in dedicated node groups
- Rolling updates enable safe deployment of ledger and transfer changes
- Kubernetes operators can manage complex stateful services (Cassandra, Kafka)
- Standard manifests enable consistent deployment across environments

### Negative
- Need to manage and operate a Kubernetes cluster
- Steeper learning curve for team members unfamiliar with K8s
- Resource requests and limits require careful tuning for financial workloads
- YAML manifests can become complex without proper templating

## Implementation Notes

### Namespace Isolation
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: nexora-financial
  labels:
    tier: critical
---
apiVersion: v1
kind: Namespace
metadata:
  name: nexora-support
  labels:
    tier: standard
```

### Resource Quotas for Financial Services
```yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: financial-quota
  namespace: nexora-financial
spec:
  hard:
    requests.cpu: "16"
    requests.memory: 32Gi
    limits.cpu: "32"
    limits.memory: 64Gi
```

### Horizontal Pod Autoscaler
```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: payment-service-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: payment-service
  minReplicas: 3
  maxReplicas: 20
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
```
