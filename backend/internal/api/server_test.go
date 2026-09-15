// SPDX-License-Identifier: Apache-2.0
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/backend/internal/store"
	"github.com/infracity/infracity/backend/internal/ws"
	"github.com/infracity/infracity/pkg/model"
)

// Single shared server: NewServer registers Prometheus collectors globally,
// so only one instance may exist per test binary.
func testServer() *Server {
	g := graph.New()
	now := time.Now()
	g.UpsertNode(model.Node{
		ID:   model.IDFor(model.TypeService, "ci", "demo", "api"),
		Type: model.TypeService, Cluster: "ci", Namespace: "demo", Name: "api",
		Status: "running", UpdatedAt: now,
		Metrics: &model.Metrics{ReqPerSec: 100, ErrRate: 0.01, LatencyMsP95: 40},
	})
	g.UpsertNode(model.Node{
		ID:   model.IDFor(model.TypePod, "ci", "demo", "api-abc"),
		Type: model.TypePod, Cluster: "ci", Namespace: "demo", Name: "api-abc",
		Status: "running", UpdatedAt: now,
	})
	g.UpsertEdge(model.Edge{
		Source:      model.IDFor(model.TypeService, "ci", "demo", "api"),
		Destination: model.IDFor(model.TypePod, "ci", "demo", "api-abc"),
		Type:        model.EdgeTargets, UpdatedAt: now,
	})
	return NewServer(g, store.New(8), ws.NewHub(), "test-token")
}

var srv = testServer()

func get(t *testing.T, path string) (int, map[string]any, []any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d, want 200 (%s)", path, rec.Code, rec.Body.String())
	}
	// decode as generic; callers narrow
	var obj map[string]any
	var arr []any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	_ = arr
	return rec.Code, obj, nil
}

func TestHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("health = %d", rec.Code)
	}
}

func TestGraphAndSubgraph(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/graph?cluster=ci", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var g struct {
		Nodes []model.Node `json:"nodes"`
		Edges []model.Edge `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("graph = %d nodes %d edges, want 2/1", len(g.Nodes), len(g.Edges))
	}
}

func TestIngestAuth(t *testing.T) {
	rep := model.AgentReport{ClusterID: "ci", Timestamp: time.Now().UnixNano()}
	body, _ := json.Marshal(rep)

	// no token -> 401
	req := httptest.NewRequest("POST", "/api/v1/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("unauthenticated ingest = %d, want 401", rec.Code)
	}

	// valid token -> 202 and node visible
	rep.Nodes = []model.Node{{
		ID:   model.IDFor(model.TypeDeployment, "ci", "demo", "web"),
		Type: model.TypeDeployment, Cluster: "ci", Namespace: "demo", Name: "web",
	}}
	body, _ = json.Marshal(rep)
	req = httptest.NewRequest("POST", "/api/v1/ingest", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatalf("ingest = %d (%s), want 202", rec.Code, rec.Body.String())
	}
	if _, ok := srv.graph.Get(model.IDFor(model.TypeDeployment, "ci", "demo", "web")); !ok {
		t.Fatal("ingested node missing from graph")
	}
}

func TestSearchAndBlast(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/search?q=svc:api", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var res []model.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || len(res) != 1 {
		t.Fatalf("search = %v err=%v, want 1 result", rec.Body.String(), err)
	}

	req = httptest.NewRequest("GET", "/api/blast/"+model.IDFor(model.TypeService, "ci", "demo", "api"), nil)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var blast struct {
		Downstream []string `json:"downstream"`
		Upstream   []string `json:"upstream"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &blast); err != nil {
		t.Fatal(err)
	}
	if len(blast.Downstream) != 1 {
		t.Fatalf("blast downstream = %v, want 1", blast.Downstream)
	}
}

func TestIncidentsShape(t *testing.T) {
	// must be [] not null so the UI can .length it
	req := httptest.NewRequest("GET", "/api/incidents", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if string(rec.Body.Bytes()) == "null\n" {
		t.Fatal("incidents must serialize as [], never null")
	}
}

var _ = http.StatusOK
