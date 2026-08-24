package imagesregistry

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_crud.go holds the *ClipsRepository methods that map to the
// canonical mutation surface (CRUD-shaped): Upsert + Get + GetClip +
// SourceVersionFor (the narrow SSOT port implementation), the
// dispatcher-only UpsertClip + DeleteClip wrappers, and the typed
// command-dispatcher Mutate entry point. SetIndexState/SoftDelete/DeleteClipByDriveLink
// live in clips_index_state.go; tx-scoped mutations in clips_transactions.go;
// filtered Count in clips_queries.go; ResolveBy* lookups in clips_resolution.go.
// The receiver type + helpers + MediaAssetColumns live in clips_repository.go.

// Upsert is the canonical low-level write path that ALL production
// callers eventually flow into (via AssetStoreSQLite.Save). It is
// public because the canonical asset.Repository wrapper calls it;
// the narrow API surface for callers is Upsert only.
//
// QDRANT-asset-mutation isolation (June 2026): Upsert itself
// bypasses the outbox. Production callers that need vector
// indexing MUST use outbox.Dispatcher.EnqueueAndIndex (which
// performs the UPSERT and outbox_events INSERT in a single
// atomic tx). Methods flagged with `//nolint:production` below
// are dispatcher-only entry points; the CI lint in
// scripts/ci-architectural-checks.sh bans them in
// internal/application + internal/api paths.
func (r *ClipsRepository) Upsert(ctx context.Context, m *asset.Asset) error {
	return r.AssetStoreSQLite.Save(ctx, &asset.Details{Asset: m})
}

func (r *ClipsRepository) Get(ctx context.Context, id string) (*asset.Asset, error) {
	details, err := r.AssetStoreSQLite.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if details == nil {
		return nil, nil
	}
	return details.Asset, nil
}

func (r *ClipsRepository) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return r.Get(ctx, id)
}

// SourceVersionFor is the PR 11 follow-up narrow port implementation
// consumed by the IndexingHandler source_version supersede gate.
// Delegates to the package-level helper (source_version.go) so the
// priority-chain semantics are owned by ONE function even though two
// upstream callers (this method + cmd/admin inline) flow through it.
//
// Returns sql.ErrNoRows unchanged so the upstream consumer can
// distinguish "row missing" from "row exists but empty fingerprint".
// Both paths fall through to "skip the gate, let IndexClip decide";
// the diagnostic value of distinguishing them lives in tests
// (TestSourceVersionFor_AssetNotFoundReturnsErrNoRows).
//
// Note: GetClip (above) remains because IndexDeleteHandler keeps it
// via the AssetDeleter interface — that's a separate concern
// (deletion rather than version lookup). Removing GetClip would
// trigger a separate refactor (AssetDeleter → AssetMutator) which
// is out of scope for the PR 11 followup.
func (r *ClipsRepository) SourceVersionFor(ctx context.Context, id string) (string, error) {
	return SourceVersionFor(ctx, r.db, id)
}

// UpsertClip upserts a clip through the low-level Save() path.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. Callers that need vector
// indexing MUST use outbox.Dispatcher.EnqueueAndIndex instead, which
// performs the UPSERT and outbox_events INSERT in a single atomic tx.
//
// QDRANT-asset-mutation isolation (June 2026): //nolint:production.
// Production callers (internal/application/**, internal/api/**)
// MUST NOT call this directly. The legitimate callers are:
//  1. The dispatcher itself, which wraps this call inside an
//     outbox transaction via UpsertClipTx + emits an outbox event
//     in the same tx (the canonical QDRANT-002 path).
//  2. The admin tool's InternalAdminPurge adapter, when
//     back-filling a row in a scenario where the worker pool is
//     offline; the admin path uses `assets.ClipsRepository.Upsert`
//     rather than this method (which is dispatcher-only).
//  3. Tests via the dispatcher stub or a bare `&Service{}` fixture
//     (test code paths are explicitly allowlisted by the CI lint).
//
// Removed from public API surfaces:
//   - artlist.AssetStore (search_core_test only)
//   - clips.ClipRepositoryPort (clip_ops.go only)
//   - sourcing.ClipStorePort (sourcing/service.go only)
//
// Per the user's verify-the-rg-test contract:
//
//	`rg 'UpsertClip\(' internal/application internal/api` returns
//	ZERO production hits (test hits allowed).
func (r *ClipsRepository) UpsertClip(ctx context.Context, clip *asset.Asset) error {
	return r.Upsert(ctx, clip)
}

func (r *ClipsRepository) DeleteClip(ctx context.Context, id string) error {
	return r.SoftDelete(ctx, id)
}

