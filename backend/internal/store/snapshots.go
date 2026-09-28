// SPDX-License-Identifier: Apache-2.0
// Package store keeps ring-buffer snapshots for Time Travel + incident replay.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/infracity/infracity/pkg/model"
)

// SnapshotStore is a hot ring buffer with an optional durable backend.
// Ring behavior (cap, ordering, payload stripping) is unchanged by the
// backend: the ring serves reads first, the backend extends history beyond
// it. Production wiring: NewPostgresBackend (postgres.go).
type SnapshotStore struct {
	mu        sync.RWMutex
	max       int
	snapshots []model.Snapshot
	backend   SnapshotBackend
}

func New(max int) *SnapshotStore {
	return NewWithBackend(max, nil)
}

// NewWithBackend attaches a durable SnapshotBackend (nil = ring only).
func NewWithBackend(max int, backend SnapshotBackend) *SnapshotStore {
	if max <= 0 {
		max = 288 // 24h at 5m intervals by default
	}
	return &SnapshotStore{max: max, backend: backend}
}

func (s *SnapshotStore) Add(nodes []model.Node, edges []model.Edge, label string) model.Snapshot {
	s.mu.Lock()
	snap := model.Snapshot{
		ID:        newID(),
		Timestamp: time.Now().UTC(),
		Nodes:     append([]model.Node(nil), nodes...),
		Edges:     append([]model.Edge(nil), edges...),
		Label:     label,
	}
	s.snapshots = append(s.snapshots, snap)
	if len(s.snapshots) > s.max {
		s.snapshots = s.snapshots[len(s.snapshots)-s.max:]
	}
	backend := s.backend
	s.mu.Unlock()
	// Durable spill is fail-open: a sick database must not break ingest.
	// The ring still serves; the warn carries the loss.
	if backend != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := backend.Store(ctx, snap); err != nil {
			log.Warn().Err(err).Str("snapshot", snap.ID).Msg("durable snapshot spill failed")
		}
	}
	return snap
}

func (s *SnapshotStore) List() []model.Snapshot {
	s.mu.RLock()
	out := append([]model.Snapshot(nil), s.snapshots...)
	backend := s.backend
	s.mu.RUnlock()
	if backend != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stored, err := backend.List(ctx); err != nil {
			log.Warn().Err(err).Msg("durable snapshot list failed, serving ring")
		} else {
			seen := map[string]bool{}
			for _, snap := range out {
				seen[snap.ID] = true
			}
			for _, snap := range stored {
				if !seen[snap.ID] {
					out = append(out, snap)
					seen[snap.ID] = true
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	// strip payloads for list calls (client fetches full snapshot on demand)
	for i := range out {
		out[i].Nodes = nil
		out[i].Edges = nil
	}
	return out
}

func (s *SnapshotStore) Get(id string) (model.Snapshot, bool) {
	s.mu.RLock()
	for _, snap := range s.snapshots {
		if snap.ID == id {
			s.mu.RUnlock()
			return snap, true
		}
	}
	backend := s.backend
	s.mu.RUnlock()
	if backend == nil {
		return model.Snapshot{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := backend.Load(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Warn().Err(err).Str("snapshot", id).Msg("durable snapshot load failed")
		}
		return model.Snapshot{}, false
	}
	// backfill the hot ring so repeat views stay cheap
	s.mu.Lock()
	s.snapshots = append(s.snapshots, snap)
	if len(s.snapshots) > s.max {
		s.snapshots = s.snapshots[len(s.snapshots)-s.max:]
	}
	s.mu.Unlock()
	return snap, true
}

// Nearest returns the snapshot closest to (but not after) ts, from ring +
// durable history alike.
func (s *SnapshotStore) Nearest(ts time.Time) (model.Snapshot, bool) {
	var bestID string
	var best time.Time
	for _, snap := range s.List() {
		if !snap.Timestamp.After(ts) && (bestID == "" || snap.Timestamp.After(best)) {
			bestID, best = snap.ID, snap.Timestamp
		}
	}
	if bestID == "" {
		return model.Snapshot{}, false
	}
	return s.Get(bestID)
}

// Close releases the durable backend, if any. Nil-safe.
func (s *SnapshotStore) Close() error {
	s.mu.RLock()
	backend := s.backend
	s.mu.RUnlock()
	if backend == nil {
		return nil
	}
	return backend.Close()
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
