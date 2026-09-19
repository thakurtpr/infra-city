// SPDX-License-Identifier: Apache-2.0
package analysis

import (
	"testing"
	"time"

	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/pkg/model"
)

func seed(t *testing.T) *graph.Graph {
	t.Helper()
	g := graph.New()
	now := time.Now()
	g.UpsertNode(model.Node{ID: "service/c/d/api", Type: model.TypeService, Cluster: "c", Namespace: "d", Name: "api",
		UpdatedAt: now, Metrics: &model.Metrics{ReqPerSec: 500, ErrRate: 0.3, LatencyMsP95: 120}})
	g.UpsertNode(model.Node{ID: "service/c/d/db", Type: model.TypeDatabase, Cluster: "c", Namespace: "d", Name: "db",
		UpdatedAt: now, Metrics: &model.Metrics{ReqPerSec: 400, LatencyMsP95: 2500}})
	g.UpsertEdge(model.Edge{Source: "service/c/d/api", Destination: "service/c/d/db",
		Type: model.EdgeNetwork, RequestsPerSec: 400, LatencyMs: 90, UpdatedAt: now})
	return g
}

func TestInferDependenciesOrdersByConfidence(t *testing.T) {
	g := seed(t)
	deps := InferDependencies(g)
	if len(deps) != 1 || deps[0].Confidence <= 0.5 {
		t.Fatalf("deps = %+v, want 1 high-confidence", deps)
	}
}

func TestDetectIncidentsFlagsErrorAndLatency(t *testing.T) {
	g := seed(t)
	inc := DetectIncidents(g)
	if len(inc) != 2 {
		t.Fatalf("incidents = %d, want 2 (api errors, db latency)", len(inc))
	}
}

func TestExplainSlownessUnknownTarget(t *testing.T) {
	g := seed(t)
	expl, _ := ExplainSlowness(g, "service/c/d/missing")
	if expl == "" {
		t.Fatal("explainer must always return a sentence")
	}
	expl, up := ExplainSlowness(g, "service/c/d/db")
	if expl == "" || len(up) != 1 {
		t.Fatalf("explainer = %q upstream = %v", expl, up)
	}
}
