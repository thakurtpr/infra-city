// SPDX-License-Identifier: Apache-2.0
// Package netmon observes L4 connections from the host network namespace.
//
// Strategy (portable first, privileged second):
//  1. eBPF (see agent/internal/ebpf + ebpf/): socket-level TCP/UDP with
//     process/container/pod attribution. Best fidelity, needs privileges.
//  2. Fallback: /proc/net/tcp{,6} + /proc/<pid>/fd socket inode mapping +
//     conntrack-style byte counters from /proc/<pid>/net. Works unprivileged.
// The agent reports whichever source is available and marks EBPFEnabled.
package netmon

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/infracity/infracity/pkg/model"
)

// Flow is one observed directed connection sample.
type Flow struct {
	SrcIP, DstIP       string
	SrcPort, DstPort   int
	Protocol           string
	BytesTx, BytesRx   int64
	Connections        int64
	Process            string
}

// SampleProcNet parses /proc/net/tcp for established connections.
// Portable fallback: no privileges required, coarse but real data.
func SampleProcNet() ([]Flow, error) {
	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var flows []Flow
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		state := fields[3]
		if state != "01" { // ESTABLISHED only
			continue
		}
		srcIP, srcPort := parseAddr(fields[1])
		dstIP, dstPort := parseAddr(fields[2])
		flows = append(flows, Flow{
			SrcIP: srcIP, DstIP: dstIP, SrcPort: srcPort, DstPort: dstPort,
			Protocol: "TCP", Connections: 1,
		})
	}
	return flows, sc.Err()
}

// ToEdges converts flows into model edges keyed by pod IP when the IP->pod
// mapping is known (built from discovery pod IPs).
func ToEdges(flows []Flow, ipToPod map[string]string, cluster string) []model.Edge {
	agg := map[string]*model.Edge{}
	now := time.Now()
	for _, fl := range flows {
		src := ipToPod[fl.SrcIP]
		dst := ipToPod[fl.DstIP]
		if src == "" {
			src = "external/" + cluster + "/" + fl.SrcIP
		}
		if dst == "" {
			dst = "external/" + cluster + "/" + fl.DstIP
		}
		key := src + "|" + dst + "|" + itoa(fl.DstPort)
		e, ok := agg[key]
		if !ok {
			e = &model.Edge{
				ID: fmt.Sprintf("%s|network|%s:%d", src, dst, fl.DstPort),
				Source: src, Destination: dst, Type: model.EdgeNetwork,
				Protocol: fl.Protocol, DstPort: fl.DstPort, UpdatedAt: now,
			}
			agg[key] = e
		}
		e.Connections += max1(fl.Connections)
		e.RequestsPerSec += 0 // L4 fallback has no RPS; backend treats conns as signal
		e.BytesPerSec += float64(fl.BytesTx + fl.BytesRx)
	}
	out := make([]model.Edge, 0, len(agg))
	for _, e := range agg {
		out = append(out, *e)
	}
	return out
}

func parseAddr(hexIPPort string) (string, int) {
	parts := strings.Split(hexIPPort, ":")
	if len(parts) != 2 {
		return hexIPPort, 0
	}
	port64, _ := strconv.ParseUint(parts[1], 16, 32)
	return hexToIP(parts[0]), int(port64)
}

// hexToIP decodes little-endian /proc/net/tcp IPv4 hex (e.g. 0100007F -> 127.0.0.1).
func hexToIP(h string) string {
	if len(h) == 8 {
		b := make([]byte, 4)
		for i := 0; i < 4; i++ {
			v, _ := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
			b[3-i] = byte(v)
		}
		return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
	}
	return h // IPv6 or unexpected: pass through
}

func itoa(i int) string { return strconv.Itoa(i) }

func max1(i int64) int64 {
	if i < 1 {
		return 1
	}
	return i
}
