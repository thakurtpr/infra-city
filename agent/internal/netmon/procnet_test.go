// SPDX-License-Identifier: Apache-2.0
package netmon

import "testing"

func TestHexToIPLoopback(t *testing.T) {
	if got := hexToIP("0100007F"); got != "127.0.0.1" {
		t.Fatalf("hexToIP = %q, want 127.0.0.1", got)
	}
}

func TestToEdgesAggregatesByPodAndPort(t *testing.T) {
	ipToPod := map[string]string{"10.0.0.1": "pod/c/dev/a", "10.0.0.2": "pod/c/dev/b"}
	flows := []Flow{
		{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", DstPort: 8080, Protocol: "TCP", Connections: 1},
		{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", DstPort: 8080, Protocol: "TCP", Connections: 2},
		{SrcIP: "1.2.3.4", DstIP: "10.0.0.2", DstPort: 443, Protocol: "TCP", Connections: 1},
	}
	edges := ToEdges(flows, ipToPod, "c")
	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2 (pod pair + external)", len(edges))
	}
	for _, e := range edges {
		if e.Source == "pod/c/dev/a" && e.Connections != 3 {
			t.Fatalf("aggregated conns = %d, want 3", e.Connections)
		}
	}
}
