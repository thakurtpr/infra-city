// SPDX-License-Identifier: Apache-2.0
// eBPF socket tracer: TCP connect/flow accounting with cgroup->pod attribution.
// Build: clang -O2 -target bpf -c sock-trace.bpf.c -o sock-trace.bpf.o
// Load via cilium/ebpf (see ebpf/README.md). This file documents the intended
// probes; the portable Go build degrades to /proc sampling with identical edge schema.
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>

struct flow_key {
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
    __u8  proto;
};

struct flow_val {
    __u64 bytes_tx;
    __u64 bytes_rx;
    __u64 conns;
    __u32 pid;
    __u64 cgroup_id;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 65536);
    __type(key, struct flow_key);
    __type(value, struct flow_val);
} flows SEC(".maps");

// Trace TCP state changes; record established flows with owning cgroup for
// userspace PID->container->pod resolution via /proc + kubelet pod cache.
SEC("tracepoint/sock/inet_sock_set_state")
int trace_tcp_state(void *ctx) { return 0; }

char LICENSE[] SEC("license") = "Dual BSD/GPL";
