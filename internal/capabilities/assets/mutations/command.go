// Package mutations — AssetMutationCommand canonical command shape.
//
// PR 2 / Blocco 1 Asset SSOT followup (June 2026): introduces a typed
// command envelope that the new *assets.ClipsRepository.Mutate
// dispatcher-only entry point consumes. The typed command shape
// collapses the per-action method proliferation
// (Upsert + UpsertFolder + UpsertClip + UpsertClipTx + SoftDelete + ...)
// into a SINGLE audit-friendly surface. Future callers in
// internal/application/** and internal/api/** SHOULD use
// Mutate(ctx, AssetMutationCommand{}) instead of the legacy public
// methods (which remain on *assets.ClipsRepository for backward
// compatibility and adapter delegation, but are explicitly gated for
// new code by CI Check 10 / Check 10b).
//
// SCOPE (June 2026, PR 2 / Blocco 1 sub-PR)
// ----------------------------------------------------------------------------
// This sub-PR is a SAFE-ADDITIVE step. The literal PR 2 spec asked to
// lowercase all the legacy primitive methods (UpsertClipTx, HardDeleteTx,
// RestoreTx, UpsertFolder, SoftDeleteFilter) + remove the
// *asset.AssetStoreSQLite embedding from *ClipsRepository. Both those
// steps are STRUCTURALLY-BLOCKED at the time of writing:
//
//  1. upsertClipTx MUST stay public because the canonical
//     outbox.Dispatcher (internal/platform/sqlite/outbox/
//     repository.go) calls it cross-package inside the dispatcher's tx.
//     Lowercasing it would break the build.
//  2. hardDeleteTx / restoreTx are ALREADY removed from *ClipsRepository
//     and live in the restricted txmutation/ package (Wave 22 task 5 /
//     PR-CLIP-RAW-MUTATIONS, June 2026). They are no longer on the
//     receiver — the "limiting-surface" goal is already achieved for
//     those two via package isolation, not via lowercase visibility.
//  3. upsertFolder / SoftDeleteFilter live on the embedded
//     *asset.AssetStoreSQLite. Removing that embedding is PR 1's
//     deliverable (move SQL primitives out of internal/kernel/asset/).
//     PR 1 was aborted in a prior turn to preserve build green; the
//     domain still hosts the embedded SQLite primitives, so the
//     embedding structurally cannot be removed until PR 1 lands.
//
// PR 2 / Blocco 1 sub-PR therefore SHIPS:
//   - A typed AssetMutationCommand + AssetMutationAction (this file) —
//     pure types, zero runtime cost, future-call site shape. Only
//     AssetMutationUpsert is implemented at the *ClipsRepository.Mutate
//     layer today; restore / delete return ErrUnsupportedAction and
//     delegate callers to AssetMutationDispatcher / txmutation (the
//     canonical paths).
//   - A *assets.ClipsRepository.Mutate wrapper (clips_repository.go,
//     additive only) — internally dispatches to the existing legitimate
//     Upsert path. New code can use it; old code keeps working.
//   - CI Check 10b in scripts/ci-architectural-checks.sh that catches
//     NEW direct callers of r.UpsertFolder / r.SoftDeleteFilter outside
//     the canonical allowlist.
//   - docs/migrations/bypass_audit_2026-06-27.md refresh so the obsolete
//     "clips_repository.go:336 residue" claim is corrected.
//
// Removed during code-review (June 2026): the AssetMutationIndex action
// was originally proposed as a forward-canonical seam for the future
// outbox-driven UPSERT+outbox_event path. Code review flagged it as
// misleading (today it's an alias for AssetMutationUpsert, so picking
// "index" would not deliver the outbox-event emit the name implies and
// would silently diverge from production expectation). It is REMOVED
// from this PR until the future dispatcher-promotion lands.
package mutations

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrUnsupportedAction is the canonical sentinel returned by the
// canonical *assets.ClipsRepository.Mutate entry point when it
// receives an AssetMutationCommand whose Action is not in the
// implemented-set. Use errors.Is for compatibility.
var ErrUnsupportedAction = errors.New("mutations: unsupported AssetMutationAction")

// AssetMutationAction is the typed enum of canonical asset-mutation
// actions the *assets.ClipsRepository.Mutate entry point accepts.
//
// Two layers of "implemented" semantics live here:
//   - AssetMutationUpsert IS implemented today: the canonical wrapper
//     delegates to UPSERT-only (the outbox-driven canonical path is
//     still AssetMutationDispatcher.EnqueueAndIndex at a higher layer).
//   - AssetMutationRestore / AssetMutationDelete return
//     ErrUnsupportedAction at the *ClipsRepository.Mutate layer — they
//     are intentionally NOT implemented here. Production code paths
//     for those actions route through AssetMutationDispatcher.
//     EnqueueAnd{Restore,Delete} (outbox-driven) or txmutation.
//     RestoreTx / txmutation.HardDeleteTx (admin-only tx-scoped).
//
// The enum is an alias for string to keep Go zero-value safe (the
// empty AssetMutationAction value is rejected by the dispatcher's
// switch) and to allow future additions without a parallel interface
// growth. Each new constant MUST be added in lockstep with a switch
// arm in *assets.ClipsRepository.Mutate (the unit tests in
// clips_repository_mutate_test.go exhaustively assert this invariant).
type AssetMutationAction string

