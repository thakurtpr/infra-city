<!-- SPDX-License-Identifier: Apache-2.0 -->
# Contributing to InfraCity

Thanks for helping build the 3D infrastructure digital twin. This guide keeps
reviews fast and the codebase production-grade.

## Ground rules

- **License**: contributions are under Apache-2.0. New source files must start
  with `// SPDX-License-Identifier: Apache-2.0` (or `# ...` for YAML/shell).
- **DCO sign-off required**: every commit must carry `Signed-off-by: Name <email>`
  (`git commit -s`). CI blocks unsigned commits.
- **Secret values are never collected, logged, or printed.** Secrets = metadata
  only. If your change touches secret-adjacent code, say so in the PR.
- **Shared model first**: new telemetry fields go in `pkg/model/types.go`
  before agent/backend/frontend code.

## Definition of done (CI enforces this)

```bash
go build ./... && go vet ./... && go test ./...   # agent + backend
go run golang.org/x/vuln/cmd/govulncheck@latest ./...   # vuln scan
cd frontend && npx tsc --noEmit && npm test -- --run && npm run build
helm lint helm/infracity
helm template infracity helm/infracity --set clusterId=ci > /tmp/rendered.yaml
```

## Workflow

1. Fork, branch from `main` (`feat/<x>`, `fix/<x>`, `docs/<x>`).
2. Keep PRs small and single-purpose. One finding per commit where sensible.
3. Fill in the PR template (what/why, verification output, security checklist).
4. For 3D/UI changes attach a screenshot or short clip.
5. For manifest changes run the lint-manifests gate (see PR template).

## Project layout

`agent/` DaemonSet · `backend/` control plane · `ebpf/` probes ·
`frontend/` React+Three.js city · `helm/` chart · `demo/` kind town ·
`pkg/model` shared types · `docs/` architecture + security model.

## Getting help

Open a discussion or draft PR early. Maintainers review within a few days.
Security issues: see `SECURITY.md` — never file them as public issues.
