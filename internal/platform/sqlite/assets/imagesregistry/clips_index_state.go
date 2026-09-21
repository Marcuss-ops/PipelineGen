package imagesregistry

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_index_state.go holds the *ClipsRepository methods that
// transition media_assets into lifecycle / indexing states. SoftDelete
// flips into 'deleted' (tombstone); SetIndexState writes the
// canonical index_state column (QDRANT-002 PR6 / migration 094). The
// dispatcher (outbox.Dispatcher) is the canonical caller of the tx-scoped
// SetIndexStateTx mirror (in clips_transactions.go) and the production-grade
// deletion path.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21): DeleteClipByDriveLink is
// DELETED. It had zero production and zero test call sites and had already
// been reduced to a fail-closed stub that returned an error instead of doing
// the outbox-bypassing soft-delete, so it was a dead door with no behaviour
// left to preserve (the canonical path is Dispatcher/
// DeletionService by asset ID).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21, sub-wave B): GetIndexState is
// DELETED. It was this file's only media READ and it had zero production call
// sites — the production read resolves on the media SSOT through the
// postgresCatalogRepository router (pgmedia.MediaSearcher), and the only
// remaining consumer shape, the catalogsync degrade branch, now answers the
// index-state slot with noMediaPlaneIndexState instead of reading
// media_assets.index_state off the operational mirror (which holds no committed
// media rows). What remains here are the two terminal WRITES — SoftDelete and
// SetIndexState — so this file left the sqliteMediaReaderInventoriedZoneFiles
// register in the same change.

func (r *ClipsRepository) SoftDelete(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	return UpdateMediaAssetLifecycle(ctx, r.db, id, string(asset.StateDeleted), nowStr, nowStr)
}

// SetIndexState writes the canonical media_assets.index_state column
// (QDRANT-002 PR6 / migration 094). Called by IndexDeleteHandler for
// the DELETE_PENDING and DELETED transitions; the Delete path is the
// only consumer in production today, but the method is exposed as
// public because future worker bootstrap or operator tooling may
// need to flip state directly (QDRANT-005 alerting followup).
//
// No lifecycle_state filter — the caller is responsible for picking
// the right state at the right time. SoftDeleteFilter() is applied
// by callers that need to exclude tombstoned rows (e.g. live
// re-index tooling); IndexDeleteHandler does NOT need it because the
// pre-flight already short-circuits to success on lifecycle_state in
// {deleted, DELETED}.
//
// Idempotent: the column flip on an already-target-state row is a
// no-op write; the lease-fence on the outbox handler prevents the
// same worker from racing itself.
func (r *ClipsRepository) SetIndexState(ctx context.Context, id string, state asset.IndexState) error {
	if id == "" {
		return fmt.Errorf("clips.SetIndexState: id is required")
	}
	if state == "" {
		return fmt.Errorf("clips.SetIndexState: state is required (got empty string; use the canonical 7-state enum)")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	err := UpdateMediaAssetIndexState(ctx, r.db, id, string(state), nowStr, "")
	if err != nil {
		return fmt.Errorf("clips.SetIndexState(%s, %s): %w", id, state, err)
	}
	return nil
}
