// SPDX-License-Identifier: Apache-2.0
// Package analysis implements dependency inference, incident detection,
// blast-radius / risk scoring and the "why is this slow?" explainer.
//
// Every conclusion references actual graph metrics — the explainer never
// invents telemetry (see docs/architecture.md).
package analysis

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/pkg/model"
)

// InferDependencies builds Dependency records from observed network edges.
// Confidence grows with sample volume and stability: flows with sustained
// requests/sec score higher than single-sample observations.
func InferDependencies(g *graph.Graph) []model.Dependency {
	edges := g.Edges("", model.EdgeNetwork)
	out := make([]model.Dependency, 0, len(edges))
	for _, e := range edges {
		conf := confidenceFor(e.RequestsPerSec, e.Connections)
		out = append(out, model.Dependency{
			Source:     e.Source,
			Target:     e.Destination,
			Confidence: conf,
			ReqPerSec:  e.RequestsPerSec,
			LatencyMs:  e.LatencyMs,
			ErrRate:    errRate(e),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return out
}

func confidenceFor(rps float64, conns int64) float64 {
	// Logistic-ish curve: ~50 rps => ~0.8, ~1000 rps => ~0.98.
	c := 1 - math.Exp(-rps/60.0)
	if conns > 10 {
		c += 0.05
	}
	if c > 0.99 {
		c = 0.99
	}
	if c < 0.05 && (rps > 0 || conns > 0) {
		c = 0.05
	}
	return math.Round(c*100) / 100
}

func errRate(e model.Edge) float64 {
	if e.RequestsPerSec <= 0 {
		return 0
	}
	return e.ErrorsPerSec / e.RequestsPerSec
}

// DetectIncidents scans node metrics for error/latency/restart anomalies.
// Thresholds are deliberately conservative; production would use per-service baselines.
func DetectIncidents(g *graph.Graph) []model.Incident {
	var out []model.Incident
	now := time.Now().UnixNano()
	for _, n := range g.Nodes("", "", "") {
		if n.Metrics == nil {
			continue
		}
		m := n.Metrics
		var reasons []string
		sev := ""
		if m.ErrRate >= 0.2 {
			reasons = append(reasons, fmt.Sprintf("error rate %.1f%%", m.ErrRate*100))
			sev = "critical"
		} else if m.ErrRate >= 0.05 {
			reasons = append(reasons, fmt.Sprintf("error rate %.1f%%", m.ErrRate*100))
			if sev == "" {
				sev = "warning"
			}
		}
		if m.LatencyMsP95 >= 2000 {
			reasons = append(reasons, fmt.Sprintf("p95 latency %.0fms", m.LatencyMsP95))
			sev = "critical"
		} else if m.LatencyMsP95 >= 500 {
			reasons = append(reasons, fmt.Sprintf("p95 latency %.0fms", m.LatencyMsP95))
			if sev == "" {
				sev = "warning"
			}
		}
		if m.Restarts >= 5 {
			reasons = append(reasons, fmt.Sprintf("%d restarts", m.Restarts))
			if sev == "" {
				sev = "warning"
			}
		}
		if len(reasons) == 0 {
			continue
		}
		down, _ := g.BlastRadius(n.ID, 3)
		status := "firing"
		out = append(out, model.Incident{
			ID:          "inc-" + sanitize(n.ID),
			Title:       n.Name + " degraded (" + join(reasons, ", ") + ")",
			RootNode:    n.ID,
			Status:      status,
			Severity:    sev,
			StartedAt:   now,
			Affected:    append([]string{n.ID}, down...),
			Description: "Auto-detected from live metrics: " + join(reasons, ", ") + ".",
		})
	}
	return out
}

// ExplainSlowness answers "why is this slow?" by walking upstream network
// dependencies and ranking them by latency contribution. All claims cite
// observed edge/node metrics.
func ExplainSlowness(g *graph.Graph, target string) (string, []string) {
	node, ok := g.Get(target)
	if !ok {
		return "Resource not found in current graph.", nil
	}
	upstream := g.Traverse(target, false, 4, map[string]bool{model.EdgeNetwork: true, model.EdgeDepends: true})
	type scored struct {
		id  string
		p95 float64
		lat float64
	}
	var cands []scored
	if node.Metrics != nil {
		cands = append(cands, scored{target, node.Metrics.LatencyMsP95, node.Metrics.LatencyMsP95})
	}
	for _, id := range upstream {
		n, ok := g.Get(id)
		if !ok || n.Metrics == nil {
			continue
		}
		cands = append(cands, scored{id, n.Metrics.LatencyMsP95, n.Metrics.LatencyMsP95})
	}
	if len(cands) == 0 {
		return fmt.Sprintf("%s shows no latency telemetry yet. Check that the agent is reporting metrics for this resource and its dependencies.", target), upstream
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].p95 > cands[j].p95 })
	top := cands[0]
	expl := fmt.Sprintf("%s p95 is %.0fms. Highest upstream signal: %s at p95 %.0fms (observed, not inferred). "+
		"Likely path: %s. Downstream impact includes this resource and its callers — see blast radius.",
		shortName(target), p95of(g, target), shortName(top.id), top.p95, chainStr(append([]string{top.id}, target)))
	return expl, upstream
}

// RiskScore computes a 0..100 dependency risk score.
func RiskScore(g *graph.Graph, id string) (int, map[string]any) {
	down, up := g.BlastRadius(id, 6)
	central := g.Centrality(0)
	rank := -1
	for i, c := range central {
		if c.ID == id {
			rank = i + 1
			break
		}
	}
	n, _ := g.Get(id)
	rps := 0.0
	if n != nil && n.Metrics != nil {
		rps = n.Metrics.ReqPerSec
	}
	score := len(down)*4 + len(up)*2
	if rank > 0 && rank <= 5 {
		score += 20
	}
	if rps > 1000 {
		score += 15
	} else if rps > 100 {
		score += 8
	}
	spof := len(up) <= 1 && len(down) >= 3
	if spof {
		score += 15
	}
	if score > 100 {
		score = 100
	}
	details := map[string]any{
		"downstream": len(down), "upstream": len(up),
		"centralityRank": rank, "reqPerSec": rps,
		"singlePointOfFailure": spof,
		"criticality":          criticality(score),
	}
	return score, details
}

func criticality(s int) string {
	switch {
	case s >= 75:
		return "HIGH"
	case s >= 40:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

func p95of(g *graph.Graph, id string) float64 {
	n, ok := g.Get(id)
	if !ok || n.Metrics == nil {
		return 0
	}
	return n.Metrics.LatencyMsP95
}

func shortName(id string) string {
	// take last path segment
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '/' {
			return id[i+1:]
		}
	}
	return id
}

func chainStr(ids []string) string {
	s := ""
	for i, id := range ids {
		if i > 0 {
			s += " -> "
		}
		s += shortName(id)
	}
	return s
}

func join(xs []string, sep string) string {
	s := ""
	for i, x := range xs {
		if i > 0 {
			s += sep
		}
		s += x
	}
	return s
}

func sanitize(id string) string {
	out := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c == '/' || c == ':' {
			out = append(out, '-')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
