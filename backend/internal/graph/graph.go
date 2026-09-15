// Package graph implements InfraCity's in-memory infrastructure graph engine.
//
// Design notes:
//   - Single-writer-friendly RWMutex; reads (API/WS fan-out) vastly outnumber writes.
//   - Nodes keyed by canonical ID; edges keyed by source|type|destination.
//   - Start with in-process graph + PostgreSQL-compatible snapshot model rather
//     than a dedicated graph DB (see docs/architecture.md for tradeoffs).
//   - All traversal helpers are pure w.r.t. a read snapshot to keep them testable.
package graph

import (
	"sort"
	"sync"
	"time"

	"github.com/infracity/infracity/pkg/model"
)

// Graph is the single source of truth served to the frontend.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*model.Node
	edges map[string]*model.Edge

	// adjacency for fast traversal
	out map[string]map[string]*model.Edge // source -> edgeID -> edge
	in  map[string]map[string]*model.Edge // dest   -> edgeID -> edge
}

func New() *Graph {
	return &Graph{
		nodes: make(map[string]*model.Node),
		edges: make(map[string]*model.Edge),
		out:   make(map[string]map[string]*model.Edge),
		in:    make(map[string]map[string]*model.Edge),
	}
}

func edgeKey(source, typ, dest string) string { return source + "|" + typ + "|" + dest }

// UpsertNode inserts or merges a node. Returns true if created.
func (g *Graph) UpsertNode(n model.Node) bool {
	if n.ID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = time.Now()
	}
	if existing, ok := g.nodes[n.ID]; ok {
		// Merge: keep newest metrics, union labels/metadata.
		merged := *existing
		if n.Status != "" {
			merged.Status = n.Status
		}
		if n.Metrics != nil {
			merged.Metrics = n.Metrics
		}
		if n.IP != "" {
			merged.IP = n.IP
		}
		if n.NodeName != "" {
			merged.NodeName = n.NodeName
		}
		for k, v := range n.Labels {
			if merged.Labels == nil {
				merged.Labels = map[string]string{}
			}
			merged.Labels[k] = v
		}
		for k, v := range n.Metadata {
			if merged.Metadata == nil {
				merged.Metadata = map[string]string{}
			}
			merged.Metadata[k] = v
		}
		merged.UpdatedAt = n.UpdatedAt
		g.nodes[n.ID] = &merged
		return false
	}
	cp := n
	g.nodes[n.ID] = &cp
	return true
}

// UpsertEdge inserts or merges an edge. Network edges aggregate rates.
func (g *Graph) UpsertEdge(e model.Edge) bool {
	if e.Source == "" || e.Destination == "" || e.Type == "" {
		return false
	}
	if e.ID == "" {
		e.ID = edgeKey(e.Source, e.Type, e.Destination)
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.edges[e.ID]; ok {
		merged := *existing
		// For observed flows take the latest sample; for inferred deps keep max confidence.
		if e.RequestsPerSec != 0 || e.BytesPerSec != 0 || e.LatencyMs != 0 {
			merged.RequestsPerSec = e.RequestsPerSec
			merged.BytesPerSec = e.BytesPerSec
			merged.LatencyMs = e.LatencyMs
			merged.ErrorsPerSec = e.ErrorsPerSec
			merged.Connections = e.Connections
			merged.Protocol = firstNonEmpty(e.Protocol, merged.Protocol)
		}
		if e.Confidence > merged.Confidence {
			merged.Confidence = e.Confidence
		}
		if e.Allowed != nil {
			merged.Allowed = e.Allowed
		}
		if e.DstPort != 0 {
			merged.DstPort = e.DstPort
		}
		merged.UpdatedAt = e.UpdatedAt
		g.edges[e.ID] = &merged
		return false
	}
	cp := e
	g.edges[e.ID] = &cp
	if g.out[e.Source] == nil {
		g.out[e.Source] = map[string]*model.Edge{}
	}
	if g.in[e.Destination] == nil {
		g.in[e.Destination] = map[string]*model.Edge{}
	}
	g.out[e.Source][e.ID] = &cp
	g.in[e.Destination][e.ID] = &cp
	return true
}

// RemoveNode deletes a node and all incident edges.
func (g *Graph) RemoveNode(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.nodes, id)
	for eid, e := range g.edges {
		if e.Source == id || e.Destination == id {
			delete(g.edges, eid)
			if m, ok := g.out[e.Source]; ok {
				delete(m, eid)
			}
			if m, ok := g.in[e.Destination]; ok {
				delete(m, eid)
			}
		}
	}
}

// Get returns a copy of a node.
func (g *Graph) Get(id string) (*model.Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, false
	}
	cp := *n
	return &cp, true
}

// Counts returns total nodes/edges (for /health, command center, self-metrics).
func (g *Graph) Counts() (nodes, edges int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes), len(g.edges)
}

