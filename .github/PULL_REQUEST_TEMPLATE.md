<!-- SPDX-License-Identifier: Apache-2.0 -->
## What / why

<!-- One paragraph: what changes, why it's needed. Link issue: Fixes #NNN -->

## Verification

<!-- Paste exact command output, e.g. -->

```text
go test ./...            # ok ...
cd frontend && npx tsc --noEmit   # clean
helm lint helm/infracity          # 1 chart(s) linted, 0 failed
```

- [ ] `go build ./... && go vet ./... && go test ./...`
- [ ] `govulncheck ./...`
- [ ] frontend: `tsc --noEmit`, `npm test`, `npm run build`
- [ ] `helm lint` + template dry-run (`kubectl apply --dry-run=client`)
- [ ] UI change → screenshot attached
- [ ] Commits carry `Signed-off-by` (DCO)

## Security checklist

- [ ] No secret values read, logged, or returned (metadata only)
- [ ] No new privileged caps / host mounts (or justified below)
- [ ] RBAC changes are read-only + commented with *why*
- [ ] Ingest/auth behavior unchanged (or described below)
