// SPDX-License-Identifier: Apache-2.0
// Userspace tests for ebpf/flow_parse.h (same code compiled into the BPF
// object). Crafted frames cover paths a kind veth never produces (VLAN tags,
// IPv6 extension headers) so they are proven here, not hoped for in prod.
#include <stdio.h>
#include <string.h>

#include "../flow_parse.h"

static int fails = 0;

#define CHECK(cond, msg) do { \
	if (!(cond)) { printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, msg); fails++; } \
} while (0)

static void eth(__u8 *f, __u16 type)
{
	memset(f, 0, 14);
	f[12] = (type >> 8) & 0xff;
	f[13] = type & 0xff;
}

static void ipv4(__u8 *h, __u8 proto, const __u8 s[4], const __u8 d[4], __u8 ihl_words)
{
	memset(h, 0, ihl_words * 4);
	h[0] = 0x40 | (ihl_words & 0x0f);
	h[9] = proto;
	memcpy(h + 12, s, 4);
	memcpy(h + 16, d, 4);
}

static void ports(__u8 *h, __u16 sport, __u16 dport)
{
	h[0] = (sport >> 8) & 0xff;
	h[1] = sport & 0xff;
	h[2] = (dport >> 8) & 0xff;
	h[3] = dport & 0xff;
}

static void ipv6(__u8 *h, __u8 nexthdr, const __u8 s[16], const __u8 d[16])
{
	memset(h, 0, 40);
	h[0] = 0x60;
	h[6] = nexthdr;
	memcpy(h + 8, s, 16);
	memcpy(h + 24, d, 16);
}

static void key4(struct flow_key *k, const __u8 s[4], const __u8 d[4],
		 __u16 sport, __u16 dport, __u8 proto)
{
	memset(k, 0, sizeof(*k));
	memcpy(k->saddr, s, 4);
	memcpy(k->daddr, d, 4);
	k->sport = sport;
	k->dport = dport;
	k->family = F_AF_INET;
	k->proto = proto;
}

static int keyeq(const struct flow_key *a, const struct flow_key *b)
{
	return memcmp(a, b, sizeof(*a)) == 0;
}

