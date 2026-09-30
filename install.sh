#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# InfraCity one-script installer — works against ANY kubectl-connected cluster.
#
#   curl -fsSL https://raw.githubusercontent.com/thakurtpr/infra-city/main/install.sh | bash
#
# What it does: preflight checks -> fetch sources -> build images ->
# deliver them (kind/minikube/k3d load, or push to $REGISTRY) ->
# helm install -> verify -> print access instructions.
#
# Configuration (env vars, all optional):
#   CLUSTER_ID   cluster identity shown in the city      (default: production)
#   NAMESPACE    kubernetes namespace                    (default: infracity)
#   RELEASE      helm release name                       (default: infracity)
#   VERSION      image tag to build                      (default: 0.2.0)
#   EBPF         1 = privileged full-fidelity agent      (default: 0)
#                (needs node BTF + permission for privileged pods)
#   DEMO         1 = also install the demo town          (default: 0)
#   REGISTRY     registry prefix for remote clusters,
#                e.g. docker.io/you (required unless kind/minikube/k3d)
#   REPO_URL     sources                                 (default: github infra-city)
#   REF          git ref to install                      (default: main)
#   REPO_DIR     use a local checkout instead of cloning (dev only)
#
# Uninstall: curl ... | bash -s -- --uninstall   (env NAMESPACE/RELEASE respected)
set -euo pipefail

CLUSTER_ID="${CLUSTER_ID:-production}"
NAMESPACE="${NAMESPACE:-infracity}"
RELEASE="${RELEASE:-infracity}"
VERSION="${VERSION:-0.2.0}"
EBPF="${EBPF:-0}"
DEMO="${DEMO:-0}"
REGISTRY="${REGISTRY:-}"
REPO_URL="${REPO_URL:-https://github.com/thakurtpr/infra-city.git}"
REF="${REF:-main}"
REPO_DIR="${REPO_DIR:-}"

log()  { printf '==> %s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 not found ($2)"; }

if [ "${1:-}" = "--uninstall" ]; then
	need helm "https://helm.sh/docs/intro/install/"
	need kubectl "https://kubernetes.io/docs/tasks/tools/"
	helm uninstall "$RELEASE" -n "$NAMESPACE" || true
	kubectl delete namespace "$NAMESPACE" --ignore-not-found=true
	log "uninstalled $RELEASE from namespace $NAMESPACE"
	exit 0
fi

# ---- preflight ----
need kubectl "https://kubernetes.io/docs/tasks/tools/"
need helm "https://helm.sh/docs/intro/install/ (brew install helm)"
need docker "https://docs.docker.com/get-docker/"
need git "https://git-scm.com/downloads"
kubectl cluster-info >/dev/null 2>&1 || fail "no reachable cluster (check kubectl config current-context)"
CTX="$(kubectl config current-context 2>/dev/null || true)"
[ -n "$CTX" ] || fail "kubectl has no current context"
log "cluster context: $CTX"

# ---- sources ----
if [ -n "$REPO_DIR" ]; then
	SRC="$REPO_DIR"
	[ -f "$SRC/install.sh" ] || fail "REPO_DIR=$REPO_DIR is not an InfraCity checkout"
	log "using local sources: $SRC"
else
	SRC="$(mktemp -d)/infra-city"
	git clone --depth 1 --branch "$REF" "$REPO_URL" "$SRC" >/dev/null 2>&1 \
		|| fail "cannot clone $REPO_URL@$REF"
	log "cloned $REPO_URL@$REF"
	trap 'rm -rf "$(dirname "$SRC")"' EXIT
fi
cd "$SRC"

# ---- images ----
AGENT_TARGET="agent"
AGENT_TAG="agent:$VERSION"
if [ "$EBPF" = "1" ]; then
	log "building eBPF probe + privileged agent (needs a few minutes once)"
	docker build -t infracity/ebpf-builder:24.04 -f ebpf/Dockerfile.builder ebpf/ >/dev/null
	docker run --rm -v "$PWD/ebpf:/src:ro" -v "$PWD/agent/internal/ebpf/bpf:/out" \
		infracity/ebpf-builder:24.04 \
		bash -c "clang -O2 -g -target bpf -c /src/sock-trace.bpf.c -o /out/sock-trace.bpf.o"
	AGENT_TARGET="agent-ebpf"
	AGENT_TAG="agent:$VERSION-ebpf"
fi
log "building images (backend, agent, frontend :$VERSION)"
docker build -t "infracity/backend:$VERSION" -f Dockerfile --target backend . >/dev/null
if [ "$EBPF" = "1" ]; then
	docker build -t "infracity/$AGENT_TAG" -f Dockerfile --target "$AGENT_TARGET" \
		--build-arg TAGS=ebpf_full . >/dev/null
else
	docker build -t "infracity/agent:$VERSION" -f Dockerfile --target agent . >/dev/null
fi
docker build -t "infracity/frontend:$VERSION" -f Dockerfile.frontend . >/dev/null

# ---- deliver ----
BACKEND_IMG="infracity/backend:$VERSION"
AGENT_IMG="infracity/$AGENT_TAG"
FRONTEND_IMG="infracity/frontend:$VERSION"
case "$CTX" in
kind-*)
	KIND_NAME="${CTX#kind-}"
	log "kind detected (cluster $KIND_NAME): loading images into nodes"
	need kind "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
	kind load docker-image "$BACKEND_IMG" "$AGENT_IMG" "$FRONTEND_IMG" --name "$KIND_NAME"
	;;
