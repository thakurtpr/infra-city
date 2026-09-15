# /review — code review slash command

Invoke as `/review` (or `/project:review`). Delegates to the `code-reviewer`
sub-agent (`.opencode/agents/code-reviewer.md`).

## Usage

- `/review` — review uncommitted/working-tree changes
- `/review <ref>` — review a specific ref, e.g. `/review HEAD~3`, `/review main..HEAD`
- `/review <path>` — scope the review, e.g. `/review backend/internal/graph`

## Procedure

1. Determine scope (default: working tree diff).
2. Hand the diff + `AGENTS.md` conventions to the `code-reviewer` agent.
3. Return its verdict (`APPROVE` / `REQUEST CHANGES` / `COMMENT`) and findings
   table verbatim; do not soften blockers.
4. If verdict is `REQUEST CHANGES`, offer to fix via `/fix-issue`.
