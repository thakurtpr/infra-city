// SPDX-License-Identifier: Apache-2.0
// Shared packet -> flow-key parsing, compiled twice:
//   - into ebpf/sock-trace.bpf.c (-target bpf): __builtin_memcpy + explicit
//     bounds checks keep the verifier happy; loops are constant-bounded.
//   - into ebpf/tests/test_parse.c (host clang): the same logic runs in
//     userspace against crafted frames, so parse paths that never appear on
//     a kind veth (VLAN tags, IPv6 extension headers) are still tested.
//
// No BPF helpers, no map access here — pure function over [data, data_end).
// Key layout: addresses in network-order bytes (v4 in the first 4 bytes),
// ports in HOST order, matching the tracepoint identity side.
#ifndef INFRACITY_FLOW_PARSE_H
#define INFRACITY_FLOW_PARSE_H

typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;
typedef unsigned long long __u64;

#define F_AF_INET 2
#define F_AF_INET6 10
#define F_TCP 6
#define F_UDP 17
#define F_ETH_IP 0x0800
#define F_ETH_IPV6 0x86DD
#define F_ETH_VLAN 0x8100
#define F_ETH_QINQ 0x88A8
#define F_MAX_VLAN_TAGS 2
#define F_MAX_EXT_HEADERS 4
/* Cap the total IPv6 extension area: real chains are a few dozen bytes; a
 * small constant bound keeps verifier precision states tiny (a variable
 * offset otherwise explodes mark_precise backtracking and the load fails). */
#define F_V6_EXT_MAX 104

/* IPv6 extension headers we walk (anything else, incl. ESP, stops parsing). */
#define F_NH_HOPBYHOP 0
#define F_NH_ROUTING 43
#define F_NH_FRAGMENT 44
#define F_NH_AH 51
#define F_NH_DESTOPTS 60
#define F_NH_MOBILITY 135

struct flow_key {
	__u8 saddr[16];
	__u8 daddr[16];
	__u16 sport;
	__u16 dport;
	__u16 family;
	__u8 proto;
	__u8 pad;
};

static __always_inline __u16 f_swap16(__u16 x)
{
	return (__u16)((x << 8) | (x >> 8));
}

/* parse_flow_key fills key from one Ethernet frame; ingress swaps endpoints
 * to tracepoint orientation (local first). Returns 0 on parsed, 1 to skip
 * (non-IP, truncated, encrypted, fragmented tail, ...). len_out always
 * carries the whole-frame length for byte accounting. */