minikube*)
	PROFILE="$CTX"
	log "minikube detected: loading images"
	need minikube "https://minikube.sigs.k8s.io/docs/start/"
	if [ "$PROFILE" = "minikube" ]; then
		minikube image load "$BACKEND_IMG" "$AGENT_IMG" "$FRONTEND_IMG"
	else
		minikube image load -p "$PROFILE" "$BACKEND_IMG" "$AGENT_IMG" "$FRONTEND_IMG"
	fi
	;;
k3d-*)
	K3D_NAME="${CTX#k3d-}"
	log "k3d detected (cluster $K3D_NAME): importing images"
	need k3d "https://k3d.io/#installation"
	k3d image import "$BACKEND_IMG" "$AGENT_IMG" "$FRONTEND_IMG" -c "$K3D_NAME"
	;;
*)
	[ -n "$REGISTRY" ] || fail "remote cluster ($CTX): set REGISTRY, e.g. REGISTRY=docker.io/you $0"
	RPREFIX="${REGISTRY%/}"
	BACKEND_IMG="$RPREFIX/infracity-backend:$VERSION"
	AGENT_IMG="$RPREFIX/infracity-agent$( [ "$EBPF" = "1" ] && echo "-ebpf" ):$VERSION"
	FRONTEND_IMG="$RPREFIX/infracity-frontend:$VERSION"
	log "pushing to $RPREFIX"
	docker tag "infracity/backend:$VERSION" "$BACKEND_IMG"
	docker tag "infracity/$AGENT_TAG" "$AGENT_IMG"
	docker tag "infracity/frontend:$VERSION" "$FRONTEND_IMG"
	docker push "$BACKEND_IMG"
	docker push "$AGENT_IMG"
	docker push "$FRONTEND_IMG"
	;;
esac

# ---- install ----
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml 2>/dev/null | kubectl apply -f - >/dev/null
ARGS="--set clusterId=$CLUSTER_ID --set backend.image=$BACKEND_IMG --set agent.image=$AGENT_IMG --set frontend.image=$FRONTEND_IMG"
if [ "$EBPF" = "1" ]; then
	ARGS="$ARGS --set agent.privilegedEBPF=true"
	log "installing $RELEASE (privileged eBPF agent)"
else
	log "installing $RELEASE (unprivileged agent, /proc fallback)"
fi
# shellcheck disable=SC2086
helm upgrade --install "$RELEASE" helm/infracity -n "$NAMESPACE" $ARGS --wait --timeout 5m

if [ "$DEMO" = "1" ]; then
	log "installing demo town"
	kubectl apply -f demo/manifests/ >/dev/null
	kubectl apply -f demo/traffic-generator.yaml >/dev/null
fi

# ---- verify ----
log "verifying"
kubectl rollout status "daemonset/$RELEASE-agent" -n "$NAMESPACE" --timeout=180s >/dev/null
kubectl rollout status "deploy/$RELEASE-backend" -n "$NAMESPACE" --timeout=180s >/dev/null
EBPF_LINE="$(kubectl logs "daemonset/$RELEASE-agent" -n "$NAMESPACE" 2>/dev/null | grep -a "network observer ready" | tail -1 || true)"
log "agent: $(echo "$EBPF_LINE" | grep -ao 'ebpf=[a-z]*' || echo 'starting…')"

cat <<EOF

InfraCity is live in namespace $NAMESPACE (release $RELEASE, cluster $CLUSTER_ID).
  UI:       kubectl port-forward svc/$RELEASE-ui -n $NAMESPACE 8080:80  # http://localhost:8080
  Backend:  kubectl port-forward svc/$RELEASE-backend -n $NAMESPACE 18080:8080
  Uninstall: curl -fsSL https://raw.githubusercontent.com/thakurtpr/infra-city/main/install.sh | NAMESPACE=$NAMESPACE RELEASE=$RELEASE bash -s -- --uninstall
EOF
