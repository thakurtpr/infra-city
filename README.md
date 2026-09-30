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

One script, any kubectl-connected cluster (kind/minikube/k3d load images
directly; remote clusters need `REGISTRY=...`):

```bash
curl -fsSL https://raw.githubusercontent.com/thakurtpr/infra-city/main/install.sh | bash
# EBPF=1 DEMO=1 NAMESPACE=infra bash install.sh  # full-fidelity + demo town
```

Or step by step:

```bash
helm install infracity helm/infracity --set clusterId=production
kubectl port-forward svc/infracity-ui 8080:80  # http://localhost:8080
```

Chart defaults reference published images; until the first GHCR cut, build
them locally (`make docker`) and `kind load` each image before installing.

Chart defaults point at `ghcr.io/infracity/*:0.1.0`; cutting a release means
building (`make docker`, plus `make ebpf` + `TAGS=ebpf_full` for the agent),
pushing (needs a token with `write:packages`:
`gh auth refresh -s write:packages`), and bumping the tags in
`helm/infracity/values.yaml`.

With a demo town + chaos scripts for portfolio walkthroughs:

```bash
make demo           # frontend/checkout/api/auth/payments/orders/postgres/redis/kafka town + traffic
make inject-latency # watch postgres → payments → checkout light up red (needs tc in DB image)
make kill-pod       # watch rollout animation
make scale-api      # watch buildings grow
```

Zero-cluster alternative (always works): `INFRACITY_CHAOS_LATENCY_MS=2500 make dev`
drives the same incident story from the demo backend — clear the variable to
watch the city heal. `INFRACITY_CHAOS_ERRORS_PCT=25` faults errors instead.

## What it does

- **City metaphor**: cluster = city, namespace = district, deployment = building
  (height CPU, width memory, glow traffic, red errors), service = road,
  gateway = entrance, DBs get role identities (postgres/redis/kafka…).
- **Live network topology**: animated packets sized by req/s; per-road latency,
  bytes/s, errors, protocol/ports.
- **Dependency inference** with confidence scores — no manual DAG.
- **Incident mode + blast radius + "why is this slow?"** explainer grounded in
  real metrics. **Risk scores** flag single points of failure.
- **Time travel** snapshots (ring + optional Postgres log), **rollout visibility**
  (live replicas/restarts), **security mode** (allowed/denied), **heatmap/cost**
  building modes, **path explorer** (`GET /api/path?from=&to=`), **"what changed?"** feed.
- **Command center**: req/s, error %, p95, pods/services/flows + active incidents.
- **Search**: `payments`, `svc:checkout`, `ns:payments`, `cluster:production` —
  camera flies to the result.
- **Self-observability**: `/metrics` (Prometheus), `/health`, `/ready`,
  `/api/self` (graph size, WS conns/drops, ingest + eviction totals).

## Repo layout

```
agent/      DaemonSet: K8s discovery, eBPF + /proc flows, TLS exporter w/ retries
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
18ms → 420ms → `make scale-api` → buildings visibly grow with traffic
shifting to new replicas. See `docs/architecture.md` for tradeoffs
(why eBPF / WS / Three.js / DaemonSet).
