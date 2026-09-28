// SPDX-License-Identifier: Apache-2.0
package ebpf

import (
	"fmt"
	"net"
	"os"

	"github.com/infracity/infracity/agent/internal/netmon"
)

// FlowKey mirrors C struct flow_key in ebpf/sock-trace.bpf.c byte-for-byte.
// Addresses are network-order bytes (v4 in the first 4 bytes). Field order
// and sizes must stay in sync — enforced by TestFlowLayoutMatchesC.
type FlowKey struct {
	Saddr  [16]byte
	Daddr  [16]byte
	Sport  uint16
	Dport  uint16
	Family uint16
	Proto  uint8
	Pad    uint8
}

// FlowVal mirrors C struct flow_val in ebpf/sock-trace.bpf.c byte-for-byte.
// Pad is explicit trailing padding: cilium/ebpf decodes field-by-field and
// rejects values with unconsumed trailing bytes.
type FlowVal struct {
	Conns    uint64
	LastSeen uint64
	BytesTx  uint64
	BytesRx  uint64
	Pid      uint32
	Comm     [16]byte
	Pad      [4]byte
}

// IP protocol / address family numbers reported by the probe.
const (
	protoTCP = 6
	familyV4 = 2
	familyV6 = 10
)

// linuxTracer is the degraded tracer both linux builds fall back to.
// Untagged on purpose: it contains no platform-specific code, only the
// shared /proc fallback behind the Tracer interface.
type linuxTracer struct {
	enabled bool
	reason  string
}

func (t *linuxTracer) Enabled() bool  { return t.enabled }
func (t *linuxTracer) Reason() string { return t.reason }
func (t *linuxTracer) Sample() ([]netmon.Flow, error) {
	return netmon.SampleProcNet()
}
func (t *linuxTracer) Close() error { return nil }

// disabledByEnv reports whether INFRACITY_EBPF=off forces the /proc fallback.
// Escape hatch for debugging and for kernels without BTF.
func disabledByEnv() bool { return os.Getenv("INFRACITY_EBPF") == "off" }

// flowToNetmon converts one consumed map entry to the shared Flow schema.
// ok=false for address families the probe never emits (defensive: the C
// program only inserts AF_INET/AF_INET6, anything else is corruption).
func flowToNetmon(k FlowKey, v FlowVal) (f netmon.Flow, ok bool) {
	var src, dst net.IP
	switch k.Family {
	case familyV4:
		src, dst = net.IP(k.Saddr[:4]), net.IP(k.Daddr[:4])
	case familyV6:
		src, dst = net.IP(k.Saddr[:]), net.IP(k.Daddr[:])
	default:
		return netmon.Flow{}, false
	}
	proto := fmt.Sprintf("IPPROTO-%d", k.Proto)
	if k.Proto == protoTCP {
		proto = "TCP"
	}
	return netmon.Flow{
		SrcIP: src.String(), DstIP: dst.String(),
		SrcPort: int(k.Sport), DstPort: int(k.Dport),
		Protocol: proto, Connections: int64(v.Conns),
		BytesTx: int64(v.BytesTx), BytesRx: int64(v.BytesRx),
		Process: formatProcess(v.Comm, v.Pid),
	}, true
}

// formatProcess renders the probe's comm+pid as "nginx(1234)".
func formatProcess(comm [16]byte, pid uint32) string {
	name := comm[:]
	for i, b := range name {
		if b == 0 {
			name = name[:i]
			break
		}
	}
	return fmt.Sprintf("%s(%d)", string(name), pid)
}
