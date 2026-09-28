// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/infracity/infracity/pkg/model"
)

// Integration against real PostgreSQL. Locally:
//
//	docker run -d --name pg-test -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:16-alpine
//	TEST_POSTGRES_DSN='postgres://postgres:test@localhost:5432/postgres?sslmode=disable' go test ./backend/internal/store/ -run TestPostgres
//
// CI runs this via the persistence job (postgres service).
func pgDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN unset")
	}
	return dsn
}

func TestPostgresRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	be, err := NewPostgresBackend(ctx, pgDSN(t))
	if err != nil {
		t.Fatalf("connect+migrate: %v", err)
	}
	defer be.Close()

	snap := model.Snapshot{
		ID: "it-1", Timestamp: time.Now().UTC(), Label: "it",
		Nodes: []model.Node{{ID: "n1", Type: "pod"}},
		Edges: []model.Edge{{ID: "n1|x|n2", Source: "n1", Destination: "n2", Type: "x"}},
	}
	if err := be.Store(ctx, snap); err != nil {
		t.Fatalf("store: %v", err)
	}
	// idempotent re-store (ON CONFLICT DO NOTHING)
	if err := be.Store(ctx, snap); err != nil {
		t.Fatalf("re-store: %v", err)
	}
	got, err := be.Load(ctx, "it-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "n1" || len(got.Edges) != 1 {
		t.Fatalf("round trip = %+v", got)
	}
	if _, err := be.Load(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
	list, err := be.List(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("list = %d %v", len(list), err)
	}
	for _, entry := range list {
		if entry.Nodes != nil || entry.Edges != nil {
			t.Fatal("List must strip payloads")
		}
	}
}

func TestPostgresStoreFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	be, err := NewPostgresBackend(ctx, pgDSN(t))
	if err != nil {
		t.Fatalf("connect+migrate: %v", err)
	}
	defer be.Close()

	s := NewWithBackend(2, be)
	a := s.Add([]model.Node{{ID: "a"}}, nil, "a")
	b := s.Add([]model.Node{{ID: "b"}}, nil, "b")
	c := s.Add([]model.Node{{ID: "c"}}, nil, "c") // evicts a from ring
	if _, ok := s.Get(a.ID); !ok {
		t.Fatal("evicted ring entry must fall back to postgres")
	}
	_ = b
	_ = c
	if _, ok := s.Nearest(time.Now()); !ok {
		t.Fatal("nearest must work across ring+backend")
	}
}

func TestPostgresBadDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := NewPostgresBackend(ctx, "postgres://bad:bad@localhost:1/db"); err == nil {
		t.Fatal("unreachable DSN must fail closed")
	}
	if _, err := NewPostgresBackend(ctx, "://not a dsn"); err == nil {
		t.Fatal("garbage DSN must fail")
	}
}
