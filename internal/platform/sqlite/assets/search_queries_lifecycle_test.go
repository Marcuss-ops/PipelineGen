// Package assets — search_queries_lifecycle_test.go pins the
// PR-QDRANT-SEARCH-LIFECYCLE-FILTER contract: search functions
// (SearchClipsAdvanced, SearchClipsByKeywords, SearchStockByKeywords)
// MUST exclude assets whose lifecycle_state is NOT in {ACTIVE, PUBLISHED}.
//
// The previous bug (T5): search used SoftDeleteFilter() which only
// excluded terminal DELETED. Assets in DELETE_REQUESTED,
// DRIVE_DELETE_PENDING, DRIVE_DELETED, INDEX_DELETE_PENDING,
// INDEX_DELETED, PREPARING, STAGING, PROCESSING, ERROR all leaked
// into search results.
//
// godlike/06 SSOT: SearchableLifecycleFilter() is the canonical
// SQL fragment; these tests pin the contract at the SQL receiver layer.
package assets

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// newLifecycleTestDB creates an in-memory SQLite with the media_assets
// schema and pre-populates 3 rows: ACTIVE, PUBLISHED, DELETE_REQUESTED.
func newLifecycleTestDB(t *testing.T) *sql.DB {
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
	require.NoError(t, err, "create media_assets")

	// Seed 3 rows: ACTIVE, PUBLISHED, DELETE_REQUESTED
	seeds := []struct {
		id, lifecycle, name string
	}{
		{"lifecycle-active-1", "ACTIVE", "boxing highlight"},
		{"lifecycle-published-1", "PUBLISHED", "boxing training"},
		{"lifecycle-deleted-req-1", "DELETE_REQUESTED", "boxing should not appear"},
	}
	for _, s := range seeds {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO media_assets (id, source, name, lifecycle_state, media_type, search_text)
			 VALUES (?, 'youtube', ?, ?, 'video', ?)`,
			s.id, s.name, s.lifecycle, s.name,
		)
		require.NoError(t, err, "seed %s", s.id)
	}
	return db
}

// TestSearchableLifecycleFilter_ReturnsCorrectFragment pins the
// canonical SQL fragment contract.
func TestSearchableLifecycleFilter_ReturnsCorrectFragment(t *testing.T) {
	got := SearchableLifecycleFilter()
	assert.Equal(t, "lifecycle_state IN ('ACTIVE', 'PUBLISHED')", got,
		"SearchableLifecycleFilter must match the canonical SearchableLifecycleStates allowlist")
}

// TestSearchClipsAdvanced_ExcludesDeleteRequested proves that
// SearchClipsAdvanced excludes DELETE_REQUESTED assets from results.
func TestSearchClipsAdvanced_ExcludesDeleteRequested(t *testing.T) {
	db := newLifecycleTestDB(t)
	s := NewAssetStoreSQLite(db, zap.NewNop())
	ctx := context.Background()

	res, err := s.SearchClipsAdvanced(ctx, asset.AdvancedSearchRequest{
		Q:     "boxing",
		Limit: 50,
	})
	require.NoError(t, err, "SearchClipsAdvanced must not error")
	require.NotNil(t, res, "result must not be nil")

	ids := make(map[string]bool)
	for _, c := range res.Clips {
		ids[c.ID] = true
	}

	assert.True(t, ids["lifecycle-active-1"],
		"ACTIVE asset must appear in search results")
	assert.True(t, ids["lifecycle-published-1"],
		"PUBLISHED asset must appear in search results")
	assert.False(t, ids["lifecycle-deleted-req-1"],
		"DELETE_REQUESTED asset must NOT appear in search results (T5 regression guard)")
}

// TestSearchClipsAdvanced_ExcludesAllNonSearchableStates proves that
// every non-{ACTIVE,PUBLISHED} state is excluded from search results.
func TestSearchClipsAdvanced_ExcludesAllNonSearchableStates(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
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
			quality_score REAL, reuse_count INTEGER, last_used_at TEXT
		)
	`)
	require.NoError(t, err)

	// One row per lifecycle state — all contain "boxing" in search_text
	states := []string{
		"PREPARING", "PUBLISHED", "STAGING", "PROCESSING",
		"ACTIVE",
		"DELETE_PENDING", "DELETE_REQUESTED",
		"DRIVE_DELETE_PENDING", "DRIVE_DELETED",
		"INDEX_DELETE_PENDING", "INDEX_DELETED",
		"DELETED", "ERROR",
	}
	for i, st := range states {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO media_assets (id, source, name, lifecycle_state, media_type, search_text)
			 VALUES (?, 'youtube', ?, ?, 'video', 'boxing')`,
			"state-"+st, "asset-"+st, st,
		)
		require.NoError(t, err, "insert state %s", st)
		_ = i // suppress unused
	}

	s := NewAssetStoreSQLite(db, zap.NewNop())
	res, err := s.SearchClipsAdvanced(context.Background(), asset.AdvancedSearchRequest{
		Q:     "boxing",
		Limit: 50,
	})
	require.NoError(t, err)

	found := make(map[string]bool)
	for _, c := range res.Clips {
		found[c.ID] = true
	}

	// Only ACTIVE and PUBLISHED should appear
	for _, st := range states {
		id := "state-" + st
		switch st {
		case "ACTIVE", "PUBLISHED":
			assert.True(t, found[id], "state %s must appear in search results", st)
		default:
			assert.False(t, found[id], "state %s must NOT appear in search results (T5)", st)
		}
	}
}

// TestSearchClipsAdvanced_ExcludesUnclassified pins the
// PR-PLANNER-LEAKAGE-CLEANUP contract: when ExcludeUnclassified is set,
// rows with empty asset_kind (StockRust/one-off test artifacts) must not
// surface; classified rows (asset_kind != ”) must. The control query
// (gate off) proves the artifact is present in the seed so the gate is
// the discriminator, not an incidental filter.
func TestSearchClipsAdvanced_ExcludesUnclassified(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
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
			asset_kind TEXT NOT NULL DEFAULT ''
		)
	`)
	require.NoError(t, err)

	// Classified clip (must surface) + unclassified test artifact (must not).
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, source, name, lifecycle_state, media_type, search_text, asset_kind)
		 VALUES ('yt_clip', 'youtube', 'Jenna Dewan backstage', 'ACTIVE', 'video', 'Jenna Dewan Channing Tatum backstage', 'clip')`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, source, name, lifecycle_state, media_type, search_text, asset_kind)
		 VALUES ('planner:deadbeef:0', 'stock', 'clip_001.mp4', 'PUBLISHED', 'video', '', '')`)
	require.NoError(t, err)

	s := NewAssetStoreSQLite(db, zap.NewNop())

	res, err := s.SearchClipsAdvanced(context.Background(), asset.AdvancedSearchRequest{
		Limit:               50,
		ExcludeUnclassified: true,
	})
	require.NoError(t, err)

	found := make(map[string]bool)
	for _, c := range res.Clips {
		found[c.ID] = true
	}
	assert.True(t, found["yt_clip"], "classified clip must surface")
	assert.False(t, found["planner:deadbeef:0"],
		"unclassified test artifact must NOT surface when ExcludeUnclassified=true")

	// Control: with the gate off, the artifact IS present (the gate is the
	// discriminator, not the lifecycle/search_text incidental filters).
	resAll, err := s.SearchClipsAdvanced(context.Background(), asset.AdvancedSearchRequest{Limit: 50})
	require.NoError(t, err)
	foundAll := make(map[string]bool)
	for _, c := range resAll.Clips {
		foundAll[c.ID] = true
	}
	assert.True(t, foundAll["planner:deadbeef:0"],
		"control: artifact present without the gate (proves the gate discriminates)")
}

