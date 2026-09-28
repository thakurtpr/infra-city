// SPDX-License-Identifier: Apache-2.0
// Package topology owns demo seeding + live demo traffic synthesis.
// Production topology comes from agents via /api/v1/ingest; this package keeps
// `make dev` and `make demo` alive with zero cluster required.
package topology

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"github.com/infracity/infracity/backend/internal/graph"
	"github.com/infracity/infracity/backend/internal/ws"
	"github.com/infracity/infracity/pkg/model"
)

const demoCluster = "production"

// SeedDemo builds: internet -> lb -> gateway -> frontend -> api -> {auth,payments,orders}
// payments -> postgres, api -> redis, orders -> kafka.
func SeedDemo(g *graph.Graph) {
	now := time.Now()
	mk := func(id, typ, ns, name, status string, m *model.Metrics, extra map[string]string) {
		g.UpsertNode(model.Node{
			ID: id, Type: typ, Cluster: demoCluster, Namespace: ns, Name: name,
			Status: status, CreatedAt: now, UpdatedAt: now, Metrics: m,
			Labels: map[string]string{
				"infracity.io/environment": "production",
				"infracity.io/region":      "us-west-2",
				"infracity.io/provider":    "aws",
			}, Metadata: extra,
		})
	}
	m := func(rps, err, p95, bps float64, cpu, mem float64) *model.Metrics {
		return &model.Metrics{ReqPerSec: rps, ErrRate: err, LatencyMsP95: p95, BytesPerSec: bps, CPUPct: cpu, MemPct: mem,
			CostPerMonth: 8 + cpu*1.5 + mem*0.7} // synthetic $/mo so cost mode has signal
	}

	mk(model.IDFor(model.TypeCluster, demoCluster, "", demoCluster), model.TypeCluster, "", demoCluster, "running", nil, map[string]string{"k8sVersion": "v1.31.2"})
	mk("node/production/node-a", model.TypeNode, "", "node-a", "ready", &model.Metrics{CPUPct: 46, MemPct: 61}, nil)
	mk("node/production/node-b", model.TypeNode, "", "node-b", "ready", &model.Metrics{CPUPct: 38, MemPct: 52}, nil)
	for _, ns := range []string{"frontend", "payments", "data", "messaging"} {
		mk(model.IDFor(model.TypeNamespace, demoCluster, ns, ns), model.TypeNamespace, ns, ns, "active", nil, nil)
	}
	mk("external/production/internet", model.TypeExternal, "", "internet", "active", nil, map[string]string{"role": "clients"})
	mk("external/production/aws-alb", model.TypeExternal, "", "aws-alb", "active", m(2400, 0.001, 38, 9e6, 12, 20), map[string]string{"role": "loadbalancer"})
	mk("gateway/production/default/infracity-gw", model.TypeGateway, "default", "infracity-gw", "running", m(2400, 0.001, 40, 9e6, 10, 18), nil)

	svcs := []struct {
		ns, name string
		met      *model.Metrics
		role     string
		typ      string
	}{
		{"frontend", "frontend", m(2200, 0.004, 68, 8e6, 55, 48), "", model.TypeDeployment},
		{"frontend", "checkout", m(640, 0.012, 210, 2.4e6, 62, 55), "", model.TypeDeployment},
		{"payments", "api", m(1284, 0.003, 42, 4.8e6, 58, 49), "", model.TypeDeployment},
		{"payments", "auth", m(980, 0.001, 24, 1.1e6, 30, 28), "", model.TypeDeployment},
		{"payments", "payments", m(410, 0.006, 120, 1.6e6, 66, 60), "", model.TypeDeployment},
		{"payments", "orders", m(380, 0.002, 88, 1.2e6, 44, 40), "", model.TypeDeployment},
		{"data", "postgres", m(390, 0.001, 18, 2.2e6, 48, 72), "postgres", model.TypeDatabase},
		{"data", "redis", m(1500, 0.0005, 3, 3.1e6, 35, 66), "redis", model.TypeDatabase},
		{"messaging", "kafka", m(900, 0.0008, 12, 5.4e6, 52, 58), "kafka", model.TypeDatabase},
	}
	for _, s := range svcs {
		mk(model.IDFor(s.typ, demoCluster, s.ns, s.name), s.typ, s.ns, s.name, "running", s.met, map[string]string{"role": s.role})
		mk(model.IDFor(model.TypeService, demoCluster, s.ns, s.name), model.TypeService, s.ns, s.name, "running",
			&model.Metrics{ReqPerSec: s.met.ReqPerSec, ErrRate: s.met.ErrRate, LatencyMsP95: s.met.LatencyMsP95, BytesPerSec: s.met.BytesPerSec}, nil)
	}
	// pods (2-3 each for deployment viz)
	for _, s := range svcs[:6] {
		for i := 1; i <= 3; i++ {
			pod := s.name + "-pod-" + string(rune('a'+i-1))
			mk(model.IDFor(model.TypePod, demoCluster, s.ns, pod), model.TypePod, s.ns, pod, "running",
				&model.Metrics{ReqPerSec: s.met.ReqPerSec / 3, ErrRate: s.met.ErrRate, LatencyMsP95: s.met.LatencyMsP95, CPUPct: s.met.CPUPct, MemPct: s.met.MemPct}, nil)
			g.UpsertEdge(model.Edge{Source: model.IDFor(s.typ, demoCluster, s.ns, s.name), Destination: model.IDFor(model.TypePod, demoCluster, s.ns, pod), Type: model.EdgeOwns})
		}
	}

	edge := func(src, dst, typ, proto string, rps, lat, bps, eps float64, dport int) {
		g.UpsertEdge(model.Edge{
			Source: src, Destination: dst, Type: typ, Protocol: proto,
			RequestsPerSec: rps, LatencyMs: lat, BytesPerSec: bps, ErrorsPerSec: eps,
			DstPort: dport, Connections: int64(rps / 4), UpdatedAt: now,
		})
	}
	FE, CO := "service/production/frontend/frontend", "service/production/frontend/checkout"
	API := "service/production/payments/api"
	AUTH := "service/production/payments/auth"
	PAY := "service/production/payments/payments"
	ORD := "service/production/payments/orders"
	PG := "database/production/data/postgres"
	RD := "database/production/data/redis"
	KF := "database/production/messaging/kafka"
	edge("external/production/internet", "external/production/aws-alb", model.EdgeNetwork, "HTTPS", 2400, 30, 9e6, 2, 443)
	edge("external/production/aws-alb", "gateway/production/default/infracity-gw", model.EdgeNetwork, "HTTPS", 2400, 34, 9e6, 2, 443)
	edge("gateway/production/default/infracity-gw", FE, model.EdgeRoutes, "HTTP", 2200, 40, 8e6, 8, 80)
	edge(FE, CO, model.EdgeNetwork, "HTTP", 640, 90, 2.4e6, 7, 8080)
	edge(CO, API, model.EdgeNetwork, "HTTP", 610, 110, 2.2e6, 6, 8080)
	edge(CO, PAY, model.EdgeNetwork, "gRPC", 410, 130, 1.6e6, 5, 9090)
	edge(API, AUTH, model.EdgeNetwork, "HTTP", 980, 24, 1.1e6, 1, 8080)
	edge(API, PAY, model.EdgeNetwork, "HTTP", 390, 120, 1.4e6, 3, 8080)
	edge(API, RD, model.EdgeNetwork, "TCP", 1500, 3, 3.1e6, 0, 6379)
	edge(PAY, PG, model.EdgeNetwork, "TCP", 390, 18, 2.2e6, 0, 5432)
	edge(ORD, KF, model.EdgeNetwork, "TCP", 900, 12, 5.4e6, 0, 9092)
	edge(ORD, PG, model.EdgeNetwork, "TCP", 360, 22, 1.8e6, 0, 5432)
}

