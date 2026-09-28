// SPDX-License-Identifier: Apache-2.0
package topology

import (
	"testing"

	"github.com/infracity/infracity/backend/internal/analysis"
	"github.com/infracity/infracity/backend/internal/graph"
)

const pgID = "database/production/data/postgres"

// seedDemo returns a freshly seeded graph (SeedDemo overwrites the package
// baselines idempotently, so tests stay independent).
func seedDemo() *graph.Graph {
	g := graph.New()
	SeedDemo(g)
	return g
}

func TestDemoChaosLatencyFiresIncident(t *testing.T) {
	g := seedDemo()

	t.Setenv("INFRACITY_CHAOS_LATENCY_MS", "2500")
	applyDemoChaos(g)

	n, ok := g.Get(pgID)
	if !ok {
		t.Fatal("postgres node missing")
	}
	if got := n.Metrics.LatencyMsP95; got != 2518 {
		t.Fatalf("postgres p95 = %.0f, want 2518 (18 base + 2500 chaos)", got)
	}
	found := false
	for _, inc := range analysis.DetectIncidents(g) {
		if inc.RootNode == pgID {
			found = true
			if inc.Severity != "critical" {
				t.Fatalf("severity = %s, want critical (p95 2518)", inc.Severity)
			}
		}
	}
	if !found {
		t.Fatal("no incident rooted at postgres")
	}

	// clearing the flag restores the baseline on the next pass
	t.Setenv("INFRACITY_CHAOS_LATENCY_MS", "")
	applyDemoChaos(g)
	n, _ = g.Get(pgID)
	if got := n.Metrics.LatencyMsP95; got != 18 {
		t.Fatalf("restored p95 = %.0f, want 18", got)
	}
	for _, inc := range analysis.DetectIncidents(g) {
		if inc.RootNode == pgID {
			t.Fatal("incident still firing after chaos cleared")
		}
	}
}

func TestDemoChaosErrors(t *testing.T) {
	g := seedDemo()

	t.Setenv("INFRACITY_CHAOS_ERRORS_PCT", "25")
	applyDemoChaos(g)
	n, _ := g.Get(pgID)
	if got := n.Metrics.ErrRate; got != 0.25 {
		t.Fatalf("err = %.2f, want 0.25", got)
	}
	t.Setenv("INFRACITY_CHAOS_ERRORS_PCT", "")
	t.Setenv("INFRACITY_CHAOS_LATENCY_MS", "")
	applyDemoChaos(g)
	n, _ = g.Get(pgID)
	if got := n.Metrics.ErrRate; got != 0.001 {
		t.Fatalf("restored err = %.4f, want 0.001", got)
	}
}

func TestEnvFloat(t *testing.T) {
	t.Setenv("INFRACITY_CHAOS_LATENCY_MS", "abc")
	if got := envFloat("INFRACITY_CHAOS_LATENCY_MS"); got != 0 {
		t.Fatalf("garbage = %.0f, want 0", got)
	}
	t.Setenv("INFRACITY_CHAOS_LATENCY_MS", "-5")
	if got := envFloat("INFRACITY_CHAOS_LATENCY_MS"); got != 0 {
		t.Fatalf("negative = %.0f, want 0", got)
	}
}
