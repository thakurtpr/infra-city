# Security model

- Least-privilege RBAC (`helm/infracity/templates/rbac.yaml`): get/list/watch only,
  each rule documented. No cluster-admin.
- **Secret values are never collected.** Agent lists secret metadata (name, type,
  key count) and never mounts or reads `.data`.
- Agent → backend: TLS + per-cluster Bearer token (`INFRACITY_TOKEN`). Backend
  rejects unauthenticated ingest when `--auth-token` is set (always set in prod).
- Agent runs read-only root FS, non-root, drops ALL caps by default; privileged
  eBPF mode is explicit (`agent.privilegedEBPF=true`) and documented.
- Frontend is read-only; no mutating API exists in v0.1.
