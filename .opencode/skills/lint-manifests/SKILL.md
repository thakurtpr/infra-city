---
name: lint-manifests
description: Validate Kubernetes manifests and Helm chart for correctness and security
keywords: [manifest, manifests, k8s, kubernetes, yaml, helm, rbac, kubeconform]
---

# Lint-manifests skill

Auto-load when the session touches `helm/`, `demo/`, `deployments/`, or any
`*.yaml` under the repo. Run the full gate before `helm install/upgrade` or
`kubectl apply`:

```bash
helm lint helm/infracity
helm template infracity helm/infracity --set clusterId=lint-check > /tmp/rendered.yaml
kubectl apply --dry-run=client -f /tmp/rendered.yaml
kubectl apply --dry-run=client -f demo/manifests/
```

## What to check in review (not just tooling)

- **RBAC** (`helm/infracity/templates/rbac.yaml`): read-only verbs only
  (`get/list/watch`); secrets metadata-only; every rule has a `why` comment.
- **Images**: pinned tags, no `:latest`; match `deploy-config.md` versions.
- **Workloads**: `resources.requests+limits`, `livenessProbe` + `readinessProbe`,
  `runAsNonRoot` where possible; `privileged: true` only behind
  `agent.privilegedEBPF=true` with justification.
- **Services**: selector labels match pod templates; port names consistent with
  probes and the nginx proxy map (`deployments/nginx.conf`).
- **Demo manifests**: confined to namespace `demo`; no hostNetwork/hostPath.

Report as: `PASS` or a table of file:line · rule · fix. Block the deploy on FAIL.
