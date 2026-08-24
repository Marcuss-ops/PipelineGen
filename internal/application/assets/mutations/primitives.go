// Package mutations declares the strictly-scoped AssetMutationPrimitives
// interface consumed by the outbox dispatcher + test fakes.
//
// Shape (B) — QDRANT-asset-mutation isolation (June 2026, Wave 22 task 5)
// ---------------------------------------------------------------
// UpsertClip is the ONLY method that survives on the production-canonical
// repository concrete (*assets.ClipsRepository), because the dispatcher
// returns it from the port requirement:
//
//	(1) Production writes — outbox.Dispatcher.EnqueueAndIndex / .EnqueueAndDelete
//	    own the upstream flow; the dispatcher is asserted against the
//	    separate AssetMutationDispatcher interface (3 methods, see
//	    dispatcher.go), NOT AssetMutationPrimitives. So the dispatcher's
//	    public surface is unaffected by the Primitives shrink below.
//	(2) Test fakes and dispatcher stubs that emulate the narrowed surface
//	    (see internal/capabilities/assets/providers/artlist/dispatcher_stub_test.go)
//	    consume the Primitives port.
//	(3) Restore and HardDelete are GONE from this interface and from
//	    the canonical repository concrete — they moved to the restricted
//	    tx-scoped package `internal/platform/sqlite/assets/txmutation/`
//	    as RestoreTx / HardDeleteTx. See architecture/deprecations.yaml
//	    under PR-CLIP-RAW-MUTATIONS for the deprecation record.
//
// Why UpsertClip stays on the repository concrete:
//   - Test fixtures in `internal/capabilities/assets/providers/artlist/*_test.go`
//     and the dispatcher stub call `repo.UpsertClip` to seed clip rows.
//     Keeping the method on the concrete (with the CI lint banning
//     production layer callers) avoids rewriting every test fixture.
//   - The dispatcher itself uses UpsertClipTx (tx-scoped variant) — the
//     AssetMutationDispatcher's compilation assertion in outbox/repository.go
//     works against the AssetMutationDispatcher (3 methods), not Primitives.
//
// Why Restore and HardDelete are removed entirely:
//   - Production code MUST NOT call Restore/HardDelete on the repository
//     directly: Restore bypasses the outbox (so a restored row leaves
//     Qdrant empty, the canonical re-index path is bypassed). HardDelete
//     permanently removes the row without emitting an
//     asset.index.delete_requested event, leaving the Qdrant point
//     orphaned. The only legitimate caller is the admin tool, which
//     goes through the InternalAdminPurge port. See
//     internal/platform/sqlite/admin/purge.go::PurgeService
//     which now uses txmutation.RestoreTx/HardDeleteTx directly
//     (caller-owned tx).
//
// Reading the file:
//   - AssetMutationPrimitives   : 1-method narrowed surface for test
//     stubs and any composition-root port
//     that needs UpsertClip specifically.
//   - ErrUnavailable            : sentinel for "primitives not wired"
//     (errors.Is compatible with the
//     artlist.ErrAssetMutationDispatcherUnavailable
//     so the same diagnostic phrasing reads
//     across packages).
package mutations

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

// AssetMutationPrimitives is the strictly-scoped 1-method surface for
// direct writes to media_assets. ONLY the dispatcher stub
// (internal/capabilities/assets/providers/artlist/dispatcher_stub_test.go)
// and any future composition-root port that needs UpsertClip specifically
// consume this interface. The outbox.Dispatcher itself is asserted
// against the ActionMutationDispatcher (3-method) interface, NOT Primitives.
//
// Method semantics mirror the canonical implementation on
// *assets.ClipsRepository (see internal/platform/sqlite/
// assets/clips_repository.go for the SQL + the //nolint:production
// marker annotation that flags it as dispatcher-only production layer
// entry point). Test fixtures are allowlisted explicitly:
//
//   - internal/capabilities/assets/providers/artlist/service_test.go:
//     `repo.UpsertClip(context.Background(), clip)` — explicit allowlist
//     note in the test fixture.
//
//   - internal/capabilities/assets/providers/artlist/dispatcher_stub_test.go:
//     the stub's `EnqueueAndIndex` delegates to `s.repo.UpsertClip` to
//     mirror production semantics.
//
//   - UpsertClip(ctx, clip) : upsert via the OUT-OF-OUTBOX low-level Save()
//     path. Production callers MUST go through
//     dispatcher.EnqueueAndIndex instead, which
//     performs the same upsert AND emits the
//     matching outbox_event in a single tx.
type AssetMutationPrimitives interface {
	UpsertClip(ctx context.Context, clip *asset.Asset) error
}
