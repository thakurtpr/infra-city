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

#define AF_INET 2
#define AF_INET6 10
#define IPPROTO_TCP 6
#define IPPROTO_UDP 17
#define TCP_ESTABLISHED 1
#define ETH_P_IP 0x0800
#define ETH_P_IPV6 0x86DD

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
	nv.cgroup_id = bpf_get_current_cgroup_id();
	bpf_get_current_comm(&nv.comm, sizeof(nv.comm));
	bpf_map_update_elem(&flows, &key, &nv, BPF_NOEXIST);
	return 0;
}

static __always_inline __u16 swap16(__u16 x)
{
	return (x << 8) | (x >> 8);
}

/* Shared TCX body. ingress=0 attributes egress bytes as-is; ingress=1 swaps
 * endpoints back to tracepoint orientation (local first). Packet memory is
 * read with __builtin_memcpy (verifier-safe on unaligned headers); every read
 * is bounds-checked first. Always returns TCX_PASS: we observe, never police.
 */
static __always_inline int count_packet(struct __sk_buff *skb, int ingress)
{
	void *data = (void *)(long)skb->data;
	void *data_end = (void *)(long)skb->data_end;
	struct flow_key key = {};
	struct flow_val *vp;
	void *l3;
	__u8 vihl, ipproto, ihl;
	__u16 h_proto;
	__u64 len;

	if (data + 14 > data_end)
		return TCX_PASS;
	__builtin_memcpy(&h_proto, data + 12, 2);
	h_proto = swap16(h_proto);
	l3 = data + 14;

	if (h_proto == ETH_P_IP) {
		if (l3 + 20 > data_end)
			return TCX_PASS;
		__builtin_memcpy(&vihl, l3, 1);
		if ((vihl >> 4) != 4)
			return TCX_PASS;
		ihl = (vihl & 0x0f) * 4;
		if (ihl < 20)
			return TCX_PASS;
		__builtin_memcpy(&ipproto, l3 + 9, 1);
		if (ipproto != IPPROTO_TCP && ipproto != IPPROTO_UDP)
			return TCX_PASS;
		if (l3 + ihl + 4 > data_end)
			return TCX_PASS;
		__builtin_memcpy(key.saddr, l3 + 12, 4);
		__builtin_memcpy(key.daddr, l3 + 16, 4);
		__builtin_memcpy(&key.sport, l3 + ihl, 2);
		__builtin_memcpy(&key.dport, l3 + ihl + 2, 2);
		key.sport = swap16(key.sport);
		key.dport = swap16(key.dport);
		key.family = AF_INET;
		key.proto = ipproto;
	} else if (h_proto == ETH_P_IPV6) {
		if (l3 + 44 > data_end)
			return TCX_PASS;
		__builtin_memcpy(&ipproto, l3 + 6, 1);
		if (ipproto != IPPROTO_TCP && ipproto != IPPROTO_UDP)
			return TCX_PASS; /* no ext-header walk in v1 */
		__builtin_memcpy(key.saddr, l3 + 8, 16);
		__builtin_memcpy(key.daddr, l3 + 24, 16);
		__builtin_memcpy(&key.sport, l3 + 40, 2);
		__builtin_memcpy(&key.dport, l3 + 42, 2);
		key.sport = swap16(key.sport);
		key.dport = swap16(key.dport);
		key.family = AF_INET6;
		key.proto = ipproto;
	} else {
		return TCX_PASS;
	}

	if (ingress) {
		__u8 tmpaddr[16];
		__u16 tmport;
		__builtin_memcpy(tmpaddr, key.saddr, 16);
		__builtin_memcpy(key.saddr, key.daddr, 16);
		__builtin_memcpy(key.daddr, tmpaddr, 16);
		tmport = key.sport;
		key.sport = key.dport;
		key.dport = tmport;
	}

	vp = bpf_map_lookup_elem(&flows, &key);
	if (!vp)
		return TCX_PASS;
	len = (void *)data_end - (void *)data;
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