static __always_inline int parse_flow_key(void *data, void *data_end,
					  struct flow_key *key, int ingress,
					  __u64 *len_out)
{
	void *l3;
	__u16 h_proto;
	__u8 vihl, ipproto, ihl, nh;
	__u64 off;
	int i;

	*len_out = (void *)data_end - (void *)data;
	if (data + 14 > data_end)
		return 1;
	__builtin_memcpy(&h_proto, data + 12, 2);
	h_proto = f_swap16(h_proto);
	l3 = data + 14;

	/* VLAN tags, hand-unrolled (two levels max): straight-line branches
	 * keep every packet-pointer bound check dominating its reads. A bounded
	 * loop here merges pointer states across iterations and older verifiers
	 * reject the dominated reads downstream. */
	if (h_proto == F_ETH_VLAN || h_proto == F_ETH_QINQ) {
		if (l3 + 4 > data_end)
			return 1;
		__builtin_memcpy(&h_proto, l3 + 2, 2);
		h_proto = f_swap16(h_proto);
		l3 += 4;
		if (h_proto == F_ETH_VLAN || h_proto == F_ETH_QINQ) {
			if (l3 + 4 > data_end)
				return 1;
			__builtin_memcpy(&h_proto, l3 + 2, 2);
			h_proto = f_swap16(h_proto);
			l3 += 4;
		}
	}

	if (h_proto == F_ETH_IP) {
		if (l3 + 20 > data_end)
			return 1;
		__builtin_memcpy(&vihl, l3, 1);
		if ((vihl >> 4) != 4)
			return 1;
		ihl = (vihl & 0x0f) * 4;
		if (ihl < 20)
			return 1;
		__builtin_memcpy(&ipproto, l3 + 9, 1);
		if (ipproto != F_TCP && ipproto != F_UDP)
			return 1;
		if (l3 + ihl + 4 > data_end)
			return 1;
		__builtin_memcpy(key->saddr, l3 + 12, 4);
		__builtin_memcpy(key->daddr, l3 + 16, 4);
		__builtin_memcpy(&key->sport, l3 + ihl, 2);
		__builtin_memcpy(&key->dport, l3 + ihl + 2, 2);
		key->sport = f_swap16(key->sport);
		key->dport = f_swap16(key->dport);
		key->family = F_AF_INET;
		key->proto = ipproto;
	} else if (h_proto == F_ETH_IPV6) {
		if (l3 + 40 > data_end)
			return 1;
		__builtin_memcpy(&nh, l3 + 6, 1);
		off = 40;
		/* Extension-header walk: bounded, unrolled, every step re-checked. */
#pragma unroll
		for (i = 0; i < F_MAX_EXT_HEADERS; i++) {
			__u8 elen, frag[2], nxthdr;
			if (nh == F_TCP || nh == F_UDP)
				break;
			if (nh == F_NH_FRAGMENT) {
				/* fixed 8 bytes; ports exist only in the first fragment */
				if (l3 + off + 8 > data_end)
					return 1;
				__builtin_memcpy(&nxthdr, l3 + off, 1);
				__builtin_memcpy(frag, l3 + off + 2, 2);
				/* 13-bit offset spread over both bytes (Res/M bits masked):
				 * nonzero means a tail fragment without ports. */
				if ((frag[0] & 0x3f) != 0 || (frag[1] & 0xfe) != 0)
					return 1;
				nh = nxthdr;
				off += 8;
			} else if (nh == F_NH_HOPBYHOP || nh == F_NH_ROUTING ||
				   nh == F_NH_DESTOPTS || nh == F_NH_MOBILITY) {
				/* length unit is 8 bytes, not counting the first 8 */
				if (l3 + off + 2 > data_end)
					return 1;
				__builtin_memcpy(&nxthdr, l3 + off, 1);
				__builtin_memcpy(&elen, l3 + off + 1, 1);
				nh = nxthdr;
				off += ((__u64)elen + 1) * 8;
			} else if (nh == F_NH_AH) {
				/* AH length unit is 4 bytes, not counting the first 8... */
				if (l3 + off + 2 > data_end)
					return 1;
				__builtin_memcpy(&nxthdr, l3 + off, 1);
				__builtin_memcpy(&elen, l3 + off + 1, 1);
				nh = nxthdr;
				off += ((__u64)elen + 2) * 4;
			} else {
				return 1; /* ESP or unknown: ports unrecoverable */
			}
			if (off > F_V6_EXT_MAX)
				return 1;
		}
		if (nh != F_TCP && nh != F_UDP)
			return 1;
		if (l3 + off + 4 > data_end)
			return 1;
		__builtin_memcpy(key->saddr, l3 + 8, 16);
		__builtin_memcpy(key->daddr, l3 + 24, 16);
		__builtin_memcpy(&key->sport, l3 + off, 2);
		__builtin_memcpy(&key->dport, l3 + off + 2, 2);
		key->sport = f_swap16(key->sport);
		key->dport = f_swap16(key->dport);
		key->family = F_AF_INET6;
		key->proto = nh;
	} else {
		return 1;
	}

	if (ingress) {
		__u8 tmpaddr[16];
		__u16 tmport;
		__builtin_memcpy(tmpaddr, key->saddr, 16);
		__builtin_memcpy(key->saddr, key->daddr, 16);
		__builtin_memcpy(key->daddr, tmpaddr, 16);
		tmport = key->sport;
		key->sport = key->dport;
		key->dport = tmport;
	}
	return 0;
}

#endif /* INFRACITY_FLOW_PARSE_H */