// DemoTrafficLoop jitters edge rates so the city breathes; honors chaos flags via env.
func DemoTrafficLoop(ctx context.Context, g *graph.Graph, hub *ws.Hub) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			edges := g.Edges("", model.EdgeNetwork)
			for _, e := range edges {
				jitter := 0.85 + r.Float64()*0.3
				e.RequestsPerSec *= jitter
				e.BytesPerSec *= jitter
				e.LatencyMs *= 0.95 + r.Float64()*0.1
				e.UpdatedAt = time.Now()
				g.UpsertEdge(e)
			}
			// heartbeat: a live agent re-reports its whole world every
			// interval; the demo must do the same or graph TTL eviction
			// would eat nodes and non-network edges between seeds.
			now := time.Now()
			for _, n := range g.Nodes("", "", "") {
				n.UpdatedAt = now
				g.UpsertNode(n)
			}
			for _, e := range g.Edges("", "") {
				if e.Type == model.EdgeNetwork {
					continue // already jittered above
				}
				e.UpdatedAt = now
				g.UpsertEdge(e)
			}
			// occasionally broadcast a metric update sample
			if len(edges) > 0 {
				pick := edges[r.Intn(len(edges))]
				b, _ := json.Marshal(model.Event{Type: model.EvMetricUpdate, Edge: &pick, Timestamp: time.Now()})
				hub.Publish(b)
			}
		}
	}
}
