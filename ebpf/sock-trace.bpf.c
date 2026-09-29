// SPDX-License-Identifier: Apache-2.0
// eBPF socket tracer: TCP/UDP established-flow accounting with PID attribution
// and per-flow byte counters.
//
// Tracepoint (sock/inet_sock_set_state on TCP_ESTABLISHED) owns flow identity:
// key = (saddr, daddr, sport, dport, family, proto) with the LOCAL endpoint
// first; value adds (cumulative conns, last-seen ns, bytes tx/rx, pid, comm).
// TCX ingress/egress programs only ADD BYTES to keys the tracepoint created —
// a lookup miss (pre-existing connection with no PID to attribute) is ignored.
// Userspace consumes entries delete-after-read, so counters are per-interval.
//
// CO-RE (compile once, run everywhere): kernel struct accesses use
// BPF_CORE_READ against target BTF, so the object needs no per-node
// compilation — but the node kernel MUST expose /sys/kernel/btf/vmlinux or
// loading fails and the agent falls back to /proc (see agent/internal/ebpf).
// Partial struct names must match vmlinux BTF exactly (in6_u.u6_addr8!).
//
// Address byte order in the key: network order (raw __be32 bytes for v4,
// u6_addr8 bytes for v6); ports are HOST order. Go mirrors the layout exactly
// (agent/internal/ebpf/flowconv.go).
//
// Parsing limits (v1): Ethernet + IPv4/IPv6 + TCP/UDP only. IPv6 extension
// headers and 802.1Q tags are skipped (TCX_PASS) — kind veths are untagged.
// Byte counts are whole-frame wire bytes (L2 inclusive).
//
// Build (pinned toolchain image, no host clang needed):
//   make ebpf   # -> agent/internal/ebpf/bpf/sock-trace.bpf.o
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>

#include "flow_parse.h"

#define TCP_ESTABLISHED 1

#ifndef TCX_PASS
#define TCX_PASS 0
#endif

/* Partial kernel structs for CO-RE: names must match vmlinux BTF,
 * layout is resolved from BTF at load time. */
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

/* flow_key lives in flow_parse.h (shared with the userspace parse tests). */

/* Must match Go agent/internal/ebpf.FlowVal byte-for-byte, including the
 * explicit trailing pad: cilium/ebpf decodes field-by-field and rejects
 * values with unconsumed trailing bytes. cgroup_id is the kernfs inode of
 * the owning cgroup — userspace maps it to container->pod by walking the
 * cgroupfs (same inode numbers host-wide), no PID lifetime race. First
 * connection wins (like pid/comm): later bumps keep the original owner. */
struct flow_val {
	__u64 conns;
	__u64 last_seen_ns;
	__u64 bytes_tx;
	__u64 bytes_rx;
	__u64 cgroup_id;
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
	if (family != F_AF_INET && family != F_AF_INET6)
		return 0;

	if (family == F_AF_INET) {
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
	key.proto = F_TCP;

	vp = bpf_map_lookup_elem(&flows, &key);
	if (vp) {
		__sync_fetch_and_add(&vp->conns, 1);
		vp->last_seen_ns = bpf_ktime_get_ns();
		return 0;
	}
	nv.conns = 1;
	nv.last_seen_ns = bpf_ktime_get_ns();
	nv.pid = bpf_get_current_pid_tgid() >> 32;
	nv.cgroup_id = bpf_get_current_cgroup_id();
	bpf_get_current_comm(&nv.comm, sizeof(nv.comm));
	bpf_map_update_elem(&flows, &key, &nv, BPF_NOEXIST);
	return 0;
}

/* TCX glue: parse (shared, unit-tested in ebpf/tests), then add whole-frame
 * wire bytes to tracepoint-owned keys. Always TCX_PASS: observe, never police.
 */
static __always_inline int count_packet(struct __sk_buff *skb, int ingress)
{
	void *data = (void *)(long)skb->data;
	void *data_end = (void *)(long)skb->data_end;
	struct flow_key key = {};
	struct flow_val *vp;
	struct flow_val nv;
	__u64 len;

	if (parse_flow_key(data, data_end, &key, ingress, &len))
		return TCX_PASS;
	vp = bpf_map_lookup_elem(&flows, &key);
	if (!vp) {
		/* UDP has no established-state signal, so the tracepoint can never
		 * create its keys: first packet wins. pid/comm/cgroup stay zero —
		 * TCX runs in softirq context with no owning process — and the
		 * agent falls back to IP attribution for these flows. TCP misses
		 * stay ignored: untracked TCP means pre-existing connection with
		 * no PID to attribute. */
		if (key.proto != F_UDP)
			return TCX_PASS;
		__builtin_memset(&nv, 0, sizeof(nv));
		nv.conns = 1;
		nv.last_seen_ns = bpf_ktime_get_ns();
		bpf_map_update_elem(&flows, &key, &nv, BPF_NOEXIST);
		vp = bpf_map_lookup_elem(&flows, &key);
		if (!vp)
			return TCX_PASS;
	}
	if (ingress)
		__sync_fetch_and_add(&vp->bytes_rx, len);
	else
		__sync_fetch_and_add(&vp->bytes_tx, len);
	return TCX_PASS;
}

SEC("tcx/egress")
int count_egress(struct __sk_buff *skb)
{
	return count_packet(skb, 0);
}

SEC("tcx/ingress")
int count_ingress(struct __sk_buff *skb)
{
	return count_packet(skb, 1);
}

_Static_assert(sizeof(struct flow_key) == 40, "flow_key must be 40 bytes");
_Static_assert(sizeof(struct flow_val) == 64, "flow_val must be 64 bytes");

char LICENSE[] SEC("license") = "Dual BSD/GPL";
