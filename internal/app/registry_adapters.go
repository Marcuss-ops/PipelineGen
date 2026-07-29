// Package app — composition-root adapter definitions.
//
// Per AGENTS.md Pattern 0 (June 2026): composition root owns thin
// adapter layers between the canonical application interfaces and
// the concrete infrastructure types. List each adapter family in a
// separate section below; keep the file focused on adapter wiring,
// not on business logic.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Wave 22 task 1 of 5: AssetMutationDispatcher adapter ───────────────────────
//
// mutationsDispatcherAdapter wraps the canonical *outbox.Dispatcher so the
// application layer can depend on the SSOT mutations.AssetMutationDispatcher
// surface (rather than the 9+ ad-hoc narrow dispatcher ports the codebase has
// accumulated over time). The adapter is a thin delegation layer — no
// business logic, no SQL, no outbox_event payload synthesis. The dispatcher
// does all of that.
//
// Compile-time assertion placement rationale (Pattern 0):
//
//	The dispatcher (in internal/infrastructure/...) SHOULD NOT import
//	mutations (in internal/application/...) directly — that would violate
//	the canonical application ← infrastructure layering. Instead, the
//	assertion lives HERE in the composition root, which is the ONE place
//	allowed to import both sides. Drift in either the interface or the
//	dispatcher's tx-bound implementation surfaces as a build error in
//	the composition root, not as a runtime panic.
//
// Signature-drift detection: break any of the 3 methods' signatures
// (e.g. change `contentHash string` to `contentHash int`) and observe
// the build fail at this compile-time assertion. That is the
// verification gate the user spec called out.
type mutationsDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// var _ mutations.AssetMutationDispatcher = (*mutationsDispatcherAdapter)(nil)
//
// is the canonical compile-time assertion. Per AGENTS.md Pattern 0 / 8
// (compile-time over runtime checks), the assertion lives at the adapter
// home rather than the dispatcher home to preserve layering.
var _ mutations.AssetMutationDispatcher = (*mutationsDispatcherAdapter)(nil)

// newMutationsDispatcherAdapter wraps a non-nil dispatcher. Returns
// an explicit error when disp is nil so the wiring at the composition
// root surfaces misconfiguration loudly (mirror of artlist's typed-
// sentinel fail-closed convention).
func newMutationsDispatcherAdapter(disp *outbox.Dispatcher) (mutations.AssetMutationDispatcher, error) {
	if disp == nil {
		return nil, fmt.Errorf("app.newMutationsDispatcherAdapter: outbox.Dispatcher is required (QDRANT-asset-mutation isolation, Wave 22 task 1; composition root must wire the canonical *outbox.Dispatcher)")
	}
	return &mutationsDispatcherAdapter{disp: disp}, nil
}

// EnqueueAndIndex delegates to the dispatcher's tx-bound UPSERT +
// outbox enqueue. No per-call mutation (the interface is the SSOT;
// the dispatcher owns the SQL + envelope + state stamp).
func (a *mutationsDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

// EnqueueAndRestore delegates to the dispatcher's tx-bound state
// stamp + outbox enqueue (event_type='asset.index.restore_requested').
// Handler lands in task 3 of 5.
func (a *mutationsDispatcherAdapter) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndRestore(ctx, assetID)
}

// EnqueueAndDelete delegates to the dispatcher's tx-bound DELETE_PENDING
// state stamp + outbox enqueue (event_type='asset.index.delete_requested').
// Handler already exists from QDRANT-002 PR7.
func (a *mutationsDispatcherAdapter) EnqueueAndDelete(ctx context.Context, assetID string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndDelete(ctx, assetID)
}
