// cmd/admin/reconcile/reconcile_qdrant_adapters.go — port adapters
// extracted from reconcile_qdrant.go (PR-RECONCILE-SPLIT, July 2026).
//
// Three adapter types bridging cmd/admin/reconcile glue to the
// reconciler service ports:
//   - qdrantListerAdapter    → reconciler.QdrantLister
//   - qdrantPayloadAdapter   → reconciler.PayloadCleaner
//   - reconcileReaderAdapter → reconciler.SQLiteReader
//
// Note: the outbox adapter (formerly outboxRepairAdapter) was
// extracted to cmd/admin/internal/outbox/adapter.go in
// PR-PKG-SIZE-CMD-ADMIN-1 (July 2026) so BOTH `package main`
// admin commands (cmd/admin/backfill_*.go) AND this reconcile
// subpackage can import the same canonical adapter without a
// `package main` cross-import dependency cycle. See
// cmd/admin/internal/outbox/adapter.go for the canonical
// RepairAdapter + NewRepairAdapter + EnqueueReindex/EnqueueDelete.
package reconcile

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ── Port adapters (cmd/admin/reconcile glue) ───────────────────────────

// qdrantListerAdapter wraps transport.Client.ScrollPoints to satisfy
// reconciler.QdrantLister. The reconciler sees only PointSnapshot (no
// leak of qdrant.ScrollPoint into the application layer).
type qdrantListerAdapter struct {
	client *transport.Client
}

func (a *qdrantListerAdapter) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (reconciler.Points, error) {
	res, err := a.client.ScrollPoints(ctx, collection, offset, limit, nil)
	if err != nil {
		return reconciler.Points{}, err
	}
	out := reconciler.Points{
		NextOffset: res.NextOffset,
		Items:      make([]reconciler.PointSnapshot, len(res.Points)),
	}
	for i, p := range res.Points {
		out.Items[i] = reconciler.PointSnapshot{ID: p.ID, Payload: p.Payload}
	}
	return out, nil
}

// qdrantPayloadAdapter wraps transport.Client.DeletePayloadKeys. The
// collection is captured at construction so the reconciler call sites
// stay simple.
type qdrantPayloadAdapter struct {
	client *transport.Client
}

func (a *qdrantPayloadAdapter) DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error {
	return a.client.DeletePayloadKeys(ctx, collection, keys, pointIDs)
}

// reconcileReaderAdapter wraps indexing.SQLiteAssetStore.ListAssetsForReconcile.
type reconcileReaderAdapter struct {
	store *indexing.SQLiteAssetStore
}

func (a *reconcileReaderAdapter) ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]reconciler.AssetSnapshot, error) {
	rows, err := a.store.ListAssetsForReconcile(ctx, includeLifecycleStates)
	if err != nil {
		return nil, err
	}
	out := make([]reconciler.AssetSnapshot, len(rows))
	for i, r := range rows {
		out[i] = reconciler.AssetSnapshot{
			ID:             r.ID,
			WorkspaceID:    r.WorkspaceID,
			LifecycleState: r.LifecycleState,
			ContentHash:    r.ContentHash,
		}
	}
	return out, nil
}
