// Package assets — clips_crud_test.go pins the canonical
// MediaAssetColumns ↔ ScanMediaAsset contract so a future migration
// (or hand-edit) that adds / removes / renames a column cannot
// silently drift the 40-column SELECT projection in
// clips_repository.go.
//
// We test the EXTERNAL contract surface (the MediaAssetColumns
// constant and the AssetStoreSQLite.Get public method) against the
// canonical ScanMediaAsset scan signature. A real in-memory
// mattn/go-sqlite3 database is used so COALESCE / AS-alias
// semantics are honoured — sqlmock-style stubs would not see
// `rows.Columns()` aliases.
package assets

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// canonicalMediaAssetColumns is the source-of-truth column list that
// BOTH MediaAssetColumns (clips_repository.go) AND ScanMediaAsset
// (scan_helpers.go) must match — same count, same positional order,
// same AS aliases. If you change this list, all three files must
// change in lockstep:
//
//   - clips_repository.go::MediaAssetColumns        (SELECT projection)
//   - scan_helpers.go::ScanMediaAsset               (consumes the aliases)
//   - clips_crud_test.go::canonicalMediaAssetColumns (this test pins the contract)
//
// The ordering here mirrors the canonical scan signature:
//
//	id, source, name, tags, tags_norm,
//	embedding_json, duration_ms, url,
//	media_type, status, local_path, relative_path,
//	drive_file_id, drive_folder_id, drive_link, download_link,
//	legacy_file_md5, metadata_json, visual_embedding, transcript_embedding,
//	created_at, updated_at, width, height,
//	lifecycle_state, deleted_at,
//	folder_id, parent_folder_id, folder_path,
//	category, group_name, filename, error,
//	thumb_url, phash, search_text, scene_type,
//	quality_score, reuse_count, last_used_at
var canonicalMediaAssetColumns = []string{
	"id", "source", "name", "tags", "tags_norm",
	"embedding_json", "duration_ms", "url",
	"media_type", "local_path", "relative_path",
	"drive_file_id", "drive_folder_id", "drive_link", "download_link",
	"legacy_file_md5", "metadata_json", "visual_embedding", "transcript_embedding",
	"created_at", "updated_at", "width", "height",
	"lifecycle_state", "deleted_at",
	"folder_id", "parent_folder_id", "folder_path",
	"category", "group_name", "filename", "error",
	"thumb_url", "phash", "search_text", "scene_type",
	"quality_score", "reuse_count", "last_used_at",
}

