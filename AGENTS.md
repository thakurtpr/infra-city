# AGENTS.md — InfraCity

> Primary instruction file. Loaded every session via `opencode.jsonc → instructions`.

## Project overview

InfraCity is a real-time 3D infrastructure digital twin: a Go DaemonSet agent
(Kubernetes discovery + eBPF with `/proc` fallback) ships topology to a Go
control plane (graph engine + REST + WebSocket), rendered as a living 3D city
by a React + TypeScript + Three.js frontend. Deployed via Helm; demoed on `kind`.

## Architecture (source of truth: `docs/architecture.md`)

```
agent/      DaemonSet: discovery (client-go), netmon (/proc), ebpf loader, exporter (TLS + retries)
backend/    graph engine, ingest API, REST+WS API, dependency/incident analysis, snapshot store
ebpf/       C probes (sock-trace.bpf.c) + loader docs
frontend/   Vite + React + Three.js city, command center, inspector
helm/       chart (least-privilege RBAC in templates/rbac.yaml)
pkg/model   shared Node/Edge/Event types — change here propagates everywhere
```

## Coding conventions

- **Go**: `gofmt` clean, `go vet ./...` passes. Small exported surface; document
  exported funcs. Errors wrapped with context (`fmt.Errorf("...: %w", err)`).
  No new external deps without justification in the PR.
- **Frontend**: strict TypeScript (`npx tsc --noEmit` must pass). No `any`
  without a `// reason:` comment. Three.js disposals on rebuild
  (geometry/material/texture) — leaking GPU resources is a bug.
- **Shared model first**: new telemetry fields go in `pkg/model/types.go`
  before agent/backend/frontend touch them.
- **No secret values, ever**: secrets = metadata only (name/type/key-count).
  Flag any code path that reads secret `.data`.
- **K8s manifests**: least-privilege RBAC, every rule commented with *why*.
  Resource requests+limits on all workloads; liveness + readiness probes.
- **Ports**: backend `:8080`, agent health `:8081`, UI `:5173` (dev) / `:80` (nginx).

## Verification (definition of done)

A change is done only when **all** applicable checks pass:

```bash
go build ./... && go test ./...          # backend + agent
cd frontend && npx tsc --noEmit          # strict types
helm lint helm/infracity                 # chart
```

 Plus, for user-facing or pipeline changes:
- Backend change → `curl` the touched endpoint against demo mode
  (`INFRACITY_DEMO=1 go run ./backend/cmd --addr=:8080 --demo`).
- Agent change → `kubectl logs daemonset/infracity-agent` on kind shows sane output.
- Frontend change → `npm run build` succeeds; screenshot for 3D changes.
- Manifest change → `/project:lint-manifests` (kubeconform-style checks).

## Safety rules (enforced by `.opencode/plugins/validate.ts`)

- Never `kubectl delete` / `helm uninstall` / `kind delete` without explicit user ask.
- Never print or exfiltrate secret values, tokens, kubeconfigs.
- `bash: ask`, `edit: ask` are the configured permission defaults — destructive
  or wide-blast-radius ops must go through approval.
- eBPF privileged mode (`agent.privilegedEBPF=true`) is opt-in only; default path
  must always work unprivileged via `/proc` fallback.
