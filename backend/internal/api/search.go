// SPDX-License-Identifier: Apache-2.0
package api

import (
	"strings"

	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/pkg/model"
)

// SearchGraph supports free text plus qualified queries:
// namespace:payments service:checkout pod:api-7f8d9 cluster:production
func SearchGraph(g *graph.Graph, q string, limit int) []model.Node {
	q = strings.TrimSpace(q)
	field, term := "", strings.ToLower(q)
	if i := strings.Index(q, ":"); i > 0 {
		field, term = strings.ToLower(strings.TrimSpace(q[:i])), strings.ToLower(strings.TrimSpace(q[i+1:]))
	}
	var out []model.Node
	for _, n := range g.Nodes("", "", "") {
		match := false
		switch field {
		case "namespace", "ns":
			match = strings.Contains(strings.ToLower(n.Namespace), term)
		case "service", "svc":
			match = n.Type == model.TypeService && contains(n, term)
		case "pod":
			match = n.Type == model.TypePod && contains(n, term)
		case "cluster":
			match = strings.Contains(strings.ToLower(n.Cluster), term)
		case "type", "kind":
			match = strings.Contains(strings.ToLower(n.Type), term)
		case "":
			match = contains(n, term)
		default:
			match = contains(n, strings.ToLower(field+":"+term))
		}
		if match {
			out = append(out, n)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func contains(n model.Node, term string) bool {
	return strings.Contains(strings.ToLower(n.Name), term) ||
		strings.Contains(strings.ToLower(n.ID), term) ||
		strings.Contains(strings.ToLower(n.Namespace), term)
}