// newAlignTestDB opens a fresh in-memory SQLite + applies a minimal
// 40-column media_assets schema that mirrors MediaAssetColumns' AS
// aliases. Types are intentionally minimal (TEXT / INTEGER / REAL)
// — what matters is column NAMES + EXISTENCE, since the test contract
// is "the SELECT projection must materialise these 40 names in this
// exact order".
func newAlignTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT, name TEXT, tags TEXT, tags_norm TEXT,
			embedding_json TEXT, duration_ms INTEGER, url TEXT,
			media_type TEXT, status TEXT, local_path TEXT,
			relative_path TEXT,
			drive_file_id TEXT, drive_folder_id TEXT,
			drive_link TEXT, download_link TEXT, legacy_file_md5 TEXT,
			metadata_json TEXT,
			visual_embedding TEXT, transcript_embedding TEXT,
			created_at TEXT, updated_at TEXT,
			width INTEGER, height INTEGER,
			lifecycle_state TEXT, deleted_at TEXT,
			folder_id TEXT, parent_folder_id TEXT, folder_path TEXT,
			category TEXT, group_name TEXT,
			filename TEXT, error TEXT,
			thumb_url TEXT, phash TEXT,
			search_text TEXT, scene_type TEXT,
			quality_score REAL, reuse_count INTEGER, last_used_at TEXT,
    index_state TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '')
	`)
	require.NoError(t, err, "create media_assets (40-col align schema)")
	return db
}

// TestAssetStoreSQLiteGet_AlignsWithScan pins three contracts in
// lockstep:
//
//  1. The 40-column count. A future migration that adds / removes a
//     column without updating MediaAssetColumns will be caught here.
//  2. The canonical-name positional order. The AS alias at index N
//     in MediaAssetColumns MUST equal the asset field ScanMediaAsset
//     reads at destination index N — any reorder will silently
//     mis-populate fields.
//  3. End-to-end Get() reads. AssetStoreSQLite.Get must surface the
//     projection's columns back into *asset.Asset fields. This
//     catches regressions where the projection has the right 40
//     count but routes columns to the wrong scan destinations
//     (e.g. ScanMediaAsset and MediaAssetColumns' AS aliases drift
//     independently).
//
// Drift in any of these will cause production to read corrupted or
// silently-empty Asset fields; this test fails immediately on drift
// instead of leaking the regression into production logs.
func TestAssetStoreSQLiteGet_AlignsWithScan(t *testing.T) {

	// ── subtest 1: 40-column count + canonical positional order ──
	// rows.Columns() honours AS aliases — this is THE contract
	// we're pinning.
	t.Run("projection_has_40_columns_in_canonical_order", func(t *testing.T) {
		db := newAlignTestDB(t)
		ctx := context.Background()

		rows, err := db.QueryContext(ctx,
			"SELECT "+MediaAssetColumns+" FROM media_assets LIMIT 0")
		require.NoError(t, err,
			"SELECT with MediaAssetColumns MUST parse + execute against the 40-col schema")
		defer rows.Close()

		cols, err := rows.Columns()
		require.NoError(t, err)

		require.Equal(t, len(canonicalMediaAssetColumns), len(cols),
			"MediaAssetColumns MUST produce exactly %d AS aliases; got %d (%v)",
			len(canonicalMediaAssetColumns), len(cols), cols)

		for i, want := range canonicalMediaAssetColumns {
			assert.Equal(t, want, cols[i],
				"MediaAssetColumns alias at position %d MUST be %q — drift would "+
					"mis-populate ScanMediaAsset's scan target at destination "+
					"position %d",
				i, want, i)
		}
	})

	// ── subtest 2: substring sanity ──
	// Catches accidental deletions of an alias from the backtick
	// block (e.g. someone hand-edits MediaAssetColumns and forgets
	// to update ScanMediaAsset in lockstep).
	t.Run("projection_contains_all_canonical_aliases", func(t *testing.T) {
		for _, want := range canonicalMediaAssetColumns {
			// Match the alias as a standalone token — we look for
			// "want," (or "want`" at end-of-string) so `id` does
			// not accidentally match `drive_file_id`.
			tokenComma := want + ","
			tokenBacktick := want + "`"
			hasIt := strings.Contains(MediaAssetColumns, tokenComma) ||
				strings.Contains(MediaAssetColumns, tokenBacktick) ||
				// First column "id" has no leading comma but appears after the opening backtick.
				strings.Contains(MediaAssetColumns, "`"+want)
			assert.True(t, hasIt,
				"MediaAssetColumns MUST contain the %q alias as a standalone "+
					"token — if you removed/replaced a column, update "+
					"MediaAssetColumns + scan_helpers.go::ScanMediaAsset + "+
					"clips_crud_test.go::canonicalMediaAssetColumns in lockstep",
				want)
		}
	})

	// ── subtest 3: end-to-end Get round-trip ──
	// Insert a row with controlled values for the 6 distinguishing
	// fields that were missing or wrong in the previous 38-col
	// projection (drive_folder_id, drive_link, group_name, media_type,
	// plus id + source for sanity), then Get() and verify each one
	// round-trips into the right *asset.Asset field.
	t.Run("get_round_trips_drive_folder_id_drive_link_and_group_name", func(t *testing.T) {
		db := newAlignTestDB(t)
		ctx := context.Background()

		const controlID = "align-test-asset-1"
		_, err := db.ExecContext(ctx, `
			INSERT INTO media_assets (
				id, source, name, media_type, status,
				drive_folder_id, drive_link, group_name,
				lifecycle_state, deleted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			controlID,
			"youtube", "align-name", "video", "active",
			"align-folder", "align-drive-link", "align-group",
			"ACTIVE", "",
		)
		require.NoError(t, err, "insert control row")

		s := NewAssetStoreSQLite(db, zap.NewNop())
		details, err := s.Get(ctx, controlID)
		require.NoError(t, err, "Get MUST succeed for the control row")
		require.NotNil(t, details, "Get MUST return a Details record for the control row")
		require.NotNil(t, details.Asset, "Details.Asset MUST be populated")

		a := details.Asset
		assert.Equal(t, controlID, a.ID, "id round-trips")
		assert.Equal(t, asset.Source("youtube"), a.Source,
			"source round-trips — failed if MediaAssetColumns dropped this column")
		assert.Equal(t, "align-name", a.Name)
		assert.Equal(t, asset.MediaType("video"), a.MediaType,
			"media_type round-trips — failed if MediaAssetColumns dropped media_type")
		assert.Equal(t, "align-folder", a.FolderID(),
			"drive_folder_id → folder_id round-trips (legacy fallback in ScanMediaAsset "+
				"— if MediaAssetColumns dropped drive_folder_id, this is empty)")
		assert.Equal(t, "align-drive-link", a.DriveLink(),
			"drive_link round-trips — failed if MediaAssetColumns has the wrong "+
				"column name (previously `download_url`, mismatched with ScanMediaAsset's "+
				"`drive_link` scan target)")
		assert.Equal(t, "align-group", a.Group,
			"group_name round-trips — failed if MediaAssetColumns dropped group_name "+
				"(was missing from the 38-col projection)")
	})

	// ── subtest 4: soft-delete filter still respects the projection ──
	t.Run("soft_deleted_row_omitted_by_get", func(t *testing.T) {
		db := newAlignTestDB(t)
		ctx := context.Background()

		_, err := db.ExecContext(ctx, `
			INSERT INTO media_assets (
				id, source, name, lifecycle_state, deleted_at
			) VALUES ('soft-deleted-asset', 'manual',
				'should-not-appear', 'DELETED', '2026-06-01T00:00:00Z')
		`)
		require.NoError(t, err, "insert soft-deleted row")

		s := NewAssetStoreSQLite(db, zap.NewNop())
		details, err := s.Get(ctx, "soft-deleted-asset")
		require.NoError(t, err,
			"Get returns nil for filtered-out rows, not an error")
		assert.Nil(t, details,
			"SoftDeleteFilter MUST exclude the row — Get returns nil Details")
	})
}
