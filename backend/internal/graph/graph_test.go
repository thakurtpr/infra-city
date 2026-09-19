// SPDX-License-Identifier: Apache-2.0
package graph

import (
	"testing"

	"github.com/infracity/infracity/pkg/model"
)

func TestUpsertAndTraverse(t *testing.T) {
	g := New()
	g.UpsertNode(model.Node{ID: "service/c1/default/frontend", Type: "service"})
	g.UpsertNode(model.Node{ID: "service/c1/default/api", Type: "service"})
	g.UpsertNode(model.Node{ID: "service/c1/default/payments", Type: "service"})
	g.UpsertEdge(model.Edge{Source: "service/c1/default/frontend", Destination: "service/c1/default/api", Type: model.EdgeNetwork})
	g.UpsertEdge(model.Edge{Source: "service/c1/default/api", Destination: "service/c1/default/payments", Type: model.EdgeNetwork})

	down := g.Traverse("service/c1/default/frontend", true, 5, nil)
	if len(down) != 2 {
		t.Fatalf("expected 2 downstream, got %v", down)
	}
	up := g.Traverse("service/c1/default/payments", false, 5, nil)
	if len(up) != 2 {
		t.Fatalf("expected 2 upstream, got %v", up)
	}
	p := g.ShortestPath("service/c1/default/frontend", "service/c1/default/payments", nil)
	if len(p) != 3 {
		t.Fatalf("expected path len 3, got %v", p)
	}
	d, u := g.BlastRadius("service/c1/default/api", 5)
	if len(d) != 1 || len(u) != 1 {
		t.Fatalf("blast radius wrong: down=%v up=%v", d, u)
	}
}

func TestUpsertEdgeMergeKeepsAdjacencyFresh(t *testing.T) {
	g := New()
	g.UpsertEdge(model.Edge{Source: "a", Destination: "b", Type: model.EdgeNetwork, RequestsPerSec: 10})
	g.UpsertEdge(model.Edge{Source: "a", Destination: "b", Type: model.EdgeNetwork, RequestsPerSec: 99, BytesPerSec: 7})
	out, _ := g.Neighbors("a")
	if len(out) != 1 {
		t.Fatalf("neighbors = %d, want 1", len(out))
	}
	if out[0].RequestsPerSec != 99 || out[0].BytesPerSec != 7 {
		t.Fatalf("stale adjacency: %+v", out[0])
	}
}

func TestEdgesClusterFilter(t *testing.T) {
	g := New()
	g.UpsertEdge(model.Edge{Source: "service/c1/default/a", Destination: "service/c1/default/b", Type: model.EdgeNetwork})
	g.UpsertEdge(model.Edge{Source: "service/c2/default/a", Destination: "service/c2/default/b", Type: model.EdgeNetwork})
	if got := len(g.Edges("c1", "")); got != 1 {
		t.Fatalf("cluster c1 edges = %d, want 1", got)
	}
	if got := len(g.Edges("", "")); got != 2 {
		t.Fatalf("unfiltered edges = %d, want 2", got)
	}
}
