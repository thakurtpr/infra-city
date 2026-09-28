# SPDX-License-Identifier: Apache-2.0
.PHONY: dev demo backend frontend agent test lint build docker helm chaos-latency chaos-errors kill-pod scale-api ebpf agent-full

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
	docker build -t infracity/backend:0.1.0 -f Dockerfile --target backend .
	docker build -t infracity/agent:0.1.0 -f Dockerfile --target agent .
	docker build -t infracity/frontend:0.1.0 -f Dockerfile.frontend .

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
inject-latency:
	kubectl exec -n demo deploy/postgres -- tc qdisc add dev eth0 root netem delay 400ms || echo "demo postgres not found; run make demo first"

inject-errors:
	kubectl patch -n demo deploy/api --patch '{"spec":{"template":{"metadata":{"annotations":{"chaos":"errors"}}}}}' || echo "run make demo first"

kill-pod:
	kubectl delete pod -n demo -l app=api --grace-period=0 --force || echo "run make demo first"

scale-api:
	kubectl scale -n demo deploy/api --replicas=6