int main(void)
{
	__u8 f[256];
	struct flow_key k, want;
	__u64 len;
	const __u8 s4[4] = {10, 0, 0, 1}, d4[4] = {10, 0, 0, 2};
	__u8 s6[16] = {0}, d6[16] = {0};
	s6[15] = 1;
	d6[15] = 2;

	/* 1. plain v4 TCP, egress */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IP);
	ipv4(f + 14, F_TCP, s4, d4, 5);
	ports(f + 14 + 20, 1234, 80);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 20 + 20, &k, 0, &len) == 0, "v4 parse");
	key4(&want, s4, d4, 1234, 80, F_TCP);
	CHECK(keyeq(&k, &want), "v4 key");
	CHECK(len == 54, "v4 len");

	/* 2. same frame, ingress swaps to local-first */
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 54, &k, 1, &len) == 0, "v4 ingress parse");
	key4(&want, d4, s4, 80, 1234, F_TCP);
	CHECK(keyeq(&k, &want), "v4 ingress swap");

	/* 3. single VLAN tag, v4 UDP */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_VLAN);
	f[14] = 0;
	f[15] = 1; /* PCP/DEI/VID */
	f[16] = (F_ETH_IP >> 8) & 0xff;
	f[17] = F_ETH_IP & 0xff;
	ipv4(f + 18, F_UDP, s4, d4, 5);
	ports(f + 18 + 20, 53, 5353);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 18 + 20 + 8, &k, 0, &len) == 0, "vlan parse");
	key4(&want, s4, d4, 53, 5353, F_UDP);
	CHECK(keyeq(&k, &want), "vlan key");

	/* 4. QinQ outer + 802.1Q inner */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_QINQ);
	f[16] = (F_ETH_VLAN >> 8) & 0xff;
	f[17] = F_ETH_VLAN & 0xff;
	f[20] = (F_ETH_IP >> 8) & 0xff;
	f[21] = F_ETH_IP & 0xff;
	ipv4(f + 22, F_TCP, s4, d4, 5);
	ports(f + 22 + 20, 443, 8443);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 22 + 20 + 20, &k, 0, &len) == 0, "qinq parse");
	key4(&want, s4, d4, 443, 8443, F_TCP);
	CHECK(keyeq(&k, &want), "qinq key");

	/* 5. v4 with IP options (IHL=6), ports shift by 4 */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IP);
	ipv4(f + 14, F_TCP, s4, d4, 6);
	ports(f + 14 + 24, 2222, 22);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 24 + 20, &k, 0, &len) == 0, "ihl6 parse");
	key4(&want, s4, d4, 2222, 22, F_TCP);
	CHECK(keyeq(&k, &want), "ihl6 key");

	/* 6. plain v6 TCP */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IPV6);
	ipv6(f + 14, F_TCP, s6, d6);
	ports(f + 14 + 40, 80, 443);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 40 + 20, &k, 0, &len) == 0, "v6 parse");
	CHECK(k.family == F_AF_INET6 && k.sport == 80 && k.dport == 443 &&
	      k.proto == F_TCP && memcmp(k.saddr, s6, 16) == 0 &&
	      memcmp(k.daddr, d6, 16) == 0, "v6 key");

	/* 7. v6 hop-by-hop (8B) then TCP */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IPV6);
	ipv6(f + 14, F_NH_HOPBYHOP, s6, d6);
	f[14 + 40] = F_TCP; /* next */
	f[14 + 40 + 1] = 0; /* len 0 -> 8 bytes */
	ports(f + 14 + 40 + 8, 8080, 9090);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 40 + 8 + 20, &k, 0, &len) == 0, "hbh parse");
	CHECK(k.sport == 8080 && k.dport == 9090 && k.family == F_AF_INET6, "hbh key");

	/* 8. v6 routing header (len 1 -> 16B) then UDP */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IPV6);
	ipv6(f + 14, F_NH_ROUTING, s6, d6);
	f[14 + 40] = F_UDP;
	f[14 + 40 + 1] = 1;
	ports(f + 14 + 40 + 16, 123, 123);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 40 + 16 + 8, &k, 0, &len) == 0, "routing parse");
	CHECK(k.sport == 123 && k.proto == F_UDP, "routing key");

	/* 9. v6 fragment, offset 0 (M set) + TCP parses */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IPV6);
	ipv6(f + 14, F_NH_FRAGMENT, s6, d6);
	f[14 + 40] = F_TCP;
	f[14 + 40 + 1] = 0;
	f[14 + 40 + 2] = 0x00;
	f[14 + 40 + 3] = 0x01; /* M flag, offset still 0 */
	ports(f + 14 + 40 + 8, 3306, 45678);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 40 + 8 + 20, &k, 0, &len) == 0, "frag0 parse");
	CHECK(k.dport == 45678, "frag0 key");

	/* 10. v6 fragment, offset 8 -> skip (no ports in tail) */
	f[14 + 40 + 3] = 0x08;
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 40 + 8 + 8, &k, 0, &len) == 1, "frag-tail skips");

	/* 11. v6 ESP -> skip */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IPV6);
	ipv6(f + 14, 50, s6, d6);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 40 + 16, &k, 0, &len) == 1, "esp skips");

	/* 12. ARP -> skip */
	memset(f, 0, sizeof(f));
	eth(f, 0x0806);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 60, &k, 0, &len) == 1, "arp skips");

	/* 13. truncated frame -> skip */
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 10, &k, 0, &len) == 1, "truncated skips");

	/* 14. truncated mid-ports -> skip */
	memset(f, 0, sizeof(f));
	eth(f, F_ETH_IP);
	ipv4(f + 14, F_TCP, s4, d4, 5);
	memset(&k, 0, sizeof(k));
	CHECK(parse_flow_key(f, f + 14 + 20 + 2, &k, 0, &len) == 1, "short ports skip");

	if (fails == 0)
		printf("parse tests: all pass\n");
	return fails != 0;
}
