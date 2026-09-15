<!-- SPDX-License-Identifier: Apache-2.0 -->
# Security Policy

## Supported versions

| Version | Supported          |
|---------|--------------------|
| 0.3.x   | :white_check_mark: |
| < 0.3   | :x:                |

InfraCity is pre-1.0. Security fixes land on the latest minor release; CVEs in
shipped images are addressed as a priority over features.

## Reporting a vulnerability

**Do not open a public issue.** Email the maintainers instead:

- **thakurtpr** via GitHub Security Advisories on
  [thakurtpr/infra-city](https://github.com/thakurtpr/infra-city/security/advisories/new)
  (preferred — keeps the report private and tracked).

Include: affected component (`agent` / `backend` / `frontend` / `helm`),
version or image tag, reproduction steps or PoC, and impact assessment.

We aim to acknowledge within **3 business days** and to ship or credibly
schedule a fix within **30 days** for high-severity issues.

## Scope notes (project-specific)

- The agent collects **secret metadata only** (name/type/key-count). Any report
  showing secret *values* in logs, events, API responses, or snapshots is
  treated as **critical**.
- eBPF privileged mode (`agent.privilegedEBPF`) is intentionally opt-in; the
  default install must remain effective unprivileged. Privilege-escalation
  paths out of the default install are in scope.
- The ingest endpoint (`POST /api/v1/ingest`) requires a Bearer token whenever
  `backend.authToken` is set (always set outside local dev). Missing-auth
  bypasses are in scope.

## What happens next

Accepted reports are fixed privately, released with a changelog entry, and
credited (reporter named unless anonymity is requested). Low-severity hardening
suggestions may be converted to public tracking issues instead.
