// dr/snapshot.go — QDRANT-005C PR3 SnapshotService.
//
// Three operations: Take (Create), List, Delete. The Restore flow
// lives in dr/restore.go because restore is SAFETY-CRITICAL — it
// gates the alias switch on ReindexVerifier.VerifyReindex, which
// has different concerns from "make a snapshot of this collection".
//
// QDRANT-005C PR3 (June 2026): constructed via NewSnapshotServiceFromDeps
// so test fixtures can mock SnapshotStore + Logger. Panics on nil Store
// (production wire-up MUST supply a concrete qdrant.Client wrapper).
// SnapshotService.Take/List return the shared domain SnapshotDescription
// alias. The infrastructure adapter passes the same type through without
// a field-by-field conversion.
package dr

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// SnapshotService manages the per-collection snapshot lifecycle:
// Take (Create) / List / Delete. Restore is intentionally NOT a
// SnapshotService operation — see dr/restore.go for the verify-then-
// switch contract.
type SnapshotService struct {
	store SnapshotStore
	log   *zap.Logger
}

// SnapshotServiceDeps bundles the injectable ports. Field nil-ability:
//
//	Required (panic if nil):  Store
//	Optional (zero default):   Log  (defaults to zap.NewNop())
type SnapshotServiceDeps struct {
	Store SnapshotStore
	Log   *zap.Logger
}

// NewSnapshotServiceFromDeps constructs a SnapshotService. Panics if
// deps.Store is nil (fail-loud for production misconfiguration).
func NewSnapshotServiceFromDeps(deps SnapshotServiceDeps) *SnapshotService {
	if deps.Store == nil {
		panic("dr.NewSnapshotServiceFromDeps: SnapshotServiceDeps.Store must not be nil")
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	return &SnapshotService{store: deps.Store, log: deps.Log}
}

// Take creates a new snapshot of the supplied collection. Returns the
// snapshot description (name + size + creation_time + checksum). The
// operator can subsequently call ListSnapshots to confirm the
// snapshot landed, or pass the name to dr.RestoreService.Restore for
// the verify-then-switch flow.
func (s *SnapshotService) Take(ctx context.Context, collection string) (*SnapshotDescription, error) {
	if collection == "" {
		return nil, fmt.Errorf("dr.SnapshotService.Take: collection must not be empty")
	}
	snap, err := s.store.CreateSnapshot(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("dr.SnapshotService.Take(%q): %w", collection, err)
	}
	s.log.Info("snapshot taken",
		zap.String("collection", collection),
		zap.String("name", snap.Name),
		zap.Int64("size_bytes", snap.Size),
		zap.Time("created_at", snap.CreationTime))
	return snap, nil
}

// List returns ALL snapshots for the supplied collection. Empty slice
// is a valid response (collection has never been snapshotted). No
// pagination — Qdrant returns the full list in one REST call, which
// is cheap; production collections typically hold tens of snapshots,
// not thousands.
func (s *SnapshotService) List(ctx context.Context, collection string) ([]SnapshotDescription, error) {
	if collection == "" {
		return nil, fmt.Errorf("dr.SnapshotService.List: collection must not be empty")
	}
	snaps, err := s.store.ListSnapshots(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("dr.SnapshotService.List(%q): %w", collection, err)
	}
	s.log.Info("snapshots listed",
		zap.String("collection", collection),
		zap.Int("count", len(snaps)))
	return snaps, nil
}

// Delete removes a single snapshot by name. Idempotent: a not-found
// snapshot is treated as success (mirrors qdrant.Client.DeleteSnapshot's
// contract). This matters because the retention sweep will routinely
// try to delete snapshots that have already been GC'd by another path.
func (s *SnapshotService) Delete(ctx context.Context, collection, snapshotName string) error {
	if collection == "" || snapshotName == "" {
		return fmt.Errorf("dr.SnapshotService.Delete: collection and snapshotName must not be empty")
	}
	if err := s.store.DeleteSnapshot(ctx, collection, snapshotName); err != nil {
		return fmt.Errorf("dr.SnapshotService.Delete(%q/%q): %w", collection, snapshotName, err)
	}
	s.log.Info("snapshot deleted",
		zap.String("collection", collection),
		zap.String("name", snapshotName))
	return nil
}
