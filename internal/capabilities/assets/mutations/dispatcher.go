// Package mutations — AssetMutationDispatcher canonical interface.
//
// This file declares the SINGLE SSOT for asset-mutation dispatching
// across the PipelineGen application layer. It is the foundational
// surface that every restore / deletion producer MUST depend on.
//
// godlike/06 SSOT (August 2026): EnqueueAndIndex has been REMOVED from
// this interface. All CREATE/UPDATE/INGEST producers MUST converge on
// mediacommit.MediaCommitter.CommitMediaAsset. This dispatcher now owns
// only EXISTING-ASSET lifecycle mutations: restore and delete.
//
// Placement: internal/application/assets/mutations/
// ----------------------------------------------------
// This is the canonical home for the dispatcher port — the file sits
// alongside mutations/primitives.go (the auxiliary surface that
// outbox.Dispatcher consumes for its repository dependency).
// The two surfaces coexist deliberately:
//
//   - AssetMutationPrimitives (methods on the **REPO**):
//     Restore / HardDelete. Consumed by outbox.Dispatcher
//     INSIDE the SQLite tx.
//
//   - AssetMutationDispatcher (2 methods on the **DISPATCHER**,
//     this file): EnqueueAndRestore / EnqueueAndDelete.
//     Consumed by application-layer restore / deletion producers.
//
// Method semantics
// -----------------------------------------------------------------
//
//   - EnqueueAndRestore(ctx, assetID):
//     atomic STATE STAMP media_assets.index_state=DISCOVERED (initial sentinel;
//     outbox handler re-indexes from scratch)
//
//     INSERT outbox_events (event_type='asset.index.restore_requested',
//     v1 envelope) + commit.
//
//   - EnqueueAndDelete(ctx, assetID):
//     atomic STATE STAMP media_assets.lifecycle_state=DELETE_REQUESTED
//
//     INSERT outbox_events (event_type='asset.index.delete_requested',
//     v1 envelope) + commit. IndexDeleteHandler completes the picture
//     with Qdrant DeletePoints + SoftDelete + index_state=DELETED.
//
// Idempotency contract:
//   - Each method's eventKey shape pattern is documented inline at the
//     canonical implementation site. Repeated calls with the same
//     assetID collapse via outbox_events' ON CONFLICT(event_key)
//     DO NOTHING.
//   - Empty assetID is rejected by the dispatcher before any tx opens.
//
// What is NOT a consumer:
//   - cmd/admin/ — admin tooling uses the lower-level CLI-driven flows
//     (reindex_qdrant, dr-qdrant).
package mutations

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrDispatcherUnavailable is the canonical sentinel returned by
// upstream code paths that detect the AssetMutationDispatcher
// dependency was not wired.
var ErrDispatcherUnavailable = errors.New("mutations: AssetMutationDispatcher unavailable")

// AssetMutationDispatcher is the canonical SINGLE-SSOT surface for
// EXISTING-ASSET lifecycle mutation dispatching across the PipelineGen
// application layer.
//
// godlike/06 SSOT (August 2026): MediaCommitter is the sole owner of
// CREATE/UPDATE/INGEST. This dispatcher owns only restore and delete.
//
// Pattern 0 contract — exact signatures only:
//   - pointwise compatibility with *outbox.Dispatcher (composition root
//     adapters assert); any drift in signature is a build failure.
type AssetMutationDispatcher interface {
	// EnqueueAndIndex is retained as a compatibility surface for existing
	// ingest producers. The concrete dispatcher routes the write through the
	// canonical AssetCommitter when it is configured.
	EnqueueAndIndex(ctx context.Context, a *asset.Asset, contentHash string) error

	// EnqueueAndRestore atomically STAMPS the row's index_state to
	// StateDiscovered (initial sentinel; outbox handler re-indexes
	// from scratch) AND emits an outbox event of type
	// 'asset.index.restore_requested' (v1 envelope).
	//
	// Empty assetID is rejected before any tx opens.
	EnqueueAndRestore(ctx context.Context, assetID string) error

	// EnqueueAndDelete atomically STAMPS the row's index_state to
	// StateDeletePending AND emits an outbox event of type
	// 'asset.index.delete_requested' (v1 envelope).
	//
	// Empty assetID is rejected before any tx opens.
	EnqueueAndDelete(ctx context.Context, assetID string) error
}
