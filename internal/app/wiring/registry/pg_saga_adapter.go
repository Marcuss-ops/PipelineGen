package registry

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// pgMediaSagaDispatcherAdapter bridges the canonical PostgreSQL media
// delete/restore saga to the capability-level AssetMutationDispatcher port.
//
// MEDIA-SSOT P0-2 (September 2026): when the media PostgreSQL plane is
// deployed, restore/delete lifecycle mutations + their saga outbox events
// MUST commit against the PG media SSOT — not against the SQLite
// dispatcher. This adapter owns that routing so producers keep depending
// on the same narrow port.
type pgMediaSagaDispatcherAdapter struct {
	saga *pgmedia.PostgresMediaCommitter
}

var _ mutations.AssetMutationDispatcher = (*pgMediaSagaDispatcherAdapter)(nil)

// NewPGMediaSagaDispatcher constructs the PG media saga dispatcher and
// fails closed when the canonical PG committer is missing.
func NewPGMediaSagaDispatcher(saga *pgmedia.PostgresMediaCommitter) (mutations.AssetMutationDispatcher, error) {
	if saga == nil {
		return nil, mutations.ErrDispatcherUnavailable
	}
	return &pgMediaSagaDispatcherAdapter{saga: saga}, nil
}

func (a *pgMediaSagaDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	_ = ctx
	_ = clip
	_ = contentHash
	return mutations.ErrDispatcherUnavailable
}

func (a *pgMediaSagaDispatcherAdapter) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if a == nil || a.saga == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.saga.EnqueueAndRestore(ctx, assetID)
}

func (a *pgMediaSagaDispatcherAdapter) EnqueueAndDelete(ctx context.Context, assetID string) error {
	if a == nil || a.saga == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.saga.EnqueueIndexDelete(ctx, assetID)
}
