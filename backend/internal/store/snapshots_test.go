// SPDX-License-Identifier: Apache-2.0
package store

import (
	"testing"
	"time"

	"github.com/infracity/infracity/pkg/model"
)

func TestRingBuffer(t *testing.T) {
	s := New(3)
	for i := 0; i < 5; i++ {
		s.Add([]model.Node{{ID: "n"}}, nil, "")
	}
	if got := len(s.List()); got != 3 {
		t.Fatalf("ring kept %d, want 3", got)
	}
	full, ok := s.Get(s.List()[0].ID)
	if !ok || len(full.Nodes) != 1 {
		t.Fatal("Get must return full payload")
	}
	if len(s.List()[0].Nodes) != 0 {
		t.Fatal("List must strip payloads")
	}
}

func TestNearest(t *testing.T) {
	s := New(8)
	s.Add(nil, nil, "first")
	time.Sleep(5 * time.Millisecond)
	mid := time.Now()
	time.Sleep(5 * time.Millisecond)
	s.Add(nil, nil, "second")
	got, ok := s.Nearest(mid)
	if !ok || got.Label != "first" {
		t.Fatalf("nearest = %+v ok=%v, want first", got, ok)
	}
	if _, ok := s.Nearest(time.Now().Add(-time.Hour)); ok {
		t.Fatal("nearest before all snapshots must miss")
	}
}
