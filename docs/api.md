# API reference

Base: backend `:8080` (dev `:8080`, UI dev `:5173` proxies `/api` + `/ws`).
All `GET`s return JSON; lists serialize as `[]`, never `null`.

## Ops

- `GET /health` → `{"status":"ok","uptime":...}`
- `GET /ready` → `{"ready":true}`
- `GET /metrics` → Prometheus (incl. `infracity_ingest_latency_seconds`)
- `GET /api/self` → `graphNodes, graphEdges, wsConnections, wsDropped,
  ingestTotal, eventsDropped, evictedNodes, evictedEdges, uptime`

## Graph

- `GET /api/graph?cluster=&namespace=` → `{nodes[], edges[]}`
- `GET /api/graph/{id}` → subgraph rooted at id
- `GET /api/clusters` → `[{id, health, nodeCount, podCount, ...}]`
  (health folds incident severity: critical > degraded > healthy)
- `GET /api/clusters/{id}`, `GET /api/namespaces?cluster=`,
  `GET /api/resources`, `GET /api/metrics?cluster=` (command center:
  req/s, error %, p95, pods/services/flows)
- `GET /api/search?q=` — `payments`, `svc:checkout`, `ns:payments`,
  `cluster:production`; camera flies to the first hit

## Analysis

- `GET /api/dependencies` → inferred `[{source, target, confidence…}]`
- `GET /api/incidents` → firing `[{id, title, rootNode, severity…}]`
- `GET /api/blast/{node-id}` → `{downstream[], upstream[]}`
- `GET /api/risk/{node-id}` → `{score (0-100), details}`
- `GET /api/explain?target=` → `{explanation, upstream[]}` — walks the
  services the target calls; every claim cites observed metrics
- `GET /api/changes` → last 1000 changes (newest last)
- `GET /api/path?from=&to=` → `{path[], hops[]}` shortest path

## Time travel

- `GET /api/snapshots` → metadata list (payloads stripped)
- `GET /api/snapshots/{id}` → full `{nodes[], edges[]}` (ring first,
  Postgres fallback when configured)

## Ingest + live

- `POST /api/v1/ingest` → `202 {accepted, created, updated}`; `400` on bad
  report, `401` with wrong/missing Bearer token when `--auth-token` is set
  (empty = open, dev only). 8 MiB body cap.
- `GET /ws/events` → WebSocket stream (`hello` frame first). Plain GET
  returns non-200 by design; slow clients are dropped and counted.
