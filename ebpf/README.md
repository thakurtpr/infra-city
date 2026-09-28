# eBPF design

Probes (`ebpf/sock-trace.bpf.c`): `inet_sock_set_state` tracepoint fires on
`TCP_ESTABLISHED`. Key = (saddr, daddr, sport, dport, family, proto) with the
local endpoint first; value adds (cumulative conns, last-seen ns, bytes
tx/rx, pid, comm). TCX ingress/egress programs add whole-frame wire bytes to
keys the tracepoint created (a miss = pre-existing connection with no PID,
ignored). Userspace consumes entries delete-after-read, so each sample
reports per-interval counts. IPs resolve to pods via the discovery pod-IP
map; `Process` carries `comm(pid)`; edge `bytesPerSec` is measured, not
synthesized.

## CO-RE: compile once, run everywhere (with BTF)

Kernel struct accesses use `BPF_CORE_READ` against the **target** kernel's
BTF, so the object needs no per-node compilation. The node kernel MUST
expose `/sys/kernel/btf/vmlinux` — without it loading fails and the agent
logs the reason and samples `/proc/net/tcp` (same edge schema, no PID).

Byte order in the map key: network order (raw `__be32` bytes for v4,
`u6_addr8` for v6). Go mirrors the layout exactly
(`agent/internal/ebpf/flowconv.go`, size-asserted by tests).

## Build

```bash
make ebpf        # pinned clang toolchain (docker) -> agent/internal/ebpf/bpf/*.o (gitignored)
make agent-full  # GOOS=linux go build -tags ebpf_full ./agent/cmd -> bin/agent-ebpf
```

Default builds (`go build ./...`) never touch this path: no objects, no
`cilium/ebpf` in the compile graph (Apache-2.0, passes the license gate).
`INFRACITY_EBPF=off` forces the `/proc` fallback even in full builds.

## Privileges

`CAP_BPF + CAP_NET_ADMIN` (or privileged DaemonSet). Helm wires it:

```bash
helm install infracity helm/infracity --set agent.privilegedEBPF=true
```

That one flag wires the whole privileged path: `hostNetwork` (node netns),
`ClusterFirstWithHostNet` (Service DNS still works with host networking),
`tracefs`/`debugfs` host mounts (tracepoint attach), uid 0 + `BPF/NET_ADMIN`
caps (program load). Pair it with the `agent-ebpf` image target (distroless
root — the default agent image is nonroot and can never load programs).

Without privileges the agent logs the reason and samples `/proc/net/tcp` —
same edge schema, lower fidelity (no per-process flows). `EBPFEnabled` is
reported in every AgentReport and surfaced at `/api/self`.

## Verify on kind

```bash
docker build -t infracity/agent:ebpf -f Dockerfile --target agent-ebpf \
  --build-arg TAGS=ebpf_full .   # objects must exist: run `make ebpf` first
kind load docker-image infracity/agent:ebpf --name infracity
helm upgrade --install infracity helm/infracity \
  --set agent.image=infracity/agent:ebpf --set agent.privilegedEBPF=true
kubectl logs daemonset/infracity-agent | grep -i ebpf
curl localhost:8080/api/self | jq .ebpfEnabled
```

## Roadmap

- cgroup-id → container → pod attribution (today: IP→pod map + `comm(pid)`).
- IPv6 extension-header walk + 802.1Q-tagged parsing (skipped in v1).
