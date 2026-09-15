# /fix-issue — fix-review-findings slash command

Invoke as `/fix-issue` (or `/project:fix-issue`). Applies fixes for findings
from a `/review` or security audit, one finding at a time.

## Usage

- `/fix-issue` — fix all open findings from the last review, highest severity first
- `/fix-issue <finding-id>` — fix a single finding, e.g. `/fix-issue R-3`

## Procedure

1. Restate the finding (file:line · severity · issue) before touching code.
2. Make the minimal edit that resolves it — no drive-by refactors.
3. Re-run the applicable `AGENTS.md` verification for the touched area:
   - Go: `go build ./... && go test ./...`
   - Frontend: `cd frontend && npx tsc --noEmit`
   - Manifests: `helm lint helm/infracity` (+ dry-run apply)
4. Report: fixed / verified-with (commands + output) / remaining findings.
5. Never fix a security HIGH by downgrading the check — remediate the cause.
   If a finding is a false positive, say why and leave the code unchanged.