const (
	// AssetMutationUpsert is the plain UPSERT-only path. Equivalent to
	// *assets.ClipsRepository.Upsert today; future PRs may rewrite it to
	// delegate to the canonical outbox-driven EnqueueAndIndex once
	// dispatcher-promotion lands across the call sites.
	AssetMutationUpsert AssetMutationAction = "upsert"

	// AssetMutationRestore is the outbox-driven restore envelope;
	// not implemented at the *ClipsRepository.Mutate wrapper today.
	// Production code paths route through
	// AssetMutationDispatcher.EnqueueAndRestore; admin tooling routes
	// through txmutation.RestoreTx. Mutate returns ErrUnsupportedAction.
	AssetMutationRestore AssetMutationAction = "restore"

	// AssetMutationDelete is the outbox-driven delete envelope; not
	// implemented at the *ClipsRepository.Mutate wrapper today.
	// Production code paths route through
	// AssetMutationDispatcher.EnqueueAndDelete; admin tooling routes
	// through txmutation.HardDeleteTx for physical-purge flows.
	AssetMutationDelete AssetMutationAction = "delete"
)

// ImplementedActions is the canonical list of AssetMutationAction
// values for which *assets.ClipsRepository.Mutate has a defined
// dispatch arm. The exhaustive-enum contract between IsKnown() and
// Mutate's switch arms is held by the unit tests in
// clips_repository_mutate_test.go; adding an action value to this
// file MUST add a switch arm in Mutate in the same commit.
var ImplementedActions = []AssetMutationAction{
	AssetMutationUpsert,
	// Restore + Delete return ErrUnsupportedAction; they are NOT
	// listed here because they have no dispatch arm.
}

// IsKnown reports whether the AssetMutationAction is a known enum
// value (whether or not the canonical Mutate wrapper has a defined
// dispatch arm). Use this for caller-side validation BEFORE sending
// a command across the network / serialisation boundary so receivers
// can log unknown actions explicitly. The IsImplemented method (below)
// is the right predicate for callers that want to filter at the
// "what does this layer actually do" level.
func (a AssetMutationAction) IsKnown() bool {
	switch a {
	case AssetMutationUpsert,
		AssetMutationRestore, AssetMutationDelete:
		return true
	}
	return false
}

// IsImplemented reports whether the canonical *assets.ClipsRepository
// Mutate wrapper has a defined dispatch arm for this action. Differs
// from IsKnown: a value can be IsKnown=true (a recognised enum value)
// but IsImplemented=false (the wrapper intentionally returns
// ErrUnsupportedAction rather than silently falling through).
//
// Callers SHOULD filter at IsImplemented for ergonomics (skip the
// Mutate call rather than handle ErrUnsupportedAction at every site)
// and SHOULD pair that with errors.Is(err, ErrUnsupportedAction) at
// the catch site as a defence-in-depth check.
func (a AssetMutationAction) IsImplemented() bool {
	switch a {
	case AssetMutationUpsert:
		return true
	}
	return false
}

// AssetMutationCommand is the typed command envelope accepted by the
// canonical *assets.ClipsRepository.Mutate(ctx, ...) dispatcher-only
// entry point (PR 2 / Blocco 1 sub-PR, June 2026). Action selects
// the dispatch arm; Asset / ID / Hash are the per-action inputs.
//
// Field semantics:
//   - Action: REQUIRED. Empty string returns ErrUnsupportedAction from
//     Mutate; an Action whose IsKnown()=false returns ErrUnsupportedAction;
//     an Action whose IsImplemented()=false (currently restore / delete)
//     returns ErrUnsupportedAction with a routing message pointing at
//     AssetMutationDispatcher or txmutation/.
//   - Asset: REQUIRED for Action = AssetMutationUpsert. Ignored for
//     other actions. The canonical domain type is *asset.Asset so Mutate
//     can be called without an internal repo-internal struct.
//   - ID: REQUIRED for Action in {AssetMutationRestore, AssetMutationDelete}.
//     Ignored for Action = AssetMutationUpsert. The string is the
//     canonical media_assets.id UUID.
//   - Hash: future-canonical seam for Action = AssetMutationUpsert once
//     dispatcher-promotion lands (the content fingerprint the worker's
//     supersede-gate compares against media_assets.metadata_json.
//     $.content_hash, per QDRANT-002 source_version pattern). Ignored
//     today.
//
// Callers MUST construct an AssetMutationCommand value via the
// field-default zero value: AssetMutationCommand{} with the relevant
// fields populated explicitly. Positional constructors / builders
// are intentionally NOT provided because the four-field surface is
// already minimal enough that a literal struct literal is readable.
type AssetMutationCommand struct {
	Action AssetMutationAction
	Asset  *asset.Asset
	ID     string
	Hash   string
}