// ── PR 2 / Blocco 1 sub-PR (June 2026): Mutate dispatcher-only wrapper ───
//
// Mutate is the canonical single SSOT entry point for asset mutations, the
// typed-command alternative to the legacy public methods (Upsert,
// UpsertClip, UpsertClipTx, UpsertFolder, SoftDelete, ...). It collapses
// the per-action method proliferation into a single audit-friendly surface
// for future callers and is the implementation seam where future
// dispatcher-promotion can route Mutate → outbox.Dispatcher.EnqueueAndIndex
// without changing the caller's struct literal.
//
// Production callers in internal/application/** and internal/api/**
// SHOULD prefer this entry point (or the upstream
// AssetMutationDispatcher SSOT — `mutations.AssetMutationDispatcher`)
// instead of the legacy methods. The legacy methods stay public for
// adapter delegation / migration interop; CI Check 10 + the new
// dispatch-detector extension in scripts/ci-architectural-checks.sh
// continue to gate the legacy direct callers from production paths.
//
// SCOPE: WHICH ACTIONS LIVE HERE
// AssetMutationAction = AssetMutationUpsert routes through this wrapper
// to the current canonical UPSERT path. AssetMutationAction =
// AssetMutationRestore | AssetMutationDelete explicitly return
// ErrUnsupportedAction so callers cannot silently fall through to a
// plain UPSERT:
//
//   - restore: production code must use
//     AssetMutationDispatcher.EnqueueAndRestore (outbox-driven); admin
//     tooling uses txmutation.RestoreTx (caller-owned tx).
//   - delete: production code must use
//     AssetMutationDispatcher.EnqueueAndDelete (outbox-driven). Admin
//     physical-purge flows use txmutation.HardDeleteTx (caller-owned
//     tx); the regular SoftDelete route lives at deletion.DeletionService.
//
// Exhaustive-enum invariant: an Action whose IsImplemented()=true MUST
// have a switch arm in this function; otherwise the call falls into
// default and returns ErrUnsupportedAction. The unit tests in
// clips_repository_mutate_test.go hold this invariant by enumerating
// ImplementedActions and asserting each one is wired.
//
// WHY THIS IS ADDITIVE-ONLY
// The literal PR 2 spec asked to lowercase all the dispatcher-only primitive
// methods (UpsertClipTx, HardDeleteTx, RestoreTx, UpsertFolder,
// SoftDeleteFilter) and to remove the *asset.AssetStoreSQLite embedding
// from *ClipsRepository. Both those steps are blocked today:
//
//  1. UpsertClipTx is called by outbox.Dispatcher (cross-package) inside
//     its own tx-bound writer. Lowercasing it would break the canonical
//     dispatcher (build fail) — the `//nolint:production` annotation
//     on UpsertClip + UpsertClipTx is the existing syntactic gate.
//  2. HardDeleteTx / RestoreTx were ALREADY removed from the receiver
//     by PR-CLIP-RAW-MUTATIONS (Wave 22 task 5, June 2026) and live
//     exclusively in the txmutation/ package. The package-isolation
//     boundary is the shallower-surface property the spec implicitly
//     asked for.
//  3. *asset.AssetStoreSQLite embedding reflects the still-in-domain
//     SQL primitives (PR 1 deliverable was aborted in a prior turn to
//     preserve build green — internal/kernel/asset/ still owns the
//     embeddable store). Removing that embedding strictly requires
//     PR 1 / Blocco 1 to land first.
//
// This Mutate wrapper is the additive step that lets future code use
// the canonical typed-command surface without breaking any current
// caller. The follow-up PR 1 (move SQL primitives out of domain) +
// future DRIVE/EMIT-AND-INDEX wiring will switch this implementation
// to a single-tx UPSERT + outbox_events INSERT path without changing
// the caller's struct literal.
//
// Layering note: the receiver lives in
// internal/platform/sqlite/assets/ and consumes the
// mutations.AssetMutationCommand type from
// internal/application/assets/mutations/. This is the same cross-layer
// pattern documented for the existing `jobsoutbox
// "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/outbox"`
// import above — the application layer owns the canonical SSOT type
// definition, and the canonical writer infrastructure consumes it
// without inverting the general layering direction (composition root
// wires them).
func (r *ClipsRepository) Mutate(ctx context.Context, cmd mutations.AssetMutationCommand) error {
	if !cmd.Action.IsKnown() {
		return errors.Join(mutations.ErrUnsupportedAction,
			fmt.Errorf("clips.Mutate: unknown action %q", cmd.Action))
	}
	switch cmd.Action {
	case mutations.AssetMutationUpsert:
		if cmd.Asset == nil {
			return fmt.Errorf("clips.Mutate: Action=%q requires non-nil Asset", cmd.Action)
		}
		return r.Upsert(ctx, cmd.Asset)
	case mutations.AssetMutationRestore:
		// Not implemented at this layer. Production must use
		// AssetMutationDispatcher.EnqueueAndRestore; admin must use
		// txmutation.RestoreTx (caller-owned tx). Documented intent:
		// never silently fall through to plain UPSERT.
		return errors.Join(mutations.ErrUnsupportedAction,
			fmt.Errorf("clips.Mutate: Action=%q is not implemented at this layer — route via AssetMutationDispatcher.EnqueueAndRestore (production) or txmutation.RestoreTx (admin)", cmd.Action))
	case mutations.AssetMutationDelete:
		// Not implemented at this layer. Production must use
		// AssetMutationDispatcher.EnqueueAndDelete; admin must use
		// txmutation.HardDeleteTx. Documented intent: never fall
		// through silently.
		return errors.Join(mutations.ErrUnsupportedAction,
			fmt.Errorf("clips.Mutate: Action=%q is not implemented at this layer — route via AssetMutationDispatcher.EnqueueAndDelete (production) or txmutation.HardDeleteTx (admin)", cmd.Action))
	default:
		// Defensive: IsKnown() returned true but a switch arm is
		// missing (someone added a new AssetMutationAction constant
		// to mutations/command.go without wiring it here). Returns
		// ErrUnsupportedAction rather than silently falling through.
		// The unit tests in clips_repository_mutate_test.go hold the
		// exhaustive-enum invariant: every entry in
		// mutations.ImplementedActions MUST have an explicit switch
		// arm here.
		return errors.Join(mutations.ErrUnsupportedAction,
			fmt.Errorf("clips.Mutate: action %q IsImplemented()=true but switch arm is missing — regenerate ImplementedActions when adding an action arm", cmd.Action))
	}
}
