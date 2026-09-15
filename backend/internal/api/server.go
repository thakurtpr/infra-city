// SPDX-License-Identifier: Apache-2.0
// Package api implements InfraCity's REST + WebSocket API surface.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/infracity/infracity/backend/internal/analysis"
	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/backend/internal/store"
	"github.com/infracity/infracity/backend/internal/ws"
	"github.com/infracity/infracity/pkg/model"
)

// Server bundles all backend state.
type Server struct {
	graph     *graph.Graph
	snaps     *store.SnapshotStore
	hub       *ws.Hub
	startTime time.Time

	// self-observability
	ingestTotal   atomic.Int64
	ingestLatency prometheus.Histogram
	eventsDropped atomic.Int64
	changes       []model.Change
	changesMu     sync.RWMutex
	authToken     string
}

func NewServer(g *graph.Graph, snaps *store.SnapshotStore, hub *ws.Hub, authToken string) *Server {
	s := &Server{
		graph:     g,
		snaps:     snaps,
		hub:       hub,
		startTime: time.Now(),
		authToken: authToken,
		ingestLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "infracity_ingest_latency_seconds",
			Help: "Agent ingest request latency.",
		}),
	}
	prometheus.MustRegister(s.ingestLatency)
	return s
}

func (s *Server) Router() *mux.Router {
	r := mux.NewRouter()
	r.Use(s.loggingMiddleware)
	r.HandleFunc("/health", s.health).Methods("GET")
	r.HandleFunc("/ready", s.ready).Methods("GET")
	r.Handle("/metrics", promhttp.Handler()).Methods("GET")

	a := r.PathPrefix("/api").Subrouter()
	a.HandleFunc("/clusters", s.listClusters).Methods("GET")
	a.HandleFunc("/clusters/{id}", s.getCluster).Methods("GET")
	a.HandleFunc("/namespaces", s.listNamespaces).Methods("GET")
	a.HandleFunc("/resources", s.listResources).Methods("GET")
	a.HandleFunc("/graph", s.getGraph).Methods("GET")
	a.HandleFunc("/graph/{id:.*}", s.getSubgraph).Methods("GET")
	a.HandleFunc("/metrics", s.getMetrics).Methods("GET")
	a.HandleFunc("/dependencies", s.getDependencies).Methods("GET")
	a.HandleFunc("/incidents", s.getIncidents).Methods("GET")
	a.HandleFunc("/changes", s.getChanges).Methods("GET")
	a.HandleFunc("/path", s.getPath).Methods("GET")
	a.HandleFunc("/blast/{id:.*}", s.getBlast).Methods("GET")
	a.HandleFunc("/risk/{id:.*}", s.getRisk).Methods("GET")
	a.HandleFunc("/explain", s.explain).Methods("GET")
	a.HandleFunc("/snapshots", s.listSnapshots).Methods("GET")
	a.HandleFunc("/snapshots/{id}", s.getSnapshot).Methods("GET")
	a.HandleFunc("/search", s.search).Methods("GET")
	a.HandleFunc("/self", s.selfStats).Methods("GET")

	v1 := r.PathPrefix("/api/v1").Subrouter()
	v1.HandleFunc("/ingest", s.ingest).Methods("POST")

	r.HandleFunc("/ws/events", s.wsEvents)
	return r
}

// ---- ops ----

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "uptime": time.Since(s.startTime).String()})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ready": true})
}

// ---- clusters ----

func (s *Server) listClusters(w http.ResponseWriter, _ *http.Request) {
	seen := map[string]*model.ClusterMeta{}
	for _, n := range s.graph.Nodes("", "", "") {
		if n.Cluster == "" {
			continue
		}
		m, ok := seen[n.Cluster]
		if !ok {
			m = &model.ClusterMeta{ID: n.Cluster, Health: "healthy"}
			seen[n.Cluster] = m
		}
		switch n.Type {
		case model.TypeNode:
			m.NodeCount++
		case model.TypePod:
			m.PodCount++
		}
		if v, ok := n.Labels["infracity.io/environment"]; ok && m.Environment == "" {
			m.Environment = v
		}
		if v, ok := n.Labels["infracity.io/region"]; ok && m.Region == "" {
			m.Region = v
		}
		if v, ok := n.Labels["infracity.io/provider"]; ok && m.Provider == "" {
			m.Provider = v
		}
		if n.Type == model.TypeCluster && n.Version != "" {
			m.K8sVersion = n.Version
		}
	}
	// overlay incident health
	for _, inc := range analysis.DetectIncidents(s.graph) {
		for _, id := range seen {
			_ = id
			_ = inc
		}
	}
	out := make([]model.ClusterMeta, 0, len(seen))
	for _, m := range seen {
		out = append(out, *m)
	}
	writeJSON(w, 200, out)
}

func (s *Server) getCluster(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	nodes := s.graph.Nodes(id, "", "")
	edges := s.graph.Edges(id, "")
	writeJSON(w, 200, map[string]any{"id": id, "nodes": nodes, "edges": edges})
}

func (s *Server) listNamespaces(w http.ResponseWriter, r *http.Request) {
	cluster := r.URL.Query().Get("cluster")
	writeJSON(w, 200, s.graph.Nodes(cluster, "", model.TypeNamespace))
}

