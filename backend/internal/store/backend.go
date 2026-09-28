// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"

	"github.com/infracity/infracity/pkg/model"
)

// ErrNotFound is returned by SnapshotBackend.Load for unknown IDs.
var ErrNotFound = errors.New("snapshot not found")

// SnapshotBackend persists snapshots beyond the in-memory ring.
// Implementations: in-process ring (nil backend = ring only), PostgreSQL
// (postgres.go; Timescale hypertable when the extension is available).
// All methods must be safe for concurrent use; List returns metadata
// (payloads stripped, like SnapshotStore.List).
type SnapshotBackend interface {
	Store(ctx context.Context, snap model.Snapshot) error
	Load(ctx context.Context, id string) (model.Snapshot, error)
	List(ctx context.Context) ([]model.Snapshot, error)
	Close() error
}
