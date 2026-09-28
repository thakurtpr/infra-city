# SPDX-License-Identifier: Apache-2.0
.PHONY: dev demo backend frontend agent test lint build docker helm inject-latency inject-errors kill-pod scale-api ebpf ebpf-test ebpf-verify agent-full

BACKEND_ADDR ?= :8080
EBPF_BUILDER ?= infracity/ebpf-builder:24.04
EBPF_OBJ := agent/internal/ebpf/bpf/sock-trace.bpf.o

# Local dev: backend in demo mode + frontend hot reload (no cluster needed).
dev:
	@echo "==> starting backend (demo city) on $(BACKEND_ADDR)"
	INFRACITY_DEMO=1 go run ./backend/cmd --addr=$(BACKEND_ADDR) & echo $$! > /tmp/infracity-backend.pid
	@echo "==> starting frontend"
	cd frontend && (npm install --silent 2>/dev/null || true) && npm run dev

stop-dev:
	-kill `cat /tmp/infracity-backend.pid` 2>/dev/null || true

backend:
	INFRACITY_DEMO=1 go run ./backend/cmd --addr=$(BACKEND_ADDR) --demo

frontend:
	cd frontend && npm run dev

agent:
	go run ./agent/cmd --backend=http://localhost:8080 --cluster=dev --insecure

# eBPF: compile the CO-RE probe with the pinned toolchain (no host clang
# needed), then build a linux agent with the real loader. Default builds
# never need this; the DaemonSet does (agent.privilegedEBPF=true).
ebpf:
	docker build -t $(EBPF_BUILDER) -f ebpf/Dockerfile.builder ebpf/
	docker run --rm -v "$(CURDIR)/ebpf:/src:ro" -v "$(CURDIR)/agent/internal/ebpf/bpf:/out" $(EBPF_BUILDER) \
		bash -c "clang -O2 -g -target bpf -c /src/sock-trace.bpf.c -o /out/sock-trace.bpf.o"

agent-full: ebpf
	GOOS=linux go build -tags ebpf_full -o bin/agent-ebpf ./agent/cmd

# Userspace tests for the shared BPF packet parser (no kernel needed):
# same flow_parse.h compiled for host, run against crafted frames.
ebpf-test:
	docker run --rm -v "$(CURDIR)/ebpf:/src:ro" $(EBPF_BUILDER) \
		bash -c "clang -O2 -Wall -Wextra -o /tmp/test_parse /src/tests/test_parse.c && /tmp/test_parse"

# Deep verifier check on the real node kernel: runs the full agent binary
# inside the kind control-plane (native BTF/tracefs/cgroupfs, blackhole
# backend so the live city is untouched), asserts the loader goes active,
# then removes all traces. Catches what clang cannot (verifier precision,
# CO-RE relocations). Needs: kind cluster up.
KIND_NODE ?= infracity-control-plane
ebpf-verify: ebpf
	GOOS=linux go build -tags ebpf_full -o /tmp/infracity-agent-verify ./agent/cmd
	docker cp /tmp/infracity-agent-verify $(KIND_NODE):/root/agent-verify
	docker exec -d $(KIND_NODE) bash -c 'KUBECONFIG=/etc/kubernetes/admin.conf INFRACITY_BACKEND=http://127.0.0.1:1 INFRACITY_CLUSTER=verify NODE_NAME=$(KIND_NODE) INFRACITY_EBPF_DEBUG=1 nohup /root/agent-verify > /root/agent-verify.log 2>&1 & echo $$! > /root/agent-verify.pid'
	sleep 12
	docker exec $(KIND_NODE) grep -a "observer ready" /root/agent-verify.log | grep -q "true" && echo "ebpf active on $(KIND_NODE)"
	docker exec $(KIND_NODE) bash -c 'kill $$(cat /root/agent-verify.pid); rm -f /root/agent-verify /root/agent-verify.log /root/agent-verify.pid'

test:
	go test ./... 2>&1 | tail -20
	cd frontend && (npm run build 2>&1 | tail -5)

lint:
	go vet ./...
	cd frontend && npx tsc --noEmit

build:
	go build -o bin/backend ./backend/cmd
	go build -o bin/agent ./agent/cmd
	cd frontend && npm run build

docker:
	docker build -t infracity/backend:0.2.0 -f Dockerfile --target backend .
	docker build -t infracity/agent:0.2.0 -f Dockerfile --target agent .
	docker build -t infracity/frontend:0.2.0 -f Dockerfile.frontend .

helm:
	helm lint helm/infracity
	helm template infracity helm/infracity --set clusterId=demo | head -50

# Demo against a real cluster: sample workloads + traffic generator.
demo:
	kubectl apply -f demo/manifests/
	@echo "Traffic generator starting…"
	kubectl apply -f demo/traffic-generator.yaml
	@echo "Install InfraCity: helm install infracity helm/infracity"
	@echo "UI: kubectl port-forward svc/infracity-ui 8080:80"

# Portfolio chaos scripts (inject failures to watch the city react).
# inject-latency needs tc in the DB image (stock postgres:alpine has none);
# the guaranteed path is demo-mode chaos: INFRACITY_CHAOS_LATENCY_MS=2500 make dev.
inject-latency:
	kubectl exec -n demo postgres-0 -- tc qdisc add dev eth0 root netem delay 400ms || echo "tc unavailable (postgres is a StatefulSet without iproute2); use INFRACITY_CHAOS_LATENCY_MS=2500 make dev"

# Was: patch a chaos annotation no component reads (theater). Real error
# faults need broken responses, not labels; the guaranteed path is demo-mode
# chaos: INFRACITY_CHAOS_ERRORS_PCT=25 make dev.
inject-errors:
	@echo "no kind-native error fault (annotations change nothing); use INFRACITY_CHAOS_ERRORS_PCT=25 make dev"

kill-pod:
	kubectl delete pod -n demo -l app=api --grace-period=0 --force || echo "run make demo first"

scale-api:
	kubectl scale -n demo deploy/api --replicas=6
