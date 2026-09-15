// Package store keeps ring-buffer snapshots for Time Travel + incident replay.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/infracity/infracity/pkg/model"
)

// SnapshotStore is an in-memory ring buffer. Production would back this with
// PostgreSQL + TimescaleDB (see docs/architecture.md); the interface is kept
// storage-agnostic so the swap is mechanical.
type SnapshotStore struct {
	mu        sync.RWMutex
	max       int
	snapshots []model.Snapshot
}

func New(max int) *SnapshotStore {
	if max <= 0 {
		max = 288 // 24h at 5m intervals by default
	}
	return &SnapshotStore{max: max}
}

func (s *SnapshotStore) Add(nodes []model.Node, edges []model.Edge, label string) model.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return snap
}

func (s *SnapshotStore) List() []model.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]model.Snapshot(nil), s.snapshots...)
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
	defer s.mu.RUnlock()
	for _, snap := range s.snapshots {
		if snap.ID == id {
			return snap, true
		}
	}
	return model.Snapshot{}, false
}

// Nearest returns the snapshot closest to (but not after) ts.
func (s *SnapshotStore) Nearest(ts time.Time) (model.Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *model.Snapshot
	for i := range s.snapshots {
		if !s.snapshots[i].Timestamp.After(ts) {
			if best == nil || s.snapshots[i].Timestamp.After(best.Timestamp) {
				c := s.snapshots[i]
				best = &c
			}
		}
	}
	if best == nil {
		return model.Snapshot{}, false
	}
	return *best, true
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
