// Package mutations — AssetMutationDispatcher canonical interface.
//
// This file declares the SINGLE SSOT for asset-mutation dispatching
// across the PipelineGen application layer. It is the foundational
// surface that every ingestion / restore / deletion producer MUST
// depend on (Artlist, sourcing, catalogsync, deletion, stock stockpipeline,
// images/google_vids_assets, ingest adapters, etc.). Task 2 of 5
// migrates each existing narrow dispatcher port to this single shape;
// task 1 of 5 lands only the interface + the compile-time assertion
// on the canonical outbox.Dispatcher so subsequent migrations have
// a stable target.
//
// Placement: internal/application/assets/mutations/
// ----------------------------------------------------
// This is the canonical home for the dispatcher port — the file sits
// alongside mutations/primitives.go (the auxiliary surface that
// outbox.Dispatcher consumes for its 3-method repository dependency).
// The two surfaces coexist deliberately:
//
//   - AssetMutationPrimitives (3 methods on the **REPO**):
//     UpsertClip / Restore / HardDelete. Consumed by outbox.Dispatcher
//     INSIDE the SQLite tx (the dispatcher's write primitives).
//
//   - AssetMutationDispatcher (3 methods on the **DISPATCHER**,
//     this file): EnqueueAndIndex / EnqueueAndRestore / EnqueueAndDelete.
//     Consumed by application-layer ingestion / restoration /
//     deletion producers. The dispatcher's tx-bound state stamp +
//     outbox event emission is the canonical writer route; the
//     AssetMutationPrimitives surface stays hidden inside that tx.
//
// Why the application-layer placement is intentional
// ----------------------------------------------------
// AGENTS.md Pattern 0 normally forbids paths in
// `internal/infrastructure/...` from importing
// `internal/application/...` — the dependency direction is inward
// (infrastructure → domain → application). This interface inverts
// the rule by design: the dispatcher (an infrastructure-level SQL
// writer + outbox event emitter) is the CANONICAL producer of
// media_assets mutations, and application producers MUST depend on
// the SSOT shape defined HERE. The compile-time assertion
// `var _ mutations.AssetMutationDispatcher = (*outbox.Dispatcher)(nil)`
// lives in the composition root (internal/app) so the dispatcher
// DOES NOT import this package directly — layering holds without
// sacrificing the canonical-SSOT property.
//
// Per Pattern 10 (sentinel errors + canonical narrowing), see
// pkg/apiutil.Error and assets.ErrAssetMutationDispatcherUnavailable
// for the related precedent.
//
// Method semantics (mirror the canonical outbox.Dispatcher impl)
// -----------------------------------------------------------------
//
//   - EnqueueAndIndex(ctx, asset, contentHash):
//     atomic UPSERT media_assets + INSERT outbox_events
//     (event_type='asset.index.requested', v1 envelope) + commit.
//     The dispatcher's IsFolder short-circuit means a folder asset
//     skips the outbox half (folder metadata still UPSERTs).
//
//   - EnqueueAndRestore(ctx, assetID):
//     atomic STATE STAMP media_assets.index_state=DISCOVERED (initial sentinel;
//     outbox handler re-indexes from scratch)
//
//   - INSERT outbox_events (event_type='asset.index.restore_requested',
//     v1 envelope) + commit. Handler (planned for task 3 of 5) completes
//     the picture with Qdrant re-upsert + lifecycle_state flip.
//
//   - EnqueueAndDelete(ctx, assetID):
//     atomic STATE STAMP media_assets.lifecycle_state=DELETE_REQUESTED
//
//   - INSERT outbox_events (event_type='asset.index.delete_requested',
//     v1 envelope) + commit. IndexDeleteHandler completes the picture
//     with Qdrant DeletePoints + SoftDelete + index_state=DELETED.
//
// Idempotency contract:
//   - Each method's eventKey shape pattern is documented inline at the
//     canonical implementation site (outbox.Dispatcher in
//     internal/infrastructure/database/sqlite/outbox/repository.go).
//     Repeated calls with the same assetID collapse via outbox_events'
//     ON CONFLICT(event_key) DO NOTHING — there is no separate retry
//     queue for any of these methods.
//   - Empty assetID is rejected by the dispatcher before any tx opens;
//     callers do NOT need to pre-validate.
//
// What is and isn't a consumer
// ---------------------------
// CONSUMING side (production code that calls AssetMutationDispatcher):
//   - internal/application/** producers (artlist, sourcing, catalogsync,
//     deletion, stock stockpipeline, images, ingest) — tasks 2 of 5
//     rewire each of them.
//   - internal/api/** HTTP handlers that need to enqueue mutations —
//     task 2 of 5 covers the API surface.
//
// PRODUCING side (the concrete implementation):
//   - *outbox.Dispatcher — the canonical atomically-tx-bound writer
//     that emits both media_assets writes AND outbox_events rows.
//   - test stubs in artlist/dispatcher_stub_test.go + future
//     testdouble packages — receive no-op behaviour for EnqueueAndIndex
//   - deleted/restore methods so unit tests run without a real
//     outbox pool.
//
// What is NOT a consumer:
//   - cmd/admin/ — admin tooling uses the lower-level CLI-driven flows
//     (reindex_qdrant, dr-qdrant); the AssetMutationDispatcher is a
//     production-asset writer. Admin paths route through their own
//     narrow admin.InternalAdminPurge interface for physical-purge
//     workflows (separate surface, separate package).
package mutations

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrDispatcherUnavailable is the canonical sentinel returned by
// upstream code paths that detect the AssetMutationDispatcher
// dependency was not wired (nil dispatcher at composition time,
// partial-deploy configuration, tests with no wired dispatcher).
//
// Identity mirrors mutations.ErrUnavailable (a parallel sentinel for
// the AssetMutationPrimitives surface kept in primitives.go). The two
// sentinels are deliberately distinct — callers branch on the
// specific surface that was unconfigured rather than conflating
// "no dispatcher wired" with "no primitive repository wired".
//
// Pair with errors.Is for compatibility:
//
//	if errors.Is(err, mutations.ErrDispatcherUnavailable) {
//	    // caller faced a dispatcher-not-wired regression
//	}
var ErrDispatcherUnavailable = errors.New("mutations: AssetMutationDispatcher unavailable")

