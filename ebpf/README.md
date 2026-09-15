# eBPF design

Probes (`ebpf/sock-trace.bpf.c`): `inet_sock_set_state` tracepoint + TC egress
for byte counts. Key = (saddr, daddr, sport, dport, proto); value adds
(bytes_tx/rx, conns, pid, cgroup_id). Userspace resolves cgroup → container →
pod via kubelet cache; IPs resolve via discovery pod-IP map.

Privileges: `CAP_BPF + CAP_NET_ADMIN` (or privileged DaemonSet). Without them the
agent logs the reason and samples `/proc/net/tcp` — same edge schema, lower
fidelity (no per-process bytes). `EBPFEnabled` is reported in every AgentReport
and surfaced at `/api/self`.
