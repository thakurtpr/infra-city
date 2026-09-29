// SPDX-License-Identifier: Apache-2.0
package ebpf

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

func TestFlowLayoutMatchesC(t *testing.T) {
	// Must equal C sizeof(struct flow_key/flow_val) = 40 (see _Static_assert
	// in ebpf/sock-trace.bpf.c); cilium/ebpf unmarshals maps into these structs.
	if got := unsafe.Sizeof(FlowKey{}); got != 40 {
		t.Fatalf("sizeof FlowKey = %d, want 40", got)
	}
	if got := unsafe.Sizeof(FlowVal{}); got != 64 {
		t.Fatalf("sizeof FlowVal = %d, want 64", got)
	}
	var k FlowKey
	if off := unsafe.Offsetof(k.Dport); off != 34 {
		t.Fatalf("Dport offset = %d, want 34", off)
	}
	var v FlowVal
	if off := unsafe.Offsetof(v.Comm); off != 44 {
		t.Fatalf("Comm offset = %d, want 44", off)
	}
	if off := unsafe.Offsetof(v.BytesTx); off != 16 {
		t.Fatalf("BytesTx offset = %d, want 16", off)
	}
	// cilium/ebpf decodes field-by-field: encoded field bytes must equal
	// the struct size (no implicit trailing padding allowed).
	if got := binary.Size(FlowKey{}); got != 40 {
		t.Fatalf("encoded FlowKey = %d bytes, want 40", got)
	}
	if got := binary.Size(FlowVal{}); got != 64 {
		t.Fatalf("encoded FlowVal = %d bytes, want 64", got)
	}
}

func TestFlowToNetmonV4(t *testing.T) {
	k := FlowKey{Family: 2, Proto: 6, Sport: 1234, Dport: 5432}
	k.Saddr[0], k.Saddr[1], k.Saddr[2], k.Saddr[3] = 10, 0, 0, 1
	k.Daddr[0], k.Daddr[1], k.Daddr[2], k.Daddr[3] = 10, 0, 0, 2
	v := FlowVal{Conns: 3, Pid: 42, BytesTx: 1500, BytesRx: 800, CgroupID: 12345}
	copy(v.Comm[:], "postgres")
	f, ok := flowToNetmon(k, v)
	if !ok {
		t.Fatal("v4 flow rejected")
	}
	if f.SrcIP != "10.0.0.1" || f.DstIP != "10.0.0.2" {
		t.Fatalf("ips = %s -> %s, want 10.0.0.1 -> 10.0.0.2", f.SrcIP, f.DstIP)
	}
	if f.SrcPort != 1234 || f.DstPort != 5432 || f.Protocol != "TCP" {
		t.Fatalf("ports/proto = %d/%d/%s", f.SrcPort, f.DstPort, f.Protocol)
	}
	if f.Connections != 3 {
		t.Fatalf("connections = %d, want 3", f.Connections)
	}
	if f.Process != "postgres(42)" {
		t.Fatalf("process = %q, want postgres(42)", f.Process)
	}
	if f.BytesTx != 1500 || f.BytesRx != 800 {
		t.Fatalf("bytes tx/rx = %d/%d, want 1500/800", f.BytesTx, f.BytesRx)
	}
	if f.CgroupID != 12345 {
		t.Fatalf("cgroup = %d, want 12345", f.CgroupID)
	}
}

func TestFlowToNetmonV6(t *testing.T) {
	k := FlowKey{Family: 10, Proto: 6, Sport: 80, Dport: 443}
	k.Saddr[15], k.Daddr[15] = 1, 2 // ::1 -> ::2
	v := FlowVal{Conns: 1, Pid: 7}
	copy(v.Comm[:], "curl")
	f, ok := flowToNetmon(k, v)
	if !ok {
		t.Fatal("v6 flow rejected")
	}
	if f.SrcIP != "::1" || f.DstIP != "::2" {
		t.Fatalf("ips = %s -> %s, want ::1 -> ::2", f.SrcIP, f.DstIP)
	}
}

func TestFlowToNetmonRejectsUnknownFamily(t *testing.T) {
	if _, ok := flowToNetmon(FlowKey{Family: 99}, FlowVal{}); ok {
		t.Fatal("unknown family must be rejected")
	}
}

func TestFlowToNetmonUDP(t *testing.T) {
	k := FlowKey{Family: 2, Proto: 17, Sport: 45678, Dport: 53}
	k.Saddr[0], k.Saddr[1], k.Saddr[2], k.Saddr[3] = 10, 244, 0, 44
	k.Daddr[0], k.Daddr[1], k.Daddr[2], k.Daddr[3] = 10, 96, 0, 10
	// TCX-created UDP keys carry no owner: pid/comm/cgroup stay zero.
	v := FlowVal{Conns: 1, BytesTx: 70, BytesRx: 120}
	f, ok := flowToNetmon(k, v)
	if !ok {
		t.Fatal("udp flow rejected")
	}
	if f.Protocol != "UDP" {
		t.Fatalf("proto = %q, want UDP", f.Protocol)
	}
	if f.Process != "(0)" {
		t.Fatalf("process = %q, want (0) for ownerless flow", f.Process)
	}
	if f.CgroupID != 0 {
		t.Fatalf("cgroup = %d, want 0", f.CgroupID)
	}
}

func TestFormatProcessTruncatesAtNul(t *testing.T) {
	var comm [16]byte
	copy(comm[:], "nginx\x00garbage")
	if got := formatProcess(comm, 1); got != "nginx(1)" {
		t.Fatalf("process = %q, want nginx(1)", got)
	}
}

func TestDisabledByEnv(t *testing.T) {
	t.Setenv("INFRACITY_EBPF", "off")
	if !disabledByEnv() {
		t.Fatal("INFRACITY_EBPF=off must force fallback")
	}
	t.Setenv("INFRACITY_EBPF", "")
	if disabledByEnv() {
		t.Fatal("empty INFRACITY_EBPF must not force fallback")
	}
}