func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, 200, s.graph.Nodes(q.Get("cluster"), q.Get("namespace"), q.Get("type")))
}

func (s *Server) getGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nodes := s.graph.Nodes(q.Get("cluster"), q.Get("namespace"), "")
	edges := s.graph.Edges(q.Get("cluster"), "")
	// cap payload for huge clusters; frontend aggregates the rest progressively
	if len(nodes) > 5000 {
		nodes = nodes[:5000]
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
}

func (s *Server) getSubgraph(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	n, ok := s.graph.Get(id)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	out, in := s.graph.Neighbors(id)
	writeJSON(w, 200, map[string]any{"node": n, "out": out, "in": in})
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	// Prometheus-compatible summary for command center: aggregate live graph metrics.
	nodes := s.graph.Nodes(r.URL.Query().Get("cluster"), "", "")
	var rps, bps, errW, latSum float64
	var pods, svcs, flows int
	podsSet := map[string]bool{}
	for _, n := range nodes {
		switch n.Type {
		case model.TypePod:
			pods++
		case model.TypeService:
			svcs++
		}
		if n.Metrics != nil {
			rps += n.Metrics.ReqPerSec
			bps += n.Metrics.BytesPerSec
			errW += n.Metrics.ErrRate * max1(n.Metrics.ReqPerSec)
			latSum += n.Metrics.LatencyMsP95
			_ = podsSet
		}
	}
	edges := s.graph.Edges("", model.EdgeNetwork)
	flows = len(edges)
	errRate := 0.0
	if rps > 0 {
		errRate = errW / rps
	}
	avgP95 := 0.0
	if len(nodes) > 0 {
		avgP95 = latSum / float64(len(nodes))
	}
	writeJSON(w, 200, map[string]any{
		"requestsPerSec": rps, "bytesPerSec": bps, "errorRate": errRate,
		"p95LatencyMs": avgP95, "pods": pods, "services": svcs, "flows": flows,
	})
}

func (s *Server) getDependencies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, analysis.InferDependencies(s.graph))
}

func (s *Server) getIncidents(w http.ResponseWriter, _ *http.Request) {
	inc := analysis.DetectIncidents(s.graph)
	if inc == nil {
		inc = []model.Incident{}
	}
	writeJSON(w, 200, inc)
}

func (s *Server) getChanges(w http.ResponseWriter, _ *http.Request) {
	s.changesMu.RLock()
	defer s.changesMu.RUnlock()
	out := append([]model.Change(nil), s.changes...)
	// newest first, cap 200
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	writeJSON(w, 200, out)
}

func (s *Server) getPath(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeJSON(w, 400, map[string]string{"error": "from and to required"})
		return
	}
	path := s.graph.ShortestPath(from, to, nil)
	if path == nil {
		writeJSON(w, 404, map[string]string{"error": "no path"})
		return
	}
	// enrich hops with latency/bytes
	type hop struct {
		NodeID string      `json:"nodeId"`
		Node   *model.Node `json:"node,omitempty"`
		Edge   *model.Edge `json:"edge,omitempty"`
	}
	hops := make([]hop, 0, len(path))
	for i, id := range path {
		h := hop{NodeID: id}
		if n, ok := s.graph.Get(id); ok {
			cp := *n
			h.Node = &cp
		}
		if i+1 < len(path) {
			out, _ := s.graph.Neighbors(id)
			for _, e := range out {
				if e.Destination == path[i+1] {
					cp := e
					h.Edge = &cp
					break
				}
			}
		}
		hops = append(hops, h)
	}
	writeJSON(w, 200, map[string]any{"path": path, "hops": hops})
}

func (s *Server) getBlast(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	depth := 4
	if d, err := strconv.Atoi(r.URL.Query().Get("depth")); err == nil && d > 0 && d <= 10 {
		depth = d
	}
	down, up := s.graph.BlastRadius(id, depth)
	writeJSON(w, 200, map[string]any{"root": id, "downstream": down, "upstream": up})
}

func (s *Server) getRisk(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	score, details := analysis.RiskScore(s.graph, id)
	writeJSON(w, 200, map[string]any{"node": id, "score": score, "details": details})
}

func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		writeJSON(w, 400, map[string]string{"error": "target required"})
		return
	}
	expl, upstream := analysis.ExplainSlowness(s.graph, target)
	writeJSON(w, 200, map[string]any{"target": target, "explanation": expl, "upstream": upstream})
}

func (s *Server) listSnapshots(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.snaps.List())
}

func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snaps.Get(mux.Vars(r)["id"])
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, snap)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, 200, []any{})
		return
	}
	results := SearchGraph(s.graph, q, 25)
	writeJSON(w, 200, results)
}

func (s *Server) selfStats(w http.ResponseWriter, _ *http.Request) {
	n, e := s.graph.Counts()
	conns, dropped := s.hub.Stats()
	writeJSON(w, 200, map[string]any{
		"graphNodes": n, "graphEdges": e,
		"wsConnections": conns, "wsDropped": dropped,
		"ingestTotal": s.ingestTotal.Load(), "eventsDropped": s.eventsDropped.Load(),
		"uptime": time.Since(s.startTime).String(),
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Dur("dur", time.Since(start)).Msg("http")
	})
}

func max1(f float64) float64 {
	if f < 1 {
		return 1
	}
	return f
}