// Nodes returns a filtered copy list (cluster/namespace/type filters optional).
func (g *Graph) Nodes(cluster, namespace, typ string) []model.Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]model.Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		if cluster != "" && n.Cluster != cluster {
			continue
		}
		if namespace != "" && n.Namespace != namespace {
			continue
		}
		if typ != "" && n.Type != typ {
			continue
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Edges returns a filtered copy list.
func (g *Graph) Edges(cluster, typ string) []model.Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]model.Edge, 0, len(g.edges))
	for _, e := range g.edges {
		if typ != "" && e.Type != typ {
			continue
		}
		_ = cluster
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Neighbors returns out/in edges of a node (copies).
func (g *Graph) Neighbors(id string) (out, in []model.Edge) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, e := range g.out[id] {
		out = append(out, *e)
	}
	for _, e := range g.in[id] {
		in = append(in, *e)
	}
	return out, in
}

// Traverse walks upstream (reverse edges) or downstream (forward edges) up to depth.
// edgeTypes nil = all types.
func (g *Graph) Traverse(start string, downstream bool, depth int, edgeTypes map[string]bool) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if depth <= 0 {
		depth = 10
	}
	seen := map[string]bool{start: true}
	frontier := []string{start}
	result := []string{}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, cur := range frontier {
			var adj map[string]*model.Edge
			if downstream {
				adj = g.out[cur]
			} else {
				adj = g.in[cur]
			}
			for _, e := range adj {
				if edgeTypes != nil && !edgeTypes[e.Type] {
					continue
				}
				nxt := e.Destination
				if !downstream {
					nxt = e.Source
				}
				if !seen[nxt] {
					seen[nxt] = true
					next = append(next, nxt)
					result = append(result, nxt)
				}
			}
		}
		frontier = next
	}
	return result
}

// ShortestPath (BFS) between two nodes following directed out-edges of given types.
func (g *Graph) ShortestPath(from, to string, edgeTypes map[string]bool) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if from == to {
		return []string{from}
	}
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.out[cur] {
			if edgeTypes != nil && !edgeTypes[e.Type] {
				continue
			}
			if _, ok := prev[e.Destination]; !ok {
				prev[e.Destination] = cur
				if e.Destination == to {
					path := []string{to}
					for p := cur; p != ""; p = prev[p] {
						path = append([]string{p}, path...)
					}
					return path
				}
				queue = append(queue, e.Destination)
			}
		}
	}
	return nil
}

// BlastRadius returns downstream dependents + upstream dependencies of root.
func (g *Graph) BlastRadius(root string, depth int) (downstream, upstream []string) {
	flowTypes := map[string]bool{model.EdgeNetwork: true, model.EdgeDepends: true, model.EdgeTargets: true, model.EdgeRoutes: true}
	downstream = g.Traverse(root, true, depth, flowTypes)
	upstream = g.Traverse(root, false, depth, flowTypes)
	return downstream, upstream
}

// Centrality ranks nodes by total degree (simple, fast, explainable).
// Returns top-N node IDs with scores.
func (g *Graph) Centrality(topN int) []struct {
	ID    string
	Score int
} {
	g.mu.RLock()
	defer g.mu.RUnlock()
	type kv struct {
		ID    string
		Score int
	}
	all := make([]kv, 0, len(g.nodes))
	for id := range g.nodes {
		all = append(all, kv{id, len(g.out[id]) + len(g.in[id])})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if topN > 0 && len(all) > topN {
		all = all[:topN]
	}
	out := make([]struct {
		ID    string
		Score int
	}, len(all))
	for i, kv := range all {
		out[i].ID, out[i].Score = kv.ID, kv.Score
	}
	return out
}

// Snapshot returns a deep copy for time-travel storage.
func (g *Graph) Snapshot() (nodes []model.Node, edges []model.Edge) {
	return g.Nodes("", "", ""), g.Edges("", "")
}

// LoadSnapshot replaces graph contents (used by time-travel replay/tests).
func (g *Graph) LoadSnapshot(nodes []model.Node, edges []model.Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes = make(map[string]*model.Node, len(nodes))
	g.edges = make(map[string]*model.Edge, len(edges))
	g.out = map[string]map[string]*model.Edge{}
	g.in = map[string]map[string]*model.Edge{}
	for _, n := range nodes {
		cp := n
		g.nodes[n.ID] = &cp
	}
	for _, e := range edges {
		cp := e
		if cp.ID == "" {
			cp.ID = edgeKey(cp.Source, cp.Type, cp.Destination)
		}
		g.edges[cp.ID] = &cp
		if g.out[cp.Source] == nil {
			g.out[cp.Source] = map[string]*model.Edge{}
		}
		if g.in[cp.Destination] == nil {
			g.in[cp.Destination] = map[string]*model.Edge{}
		}
		g.out[cp.Source][cp.ID] = &cp
		g.in[cp.Destination][cp.ID] = &cp
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
