// Package assets — canonical SQL primitives for media_assets queries.
//
// Wave A / Blocco 1 / PR 1 Asset SSOT (June 2026): moved from
// internal/kernel/asset/store_helpers.go to enforce the layering
// rule that domain must not own SQL primitives. As an INFRA package
// (internal/infrastructure/...) this file may freely import
// database/sql — the AGENTS.md Pattern 8 ban targets internal/api/**
// only.
//
// The slim domain/store_helpers.go keeps a back-compat wrapper for
// SoftDeleteFilter / buildMediaAssetQuery / buildClipFolderQuery /
// clipSearchColumns so the 71+ existing method receivers in OTHER
// domain files (clips_core.go, search_core.go, etc.) continue to
// compile without per-file migration.
package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// ── SQL projection constants (canonical home is clips_repository.go) ─────
//
// PR 1 (June 2026, Lifecycle state SSOT): the legacy `status` column
// is RETIRED — `MediaAssetColumns` const is the SSOT in
// `clips_repository.go` (was duplicated here in Wave A scaffold; the
// duplicate caused a Go compile error in Phase 3 because the same
// const name was declared in two files of the same package). This
// file references `MediaAssetColumns` (from clips_repository.go of the
// same package) directly — same-package visibility, no import.
//
// Domain mirror: `internal/kernel/asset/store_helpers.go::localMediaAssetColumns`
// is a SEPARATE verbatim duplicate kept so the domain file compiles
// without a `database/sql` import. Phase 4 unification should lift
// this duplication by exporting an `assets.MediaAssetColumns` getter
// from infra AND replacing the domain mirror — the slim domain file
// will go back to zero SQL strings inside package asset.

const ClipFolderColumns = `id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at`

// SoftDeleteFilter delegates to the canonical asset.SoftDeleteFilter (PR 1 Lifecycle state SSOT; Phase 4 unification thin wrapper).
func SoftDeleteFilter() string {
	return asset.SoftDeleteFilter()
}

// buildMediaAssetQuery constructs a SELECT query for the canonical
// media_assets row projection.
// buildMediaAssetQuery constructs a SELECT query for the canonical
// media_assets row projection. The projection lives in
// `clips_repository.go` (canonical) — same-package visibility.
func buildMediaAssetQuery(source string) string {
	q := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter()
	if source != "" && source != "all" && source != "unified" {
		q += " AND source = ?"
	}
	return q
}

// buildClipFolderQuery constructs a SELECT query for the canonical
// clip_folders projection.
func buildClipFolderQuery(source string) string {
	q := "SELECT " + ClipFolderColumns + " FROM clip_folders"
	if source != "" && source != "all" && source != "unified" {
		q += " WHERE source = ?"
	}
	return q
}

// clipSearchColumns returns the columns used by the LIKE-fallback
// search path.
func clipSearchColumns() []string {
	return []string{
		"tags",
		"name",
		"search_text",
		"json_extract(COALESCE(metadata_json,'{}'), '$.description')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clean_title')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_summary')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.hook')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.topics')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.speakers')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.mentioned_people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_tags')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.search_keywords')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.embedding_text')",
	}
}

// ── Receiver methods (4 moved from domain) ──────────────────────────

// GetFolderChildren returns non-deleted assets whose
// parent_folder_id matches the supplied parentID.
func (s *AssetStoreSQLite) GetFolderChildren(ctx context.Context, parentID string) ([]*asset.Asset, error) {
	q := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter() + " AND parent_folder_id = ? ORDER BY name ASC"
	rows, err := s.db.QueryContext(ctx, q, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*asset.Asset, 0)
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			s.log.Error("failed to scan clip in GetFolderChildren", zap.Error(err))
			continue
		}
		out = append(out, clip)
	}
	return out, rows.Err()
}

// FindByPHash searches for an asset with the given perceptual hash
// (canonical column after migration 059). Returns the asset id if
// found, "" if not.
func (s *AssetStoreSQLite) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	q := "SELECT id FROM media_assets WHERE phash = ? AND " + SoftDeleteFilter() + " LIMIT 1"
	err := s.db.QueryRowContext(ctx, q, phash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByPHash: %w", err)
	}
	return id, nil
}

// MarkUsed marks a clip as used (canonical migration 059 columns).
func (s *AssetStoreSQLite) MarkUsed(ctx context.Context, clipID string) error {
	if clipID == "" {
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET reuse_count = reuse_count + 1,
		    last_used_at = ?
		WHERE id = ?
	`, now, clipID)
	return err
}

// MarkClipsUsed marks multiple clips as used.
func (s *AssetStoreSQLite) MarkClipsUsed(ctx context.Context, clipIDs []string) error {
	for _, id := range clipIDs {
		if err := s.MarkUsed(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
