# InfraCity — real-time 3D infrastructure digital twin

> **"Don't just show me what exists. Show me how my infrastructure behaves."**

Kubernetes clusters, namespaces, workloads, services, network flows, databases
and external dependencies rendered as a **living 3D city**, fed by a DaemonSet
agent (K8s API + eBPF, `/proc` fallback) through a Go control plane (graph
engine + WebSocket) to a React/Three.js frontend.

## 60-second demo (no cluster needed)

```bash
cd infracity
make dev        # backend demo city :8080 + UI :5173
# open http://localhost:5173 — double-click buildings, watch traffic particles
```

## Real cluster

```bash
helm install infracity helm/infracity --set clusterId=production
kubectl port-forward svc/infracity-ui 8080:80  # http://localhost:8080
```

With a demo town + chaos scripts for portfolio walkthroughs:

```bash
make demo           # sample frontend/api/payments/postgres/redis town + traffic
make inject-latency # watch postgres → payments → checkout light up red
make kill-pod       # watch rollout animation
make scale-api      # watch buildings grow
```

## What it does

- **City metaphor**: cluster = city, namespace = district, deployment = building
  (height CPU, width memory, glow traffic, red errors), service = road,
  gateway = entrance, DBs get role identities (postgres/redis/kafka…).
- **Live network topology**: animated packets sized by req/s; per-road latency,
  bytes/s, errors, protocol/ports.
- **Dependency inference** with confidence scores — no manual DAG.
- **Incident mode + blast radius + "why is this slow?"** explainer grounded in
  real metrics. **Risk scores** flag single points of failure.
- **Time travel** snapshots, **deployment/canary** animation, **security mode**
  (allowed/denied), **heatmap/cost** building modes, **path explorer**
  (`GET /api/path?from=&to=`), **"what changed?"** feed.
- **Command center**: req/s, error %, p95, pods/services/flows + active incidents.
- **Search**: `payments`, `svc:checkout`, `ns:payments`, `cluster:production` —
  camera flies to the result.
- **Self-observability**: `/metrics` (Prometheus), `/health`, `/ready`,
  `/api/self` (graph size, WS conns/drops, ingest latency).

## Repo layout

```
agent/      DaemonSet: K8s discovery, eBPF//proc flows, TLS exporter w/ retries
backend/    graph engine, ingest, REST+WS API, analysis, snapshot store
ebpf/       C probes + loader docs
frontend/   React+TS+Three.js city, command center, inspector
helm/       chart (least-privilege RBAC)
demo/       sample town, traffic generator
docs/       architecture, security, data model, API
```

## Portfolio story

Open production → zoom to `payments` → follow
frontend → checkout → payments → postgres → `make inject-latency` → city reacts
(red glow, incident card, blast radius) → "why slow?" cites postgres p95
18ms → 420ms → canary v2 10% → 100% with traffic visibly shifting. See
`docs/architecture.md` for tradeoffs (why eBPF / WS / Three.js / DaemonSet).
