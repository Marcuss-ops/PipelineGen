// Package indexing — asset_store_reconcile.go owns the RECONCILE
// DRY-RUN flow of SQLiteAssetStore: ListAssetsForReconcile (the
// minimum payload scanner for the admin-side
// cmd/admin/reconcile_qdrant.go CLI's reconcile dry-run).
//
// Split rationale (operation-flow): see asset_store.go header.
//
// godlike/06 SSOT: this is the canonical read seam for the
// reconciliation dry-run; the result feeds the reconciler service
// (`internal/application/qdrant/reconciler`) which in turn batches
// against Qdrant. AssetStore is NOT the orchestration seam — it
// ships the minimum scan payload and lets the reconciler do
// in-memory batching.
//
// The query is a full scan (no pagination) because the reconciler
// has already dialed down the asset set to "all eligible rows";
// paginating here would force a second pass through the same DB on
// every dry-run. HIGH #8 pagination applies to the production
// reindex pipeline (FetchAssetBatch, in asset_store_batch.go), NOT
// to the operator-driven reconcile.
//
// godlike/07 minimum-blast-radius: the SELECT emits a COALESCE
// fallback to 'ACTIVE' for lifecycle_state so legacy data without
// the column (or with the retired lowercase `status`) doesn't crash
// the dry-run.
package indexing

import (
	"context"
	"database/sql"
	"fmt"
)

// ListAssetsForReconcile returns the minimum asset payload needed by the
// admin-side `cmd/admin/reconcile_qdrant.go` reconcile dry-run.
//
// Implements the SQL scan (June 2026 follow-up to QDRANT-005
// closure): selects id + workspace_id + lifecycle_state (with a
// COALESCE fallback through 'ACTIVE') + content_hash
// (extracted from metadata_json) from media_assets. Filters out
// folders and soft-deleted rows by default, and optionally
// restricts to a set of lifecycle states supplied by the caller.
// The reconciler service handles in-memory batching against Qdrant,
// so this query is a full scan of the eligible rows ordered by id
// for deterministic output.
//
// godlike/06 SSOT: this is the canonical read seam for the
// reconciliation dry-run. It is distinct from FetchAsset (which
// returns the FULL AssetData projection for the production reindex
// pipeline) on purpose — the reconciler needs only the four
// payload-relevant fields (id, workspace_id, lifecycle_state,
// content_hash).
//
// includeLifecycleStates is optional: when empty, no extra filter is
// applied and ALL eligible rows are returned.
func (s *SQLiteAssetStore) ListAssetsForReconcile(ctx context.Context, includeLifecycleStates []string) ([]AssetData, error) {
	query := `
		SELECT
			id,
			COALESCE(workspace_id, ''),
			COALESCE(lifecycle_state, 'ACTIVE'),
			COALESCE(json_extract(metadata_json, '$.content_hash'), '')
		FROM media_assets
		WHERE ` + indexableAssetWhereClause + `
	`

	var args []any
	if len(includeLifecycleStates) > 0 {
		query += ` AND COALESCE(lifecycle_state, 'ACTIVE') IN (`
		for i, state := range includeLifecycleStates {
			if i > 0 {
				query += ", "
			}
			query += "?"
			args = append(args, state)
		}
		query += `)`
	}

	query += ` ORDER BY id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query media_assets for reconcile: %w", err)
	}
	defer rows.Close()

	var out []AssetData
	for rows.Next() {
		var a AssetData
		var wsID, contentHash sql.NullString
		if err := rows.Scan(&a.ID, &wsID, &a.LifecycleState, &contentHash); err != nil {
			return nil, fmt.Errorf("scan asset for reconcile: %w", err)
		}
		if wsID.Valid {
			a.WorkspaceID = wsID.String
		}
		if contentHash.Valid {
			a.ContentHash = contentHash.String
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media_assets for reconcile: %w", err)
	}
	return out, nil
}