// AssetMutationDispatcher is the canonical SINGLE-SSOT surface for
// asset-mutation dispatching across the PipelineGen application
// layer. Every code path that mutates media_assets (ingest, restore,
// delete) MUST route through this interface — the concrete
// implementation is the canonical outbox.Dispatcher (composition-root
// wrapped in a thin adapter that adds this assertion).
//
// The 3 methods mirror the canonical writer's responsibilities:
//
// Pattern 0 contract — exact signatures only:
//   - pointwise compatibility with *outbox.Dispatcher (composition root
//     adapters assert `var _ mutations.AssetMutationDispatcher =
//     (*outbox.Dispatcher)(nil)`); any drift in signature is a build
//     failure, not a runtime panic.
//   - non-exported State*(tx) helpers and outbox events are
//     implementation detail and stay at the producer site; callers
//     depend only on this 3-method surface.
type AssetMutationDispatcher interface {
	// EnqueueAndIndex atomically UPSERTs the asset row in media_assets
	// AND emits an outbox event of type 'asset.index.requested' (v1
	// envelope) in a SINGLE transaction. Folders short-circuit the
	// outbox half (Dispatcher's IsFolder check).
	//
	// contentHash is the ingest-time content fingerprint that the
	// worker's supersede-gate compares against the current
	// media_assets.metadata_json.$.content_hash to short-circuit
	// stale events (QDRANT-002, source_version pattern).
	//
	// Empty asset.ID is rejected before any tx opens.
	EnqueueAndIndex(ctx context.Context, a *asset.Asset, contentHash string) error

	// EnqueueAndRestore atomically STAMPS the row's index_state to
	// StateDiscovered (initial sentinel; outbox handler re-indexes
	// from scratch) AND emits an outbox event of type
	// 'asset.index.restore_requested' (v1 envelope). The restore
	// handler completes the picture with Qdrant re-upsert +
	// lifecycle_state flip to 'ready'.
	//
	// Empty assetID is rejected before any tx opens.
	EnqueueAndRestore(ctx context.Context, assetID string) error

	// EnqueueAndDelete atomically STAMPS the row's index_state to
	// StateDeletePending AND emits an outbox event of type
	// 'asset.index.delete_requested' (v1 envelope). The IndexDelete
	// handler (already exists from QDRANT-002 PR7) completes the
	// picture with Qdrant DeletePoints + SQLite SoftDelete +
	// index_state=DELETED.
	//
	// Empty assetID is rejected before any tx opens.
	EnqueueAndDelete(ctx context.Context, assetID string) error
}
