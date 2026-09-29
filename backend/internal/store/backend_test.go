// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/infracity/infracity/pkg/model"
)

// fakeBackend records calls and replays scripted data/errors.
type fakeBackend struct {
	stored   []model.Snapshot
	load     map[string]model.Snapshot
	list     []model.Snapshot
	storeErr error
	loadErr  error
	listErr  error
	closed   bool
}

func (f *fakeBackend) Store(_ context.Context, snap model.Snapshot) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	f.stored = append(f.stored, snap)
	return nil
}

func (f *fakeBackend) Load(_ context.Context, id string) (model.Snapshot, error) {
	if f.loadErr != nil {
		return model.Snapshot{}, f.loadErr
	}
	if s, ok := f.load[id]; ok {
		return s, nil
	}
	return model.Snapshot{}, ErrNotFound
}

func (f *fakeBackend) List(_ context.Context) ([]model.Snapshot, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}

func (f *fakeBackend) Close() error {
	f.closed = true
	return nil
}

func (f *fakeBackend) PruneBefore(_ context.Context, cutoff time.Time) (int64, error) {
	var kept []model.Snapshot
	var n int64
	for _, s := range f.stored {
		if s.Timestamp.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, s)
	}
	f.stored = kept
	return n, nil
}

func TestPruneSnapshots(t *testing.T) {
	fb := &fakeBackend{}
	s := NewWithBackend(8, fb)
	old := model.Snapshot{ID: "old", Timestamp: time.Now().Add(-time.Hour)}
	fb.stored = []model.Snapshot{old}
	s.Add(nil, nil, "new")
	n, err := s.PruneSnapshots(time.Now().Add(-time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("pruned = %d %v, want 1", n, err)
	}
	if _, ok := s.Get("old"); ok {
		t.Fatal("pruned snapshot must miss everywhere")
	}
	// ring-only store prunes nothing
	n, err = New(8).PruneSnapshots(time.Now())
	if err != nil || n != 0 {
		t.Fatalf("ring-only prune = %d %v, want 0", n, err)
	}
}

func TestAddSpillsToBackend(t *testing.T) {
	fb := &fakeBackend{}
	s := NewWithBackend(8, fb)
	snap := s.Add([]model.Node{{ID: "n"}}, nil, "x")
	if len(fb.stored) != 1 || fb.stored[0].ID != snap.ID {
		t.Fatalf("backend stored = %v", fb.stored)
	}
}

func TestAddFailOpen(t *testing.T) {
	fb := &fakeBackend{storeErr: errors.New("db down")}
	s := NewWithBackend(8, fb)
	snap := s.Add([]model.Node{{ID: "n"}}, nil, "x")
	// ring still serves despite backend failure
	if _, ok := s.Get(snap.ID); !ok {
		t.Fatal("ring must serve when backend fails")
	}
}

func TestGetFallsBackAndBackfills(t *testing.T) {
	fb := &fakeBackend{load: map[string]model.Snapshot{
		"old": {ID: "old", Nodes: []model.Node{{ID: "n"}}},
	}}
	s := NewWithBackend(8, fb)
	got, ok := s.Get("old")
	if !ok || len(got.Nodes) != 1 {
		t.Fatalf("fallback = %+v %v", got, ok)
	}
	// backfilled: second Get needs no backend
	fb.loadErr = errors.New("db down")
	if _, ok := s.Get("old"); !ok {
		t.Fatal("backfilled snapshot must survive backend outage")
	}
	if _, ok := s.Get("missing"); ok {
		t.Fatal("unknown id must miss")
	}
}

func TestListMergesRingAndBackend(t *testing.T) {
	fb := &fakeBackend{list: []model.Snapshot{{ID: "b"}}}
	s := NewWithBackend(8, fb)
	snap := s.Add(nil, nil, "r")
	// backend also claims the ring id: ring wins, no duplicates
	fb.list = append(fb.list, model.Snapshot{ID: snap.ID})
	got := s.List()
	if len(got) != 2 {
		t.Fatalf("merged list = %d, want 2", len(got))
	}
	for _, entry := range got {
		if entry.Nodes != nil || entry.Edges != nil {
			t.Fatal("List must strip payloads")
		}
	}
}

func TestListFailOpen(t *testing.T) {
	fb := &fakeBackend{listErr: errors.New("db down")}
	s := NewWithBackend(8, fb)
	s.Add(nil, nil, "r")
	if got := s.List(); len(got) != 1 {
		t.Fatalf("ring-only list = %d, want 1", len(got))
	}
}

func TestCloseNilSafe(t *testing.T) {
	if err := New(8).Close(); err != nil {
		t.Fatal(err)
	}
	fb := &fakeBackend{}
	s := NewWithBackend(8, fb)
	if err := s.Close(); err != nil || !fb.closed {
		t.Fatal("close must propagate")
	}
}
