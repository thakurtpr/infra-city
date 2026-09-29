# Data model

Source of truth: `pkg/model` (Go structs) — new telemetry fields land there
first, per `AGENTS.md`. JSON names below are the wire format.

## IDs

Canonical node IDs are `<type>/<cluster>/<namespace>/<name>` (namespace
empty for cluster-scoped kinds), built by `model.IDFor`. Edge IDs are
`<source>|<type>|<destination>`. Examples: `pod/kind-infracity/demo/api-xyz`,
`service/production/payments/payments`.

## Node

`id, type, cluster, namespace?, name, labels?, annotations?, status?,
kind?, version?, role?` (database role: `postgres|redis|kafka|...`),
`owner?, nodeName?, ip?, createdAt?, updatedAt?, metadata?, metrics?`.

Node types: `cluster, node, namespace, deployment, daemonset, statefulset,
pod, service, ingress, gateway, configmap, secret, pvc, job, cronjob,
database, external`.

## Metrics (point-in-time; history lives in snapshots)

`cpuCores, cpuPct, memBytes, memPct, replicas, readyReplicas, restarts,
reqPerSec, errRate (0..1), latencyMsP50, latencyMsP95, bytesPerSec,
costPerMonth`. All `omitempty`: absent means unobserved, never zero-as-data.

## Edge

`id, source, destination, type, protocol?` (TCP/UDP/HTTP/gRPC…),
`srcPort?, dstPort?, requestsPerSec?, latencyMs?, bytesPerSec?,
errorsPerSec?, connections?, confidence?` (dependency inference 0..1),
`allowed?` (security mode: nil = unknown), `updatedAt?`.

Edge types: `owns` (cluster→node, deploy→pod…), `targets` (service→pod),
`routes` (ingress/gateway→service), `runs_on` (pod→node), `network`
(observed traffic), `depends` (inferred).

Tracepoint keys are local-first: the eBPF flow source is always the local
endpoint, so cgroup attribution resolves the source even for
host-network/localhost traffic.

## Events, incidents, dependencies

- `Event`: `type` (`RESOURCE_CREATED|UPDATED|DELETED|NETWORK_FLOW|
  METRIC_UPDATE|…`), `resource?, edge?, message?, timestamp`. Streamed on
  `/ws/events` (opens with a `hello` frame, then 25s pings).
- `Incident`: `id, title, rootNode, status (firing|resolved), severity,
  startedAt, resolvedAt?, affected[], description?`. Detectors: node
  `errRate ≥ 5/20%` (warning/critical), `p95 ≥ 500/2000ms`, `restarts ≥ 5`.
  `affected` is root + downstream; the full blast radius (both directions)
  comes from `GET /api/blast/{id}`.
- `Dependency`: `source, target, confidence, reqPerSec, latencyMs, errRate`
  — inferred from sustained traffic, never hand-written.
- `Change`: `id, timestamp, kind (added|removed|modified|scale|deploy|
  dependency), summary, nodeId?` — the "what changed?" feed (last 1000,
  eviction is GC and records nothing).
- `Snapshot`: `id, timestamp, nodes[], edges[], label?` — time travel.
  Hot ring (288) + optional Postgres log (`--postgres-dsn`); `List`
  strips payloads, `Get` returns full.
- `AgentReport` (POST `/api/v1/ingest`): `clusterId, nodeName,
  timestamp (unix nano), nodes[], edges[], events?, stats
  (eventsPerSec, flowsPerSec, droppedEvents, ebpfEnabled)`.
- `AgentStatus` (GET `/api/self` → `agents[]`): `clusterId, ebpfEnabled,
  flowsPerSec?, eventsPerSec?, droppedEvents?, lastSeen (unix nano)` —
  self-observability for the observers.
