// Package mutations declares the strictly-scoped AssetMutationPrimitives
// interface that the outbox dispatcher and the admin purge path consume.
//
// Shape (B) — QDRANT-asset-mutation isolation (June 2026)
// ---------------------------------------------------------------
// The three primitive methods UpsertClip / Restore / HardDelete live
// canonically on *assets.ClipsRepository (the single infrastructure owner
// of media_assets). Production code paths in internal/application/**
// and internal/api/** MUST NOT reach those methods directly: every
// production write to media_assets goes through outbox.Dispatcher.EnqueueAndIndex
// (atomic UPSERT + outbox_events INSERT in a single tx) or through
// outbox.Dispatcher.EnqueueAndDelete for the lifecycle_state transitions.
//
// The AssetMutationPrimitives interface exists for the dispatcher to
// declare the narrowed 3-method slice it needs from the repository.
// Outbox.Dispatcher takes Primitives, not *ClipsRepository, so a future
// port swap (e.g. if we move UpsertClip into the canonical asset/native
// SQLite store) doesn't require re-wiring every consumer in the repo
// graph. The same pattern as qdrant/search_adapter.go::VectorStorePort
// (see AGENTS.md Pattern 0 — typed, signature-bearing, minimal).
//
// Why the call site outside the production application/api layers is
// the only allowed consumer:
//   - internal/application/** and internal/api/** are the production
//     callers; the CI lint in scripts/ci-architectural-checks.sh bans
//     direct UpsertClip/Restore/HardDelete calls in those paths.
//   - cmd/admin/* uses InternalAdminPurge (separate interface, see
//     internal/infrastructure/database/sqlite/admin/purge_ports.go) — NOT
//     this one. The admin and the dispatcher are deliberately bifurcated
//     so an admin tool cannot accidentally route through the live
//     outbox pool (admin tooling runs offline, with no worker active).
//
// Reading the file:
//   - AssetMutationPrimitives   : the 3-method narrowed surface for
//                                 outbox.Dispatcher and test fakes.
//   - ErrUnavailable            : sentinel for "primitives not wired"
//                                 (errors.Is compatible with the
//                                 assets.ErrAssetMutationDispatcherUnavailable
//                                 from the artlist package so the same
//                                 diagnostic phrasing reads across
//                                 packages).
package mutations

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ErrUnavailable is the sentinel returned by upstream code paths that detect
// the AssetMutationPrimitives dependency was not wired (nil dispatcher,
// asset.StoreBuilder turned off, partial-deploy configuration). Pair with
// the artlist.ErrAssetMutationDispatcherUnavailable so callers can check
// the diagnostic with the same `errors.Is` predicate — `mutations.ErrUnavailable`
// is the canonical, package-agnostic alias.
//
// Pattern: same identity as the preceding sentinel in artlist/errors.go
// so a single log message works across both call sites.
var ErrUnavailable = errors.New("mutations: asset mutation primitives unavailable")

// AssetMutationPrimitives is the strictly-scoped 3-method surface for
// direct writes to media_assets. ONLY outbox.Dispatcher (production
// writes) and the admin purge path (offline debug / physical repair)
// consume this interface; nothing else.
//
// Method semantics mirror the canonical implementation on
// *assets.ClipsRepository (see internal/infrastructure/database/sqlite/
// assets/clips_repository.go for the SQL + the //nolint:production
// marker annotations that flag these as dispatcher-only entry points):
//
//   - UpsertClip(ctx, clip) : upsert via the OUT-OF-OUTBOX low-level Save()
//                             path used inside the dispatcher's tx-bound
//                             UpsertClipTx. Production callers MUST go
//                             through dispatcher.EnqueueAndIndex instead.
//
//   - Restore(ctx, id)      : flips lifecycle_state to 'ready' for a
//                             previously soft-deleted row. Production code
//                             rarely needs this (the dispatcher handles
//                             the lifecycle state transitions). Exposed
//                             primarily for the admin recovery path.
//
//   - HardDelete(ctx, id)   : physically removes the row + dependent rows
//                             (asset_locations, asset_processing,
//                             asset_versions). Idempotent. Production
//                             callers MUST use dispatcher.EnqueueAndDelete
//                             so the Qdrant point is also cleaned up.
type AssetMutationPrimitives interface {
	UpsertClip(ctx context.Context, clip *asset.Asset) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
}
