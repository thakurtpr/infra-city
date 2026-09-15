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