// TestSearchClipsByKeywords_ExcludesDeleteRequested proves that
// the SearchClipsByKeywords path also uses the stricter filter.
func TestSearchClipsByKeywords_ExcludesDeleteRequested(t *testing.T) {
	db := newLifecycleTestDB(t)
	s := NewAssetStoreSQLite(db, zap.NewNop())
	ctx := context.Background()

	clips, err := s.SearchClipsByKeywords(ctx, "youtube", []string{"boxing"}, 50)
	require.NoError(t, err)

	ids := make(map[string]bool)
	for _, c := range clips {
		ids[c.ID] = true
	}

	assert.True(t, ids["lifecycle-active-1"],
		"ACTIVE asset must appear")
	assert.True(t, ids["lifecycle-published-1"],
		"PUBLISHED asset must appear")
	assert.False(t, ids["lifecycle-deleted-req-1"],
		"DELETE_REQUESTED asset must NOT appear in SearchClipsByKeywords (T5)")
}

// TestSearchStockByKeywords_ExcludesDeleteRequested proves that
// the SearchStockByKeywords path also uses the stricter filter.
func TestSearchStockByKeywords_ExcludesDeleteRequested(t *testing.T) {
	db := newLifecycleTestDB(t)
	s := NewAssetStoreSQLite(db, zap.NewNop())
	ctx := context.Background()

	// Re-seed with source=stock for this test
	_, err := db.ExecContext(ctx,
		`UPDATE media_assets SET source = 'stock' WHERE id IN ('lifecycle-active-1','lifecycle-published-1','lifecycle-deleted-req-1')`)
	require.NoError(t, err)

	clips, err := s.SearchStockByKeywords(ctx, []string{"boxing"}, 50)
	require.NoError(t, err)

	ids := make(map[string]bool)
	for _, c := range clips {
		ids[c.ID] = true
	}

	assert.True(t, ids["lifecycle-active-1"],
		"ACTIVE stock asset must appear")
	assert.True(t, ids["lifecycle-published-1"],
		"PUBLISHED stock asset must appear")
	assert.False(t, ids["lifecycle-deleted-req-1"],
		"DELETE_REQUESTED stock asset must NOT appear in SearchStockByKeywords (T5)")
}
