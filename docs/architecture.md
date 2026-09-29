# Architecture

```
K8s cluster ──► InfraCity agent (DaemonSet) ──► Backend ──► React/Three.js
                     │                              │
              K8s API + eBPF(/proc)          graph + WS + snapshots
```

## Why eBPF?
Kubernetes API shows *what exists*; eBPF shows *how it behaves*: real TCP/UDP
flows with PID/cgroup attribution. Cilium proved the model; we borrow it for
observability. Portable fallback (/proc/net/tcp) keeps unprivileged installs useful.

## Why WebSockets?
Topology deltas + flow samples are small, frequent, and ordered — ideal for a
single multiplexed WS stream (`/ws/events`) instead of polling. Slow clients are
dropped and counted, never blocking ingestion.

## Why Three.js?
Instanced boxes + line-segment edges + point-sprite packets scale to 10k+ pods
with LOD (cluster → namespace → workload → pod → flow). 2D tables remain as
accessible fallback via the same REST API.

## Why DaemonSet?
Per-node flow observation must live where the sockets live. Node-scoped pod
listing (`spec.nodeName`) keeps API load O(nodes), not O(nodes × pods).

## Dependency inference
No manual `A depends on B`. Sustained network edges become `depends` edges with
confidence = f(request volume, connection stability). UI surfaces the score.

## Scaling
In-memory graph + snapshot ring today; PostgreSQL + TimescaleDB tomorrow behind
the same store interface. Edge aggregation (127 flows → 1 road) keeps WebGL load flat.
Graph GC (`--graph-ttl`, default 5m) evicts entries no report refreshed — dead
flows and deleted workloads stop haunting the city and memory stays bounded.
Snapshots already taken keep history for time travel; demo mode heartbeats its
whole world every 2s so the synthetic city never self-evicts.

## Snapshot durability
`SnapshotStore` is a hot ring (288 entries) with an optional durable
`SnapshotBackend`. Set `--postgres-dsn` (or `INFRACITY_POSTGRES_DSN`) and
every snapshot spills to a `snapshots` table (JSONB payloads, composite
PK for a Timescale hypertable when the extension exists); reads fall back
past the ring, so time travel survives restarts. Spills are fail-open
(ring keeps serving, failures warn); a configured-but-unreachable database
is a startup error. `--snapshot-retention` (e.g. `720h`) deletes durable
snapshots older than the window on every snapshot tick (default keeps
forever). In Helm: `--set backend.postgresDSN=...` for dev, or a
Secret (`kubectl create secret generic pg --from-literal=dsn=...`) with
`--set backend.postgresExistingSecret=pg` for prod.

## Live signal vs demo mode
The unprivileged agent reads its own network namespace (`/proc/net/tcp{,6}`),
so on a real cluster it sees node-level topology + wiring (services, endpoints,
ingresses) but almost no pod-to-service L7 flows — the command center honestly
reports `— L4 only` and the city renders the declared-wiring layer. Full flow
fidelity needs the privileged eBPF path (`agent.privilegedEBPF=true` plus an
agent built with `make agent-full`, objects per `ebpf/README.md`; requires
node BTF, otherwise it degrades with a logged reason). `make dev` / demo mode
synthesizes traffic so the traffic/incident/canary story is explorable with
zero cluster.
