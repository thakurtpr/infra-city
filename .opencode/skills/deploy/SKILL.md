---
name: deploy
description: Build, load, and roll out InfraCity to kind or Helm releases
keywords: [deploy, kind, helm, rollout, release, image]
---

# Deploy skill

Full rollout path for InfraCity. See `deploy-config.md` for image tags,
cluster names, and port-forward map. Steps:

## kind (local dev cluster)

```bash
# 1. images (bump tag when frontend/backend changes)
docker build -t infracity/backend:<tag> -f Dockerfile --target backend .
docker build -t infracity/agent:<tag> -f Dockerfile --target agent .
docker build -t infracity/frontend:<tag> -f Dockerfile.frontend .
kind load docker-image infracity/backend:<tag> infracity/agent:<tag> infracity/frontend:<tag> --name <cluster>

# 2. demo town + traffic (idempotent)
kubectl apply -f demo/manifests/
kubectl apply -f demo/traffic-generator.yaml

# 3. install or upgrade (first install only)
helm install infracity helm/infracity --set clusterId=<cluster> \
  --set backend.image=infracity/backend:<tag> \
  --set agent.image=infracity/agent:<tag> \
  --set frontend.image=infracity/frontend:<tag>
# subsequent: kubectl set image deploy/<name> <container>=infracity/<comp>:<tag>
#             + kubectl rollout status deploy/<name>

# 4. verify
kubectl rollout status deploy/infracity-backend
kubectl logs daemonset/infracity-agent --tail=5   # expect "report queued"
curl -s localhost:8080/api/clusters              # needs backend port-forward
curl -s -o /dev/null -w "%{http_code}\n" localhost:8090/   # UI
```

## Safety

- Never `helm uninstall`, `kubectl delete`, or `kind delete cluster` without
  explicit user approval (also enforced by `plugins/validate.ts`).
- Port-forwards die across sessions — re-establish and re-verify with `curl`
  before telling the user a URL is live.
- After any frontend change: `npx tsc --noEmit` in `frontend/` BEFORE building
  the image; a broken bundle deployed to kind wastes a full rebuild cycle.
