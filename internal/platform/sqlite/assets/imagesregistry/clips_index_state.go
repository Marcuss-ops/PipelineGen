package imagesregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_index_state.go holds the *ClipsRepository methods that
// transition media_assets into lifecycle / indexing states. SoftDelete
// flips into 'deleted' (tombstone); SetIndexState writes the
// canonical index_state column (QDRANT-002 PR6 / migration 094);
// DeleteClipByDriveLink performs the legacy drive-link-based
// delete-by-link soft-delete (QDRANT-002 outbox-bypass note). The
// dispatcher (outbox.Dispatcher) is the canonical caller of the tx-scoped
// SetIndexStateTx mirror (in clips_transactions.go) and the production-grade
// deletion path.

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

// GetIndexState reads the canonical media_assets.index_state column
// (migration 094). Returns StateDiscovered when the row is missing or
// the column is empty (the migration DEFAULT sentinel) so producers can
// branch on "already indexed" without error-handling ceremony.
//
// Index-state reads are diagnostic only. They must not suppress a new
// index intent: SQLite INDEXED does not prove that the active Qdrant
// projection contains the asset. Exact retries are deduplicated by the
// canonical outbox event key.
func (r *ClipsRepository) GetIndexState(ctx context.Context, id string) (asset.IndexState, error) {
	if id == "" {
		return asset.StateDiscovered, fmt.Errorf("clips.GetIndexState: id is required")
	}
	var state string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(index_state, '') FROM media_assets WHERE id = ?`, id,
	).Scan(&state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return asset.StateDiscovered, nil
		}
		return asset.StateDiscovered, fmt.Errorf("clips.GetIndexState(%s): %w", id, err)
	}
	if state == "" {
		return asset.StateDiscovered, nil
	}
	return asset.IndexState(state), nil
}

// DeleteClipByDriveLink soft-deletes by drive/download link.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. It flips lifecycle_state
// to 'deleted' without emitting an asset.index.delete_requested event,
// which means the Qdrant point is never cleaned up.
//
// Callers should use deletion.DeletionService.DeleteClip (which routes
// through outbox.Dispatcher.EnqueueAndDelete) or call the dispatcher
// directly.
func (r *ClipsRepository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}
	// Fail closed: deleting by locator cannot emit the canonical
	// asset.index.delete_requested outbox event atomically. Callers must use
	// DeletionService/Dispatcher with the asset ID instead.
	return fmt.Errorf("DeleteClipByDriveLink is disabled: use the canonical Dispatcher deletion path")
}
