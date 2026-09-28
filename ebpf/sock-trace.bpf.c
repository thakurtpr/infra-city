// SPDX-License-Identifier: Apache-2.0
// eBPF socket tracer: TCP established-flow accounting with PID attribution.
//
// Probe: tracepoint/sock/inet_sock_set_state, fires on TCP_ESTABLISHED.
// Key = (saddr, daddr, sport, dport, family, proto); value adds
// (cumulative conns, last-seen ns, pid, comm). Userspace consumes entries
// (delete-after-read) so conns counts connections per sample interval.
//
// CO-RE (compile once, run everywhere): kernel struct accesses use
// BPF_CORE_READ against target BTF, so no kernel headers or per-node
// compilation are needed at load time — but the node kernel MUST expose
// /sys/kernel/btf/vmlinux or loading fails and the agent falls back to
// /proc (see agent/internal/ebpf). Partial struct definitions below only
// need correct field NAMES; offsets come from BTF.
//
// Address byte order in the key: network order (raw __be32 bytes for v4,
// u6_addr8 bytes for v6). Go mirrors this layout exactly (flowconv.go).
//
// Build (pinned toolchain image, no host clang needed):
//   make ebpf   # -> agent/internal/ebpf/bpf/sock-trace.bpf.o
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>

#define AF_INET 2
#define AF_INET6 10
#define IPPROTO_TCP 6
#define TCP_ESTABLISHED 1

/* Partial kernel structs for CO-RE: names must match vmlinux BTF,
 * layout is resolved from BTF at load time. NOTE: struct in6_addr holds
 * the bytes in union in6_u.u6_addr8 — using any other spelling poisons
 * the relocation and the kernel rejects the whole program. */
struct in6_addr {
	union {
		__u8 u6_addr8[16];
	} in6_u;
} __attribute__((preserve_access_index));

struct sock_common {
	__u32 skc_daddr;
	__u32 skc_rcv_saddr;
	__u32 skc_hash;
	__u16 skc_dport;
	__u16 skc_num;
	__u16 skc_family;
	struct in6_addr skc_v6_daddr;
	struct in6_addr skc_v6_rcv_saddr;
} __attribute__((preserve_access_index));

struct sock {
	struct sock_common __sk_common;
} __attribute__((preserve_access_index));

/* Must match Go agent/internal/ebpf.FlowKey byte-for-byte (see _Static_assert). */
struct flow_key {
	__u8 saddr[16];
	__u8 daddr[16];
	__u16 sport;
	__u16 dport;
	__u16 family;
	__u8 proto;
	__u8 pad;
};

/* Must match Go agent/internal/ebpf.FlowVal byte-for-byte, including the
 * explicit trailing pad: cilium/ebpf decodes field-by-field and rejects
 * values with unconsumed trailing bytes. */
struct flow_val {
	__u64 conns;
	__u64 last_seen_ns;
	__u32 pid;
	char comm[16];
	__u8 pad[4];
};

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, struct flow_key);
	__type(value, struct flow_val);
} flows SEC(".maps");

/* Classic tracepoint ctx: 8-byte common header, then event fields in
 * TRACE_EVENT order (skaddr, oldstate, newstate, ...). Only skaddr and
 * newstate are read; everything else is derived from struct sock. */
struct inet_sock_set_state_args {
	__u64 pad;
	void *skaddr;
	int oldstate;
	int newstate;
};

SEC("tracepoint/sock/inet_sock_set_state")
int trace_tcp_state(struct inet_sock_set_state_args *ctx)
{
	struct sock *sk;
	struct flow_key key = {};
	struct flow_val *vp;
	struct flow_val nv = {};
	__u16 family, dport_net;
	__u32 v4;

	if (ctx->newstate != TCP_ESTABLISHED)
		return 0;
	sk = (struct sock *)ctx->skaddr;
	family = BPF_CORE_READ(sk, __sk_common.skc_family);
	if (family != AF_INET && family != AF_INET6)
		return 0;

	if (family == AF_INET) {
		/* __be32 memory layout IS network order on every arch. */
		v4 = BPF_CORE_READ(sk, __sk_common.skc_rcv_saddr);
		key.saddr[0] = v4 & 0xff;
		key.saddr[1] = (v4 >> 8) & 0xff;
		key.saddr[2] = (v4 >> 16) & 0xff;
		key.saddr[3] = (v4 >> 24) & 0xff;
		v4 = BPF_CORE_READ(sk, __sk_common.skc_daddr);
		key.daddr[0] = v4 & 0xff;
		key.daddr[1] = (v4 >> 8) & 0xff;
		key.daddr[2] = (v4 >> 16) & 0xff;
		key.daddr[3] = (v4 >> 24) & 0xff;
	} else {
		BPF_CORE_READ_INTO(&key.saddr, sk, __sk_common.skc_v6_rcv_saddr.in6_u.u6_addr8);
		BPF_CORE_READ_INTO(&key.daddr, sk, __sk_common.skc_v6_daddr.in6_u.u6_addr8);
	}

	key.sport = BPF_CORE_READ(sk, __sk_common.skc_num); /* host order */
	dport_net = BPF_CORE_READ(sk, __sk_common.skc_dport); /* network order */
	key.dport = (dport_net << 8) | (dport_net >> 8);
	key.family = family;
	key.proto = IPPROTO_TCP;

	vp = bpf_map_lookup_elem(&flows, &key);
	if (vp) {
		__sync_fetch_and_add(&vp->conns, 1);
		vp->last_seen_ns = bpf_ktime_get_ns();
		return 0;
	}
	nv.conns = 1;
	nv.last_seen_ns = bpf_ktime_get_ns();
	nv.pid = bpf_get_current_pid_tgid() >> 32;
	bpf_get_current_comm(&nv.comm, sizeof(nv.comm));
	bpf_map_update_elem(&flows, &key, &nv, BPF_NOEXIST);
	return 0;
}

_Static_assert(sizeof(struct flow_key) == 40, "flow_key must be 40 bytes");
_Static_assert(sizeof(struct flow_val) == 40, "flow_val must be 40 bytes");

char LICENSE[] SEC("license") = "Dual BSD/GPL";
