# Deploy config — InfraCity

Central place for the values the `deploy` skill substitutes as `<tag>` / `<cluster>`.

| Key             | Current value                                  |
|-----------------|------------------------------------------------|
| kind cluster    | `infracity` (context `kind-infracity`, k8s v1.35) |
| backend image   | `infracity/backend:0.1.0`                      |
| agent image     | `infracity/agent:0.1.0`                        |
| frontend image  | `infracity/frontend:0.3.0` (layered connectivity: wiring + flows) |
| helm release    | `infracity`, namespace `default`               |
| clusterId       | `kind-infracity`                               |

## Port-forward map (must re-establish per session)

| Service              | Local → remote |
|----------------------|----------------|
| `svc/infracity-backend` | `localhost:8080 → 8080` (REST + WS) |
| `svc/infracity-ui`      | `localhost:8090 → 80` (city UI)     |

Background them with output to `/tmp/pf*.log`, then `curl` both URLs before
announcing them.

## Demo town (`demo/`)

Namespace `demo`: frontend ×2, api ×3, payments ×2, postgres (StatefulSet),
redis, `traffic-gen` CronJob (every minute). Chaos entry points:
`make inject-latency | make inject-errors | make kill-pod | make scale-api`.

## Bumping versions

When any component image tag changes, update this file AND the
`helm install --set ...image=` invocation in `SKILL.md` so future sessions
deploy the right tags.
