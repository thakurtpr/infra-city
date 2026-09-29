// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/infracity/infracity/pkg/model"
)

//go:embed schema.sql
var schemaSQL string

// PostgresBackend is a SnapshotBackend over PostgreSQL (jackc/pgx, MIT).
// Works on stock Postgres 14+; when the TimescaleDB extension is present,
// schema.sql converts the table to a hypertable. Justification for the new
// dependency (per AGENTS.md): the architecture's documented "tomorrow"
// (docs/architecture.md); database/sql + lib/pq would work but pgx gives
// context-aware pooling, JSONB handling, and pgxpool health checks.
type PostgresBackend struct {
	pool *pgxpool.Pool
}

// NewPostgresBackend connects, pings, and migrates schema.sql.
// Fail-closed: a configured-but-unreachable database is a startup error,
// not silent history loss.
func NewPostgresBackend(ctx context.Context, dsn string) (*PostgresBackend, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	log.Info().Msg("snapshot backend: postgres durable log ready")
	return &PostgresBackend{pool: pool}, nil
}

func (b *PostgresBackend) Store(ctx context.Context, snap model.Snapshot) error {
	nodes, err := json.Marshal(snap.Nodes)
	if err != nil {
		return fmt.Errorf("marshal nodes: %w", err)
	}
	edges, err := json.Marshal(snap.Edges)
	if err != nil {
		return fmt.Errorf("marshal edges: %w", err)
	}
	_, err = b.pool.Exec(ctx,
		`INSERT INTO snapshots (id, ts, label, node_count, edge_count, nodes, edges)
		 VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb)
		 ON CONFLICT (id, ts) DO NOTHING`,
		snap.ID, snap.Timestamp.UTC(), snap.Label, len(snap.Nodes), len(snap.Edges),
		string(nodes), string(edges))
	if err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	return nil
}

func (b *PostgresBackend) Load(ctx context.Context, id string) (model.Snapshot, error) {
	var snap model.Snapshot
	var nodes, edges string
	err := b.pool.QueryRow(ctx,
		`SELECT id, ts, label, nodes, edges FROM snapshots WHERE id = $1`,
		id).Scan(&snap.ID, &snap.Timestamp, &snap.Label, &nodes, &edges)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Snapshot{}, ErrNotFound
	}
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("load snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(nodes), &snap.Nodes); err != nil {
		return model.Snapshot{}, fmt.Errorf("decode nodes: %w", err)
	}
	if err := json.Unmarshal([]byte(edges), &snap.Edges); err != nil {
		return model.Snapshot{}, fmt.Errorf("decode edges: %w", err)
	}
	return snap, nil
}

// List returns metadata only (payloads stripped), newest last like the ring.
func (b *PostgresBackend) List(ctx context.Context) ([]model.Snapshot, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT id, ts, label FROM snapshots ORDER BY ts ASC`)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	var out []model.Snapshot
	for rows.Next() {
		var s model.Snapshot
		if err := rows.Scan(&s.ID, &s.Timestamp, &s.Label); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	return out, nil
}

func (b *PostgresBackend) Close() error {
	b.pool.Close()
	return nil
}

// PruneBefore deletes snapshots older than cutoff (retention enforcement).
func (b *PostgresBackend) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := b.pool.Exec(ctx, `DELETE FROM snapshots WHERE ts < $1`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune snapshots: %w", err)
	}
	return tag.RowsAffected(), nil
}
