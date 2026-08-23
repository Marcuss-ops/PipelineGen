package providermetadata

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// setupProviderMetadataDB creates a fresh SQLite DB with the minimal
// media_assets schema plus the asset_provider_metadata and asset_tags
// tables needed by AssetMetadataRepository.
func setupProviderMetadataDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "metadata.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL")
	require.NoError(t, err)
	require.NoError(t, db.Ping())

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE'
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
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
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',);

		CREATE TABLE asset_provider_metadata (
			asset_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			external_id TEXT NOT NULL,
			creator TEXT,
			country TEXT,
			location TEXT,
			collection_id TEXT,
			collection_title TEXT,
			page_url TEXT,
			thumbnail_url TEXT,
			preview_url TEXT,
			license_class TEXT,
			provider_metadata_hash TEXT,
			raw_metadata_json TEXT,
			fetched_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
			UNIQUE(provider, external_id)
		);

		CREATE TABLE asset_tags (
			asset_id TEXT NOT NULL,
			tag TEXT NOT NULL,
			normalized_tag TEXT NOT NULL,
			source TEXT NOT NULL,
			confidence REAL,
			language TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY(asset_id, normalized_tag, source),
			FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
			CHECK (source IN ('provider', 'semantic', 'manual', 'transcript', 'visual', 'import'))
		);
	`)
	require.NoError(t, err)
	return db
}

func insertProviderMetadataTestAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO media_assets (id, source, name, media_type, lifecycle_state)
		VALUES (?, 'artlist', 'Test Clip', 'video', 'ACTIVE')
	`, id)
	require.NoError(t, err)
}

func TestAssetMetadataRepository_ProviderMetadataUpsertAndGet(t *testing.T) {
	db := setupProviderMetadataDB(t)
	t.Cleanup(func() { db.Close() })
	insertProviderMetadataTestAsset(t, db, "clip-1")

	repo := NewAssetMetadataRepository(db, zaptest.NewLogger(t))
	meta := assets.ProviderMetadata{
		AssetID:              "clip-1",
		Provider:             "artlist",
		ExternalID:           "123456",
		Creator:              "John Richter",
		Country:              "Spain",
		Location:             "Barcelona",
		CollectionID:         "col-1",
		CollectionTitle:      "Cities",
		PageURL:              "https://artlist.io/clip/123456",
		ThumbnailURL:         "https://thumb/123456.jpg",
		PreviewURL:           "https://preview/123456.mp4",
		LicenseClass:         "standard",
		ProviderMetadataHash: "abc123",
		RawMetadataJSON:      `{"key":"value"}`,
	}

	err := repo.UpsertProviderMetadata(context.Background(), meta)
	require.NoError(t, err)

	got, err := repo.GetProviderMetadata(context.Background(), "clip-1")
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, meta.AssetID, got.AssetID)
	assert.Equal(t, meta.Provider, got.Provider)
	assert.Equal(t, meta.ExternalID, got.ExternalID)
	assert.Equal(t, meta.Creator, got.Creator)
	assert.Equal(t, meta.Country, got.Country)
	assert.Equal(t, meta.Location, got.Location)
	assert.Equal(t, meta.CollectionID, got.CollectionID)
	assert.Equal(t, meta.CollectionTitle, got.CollectionTitle)
	assert.Equal(t, meta.PageURL, got.PageURL)
	assert.Equal(t, meta.ThumbnailURL, got.ThumbnailURL)
	assert.Equal(t, meta.PreviewURL, got.PreviewURL)
	assert.Equal(t, meta.LicenseClass, got.LicenseClass)
	assert.Equal(t, meta.ProviderMetadataHash, got.ProviderMetadataHash)
	assert.Equal(t, meta.RawMetadataJSON, got.RawMetadataJSON)
}

func TestAssetMetadataRepository_ReplaceTagsBySource_KeepsSourcesSeparated(t *testing.T) {
	db := setupProviderMetadataDB(t)
	t.Cleanup(func() { db.Close() })
	insertProviderMetadataTestAsset(t, db, "clip-2")

	repo := NewAssetMetadataRepository(db, zaptest.NewLogger(t))

	providerTags := []assets.AssetTag{
		{Tag: "Skyline", NormalizedTag: "skyline"},
		{Tag: "Evening", NormalizedTag: "evening"},
	}
	semanticTags := []assets.AssetTag{
		{Tag: "Urban landscape", NormalizedTag: "urban landscape"},
	}

	ctx := context.Background()
	require.NoError(t, repo.ReplaceTagsBySource(ctx, "clip-2", assets.TagSourceProvider, providerTags))
	require.NoError(t, repo.ReplaceTagsBySource(ctx, "clip-2", assets.TagSourceSemantic, semanticTags))

	all, err := repo.ListTagsByAsset(ctx, "clip-2")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	provider, err := repo.ListTagsBySource(ctx, "clip-2", assets.TagSourceProvider)
	require.NoError(t, err)
	assert.Len(t, provider, 2)
	assert.Contains(t, []string{provider[0].NormalizedTag, provider[1].NormalizedTag}, "skyline")
	assert.Contains(t, []string{provider[0].NormalizedTag, provider[1].NormalizedTag}, "evening")

	semantic, err := repo.ListTagsBySource(ctx, "clip-2", assets.TagSourceSemantic)
	require.NoError(t, err)
	assert.Len(t, semantic, 1)
	assert.Equal(t, "urban landscape", semantic[0].NormalizedTag)

	// Re-running provider tags must replace them but leave semantic tags intact.
	require.NoError(t, repo.ReplaceTagsBySource(ctx, "clip-2", assets.TagSourceProvider, []assets.AssetTag{
		{Tag: "Sunset", NormalizedTag: "sunset"},
	}))
	provider, err = repo.ListTagsBySource(ctx, "clip-2", assets.TagSourceProvider)
	require.NoError(t, err)
	assert.Len(t, provider, 1)
	assert.Equal(t, "sunset", provider[0].NormalizedTag)

	semantic, err = repo.ListTagsBySource(ctx, "clip-2", assets.TagSourceSemantic)
	require.NoError(t, err)
	assert.Len(t, semantic, 1)
}

func TestAssetMetadataRepository_CascadeDelete(t *testing.T) {
	db := setupProviderMetadataDB(t)
	t.Cleanup(func() { db.Close() })
	insertProviderMetadataTestAsset(t, db, "clip-3")

	repo := NewAssetMetadataRepository(db, zaptest.NewLogger(t))
	ctx := context.Background()

	require.NoError(t, repo.UpsertProviderMetadata(ctx, assets.ProviderMetadata{
		AssetID:    "clip-3",
		Provider:   "artlist",
		ExternalID: "999",
	}))
	require.NoError(t, repo.ReplaceTagsBySource(ctx, "clip-3", assets.TagSourceProvider, []assets.AssetTag{
		{Tag: "City", NormalizedTag: "city"},
	}))

	_, err := db.Exec("DELETE FROM media_assets WHERE id = ?", "clip-3")
	require.NoError(t, err)

	meta, err := repo.GetProviderMetadata(ctx, "clip-3")
	require.NoError(t, err)
	assert.Nil(t, meta)

	tags, err := repo.ListTagsByAsset(ctx, "clip-3")
	require.NoError(t, err)
	assert.Empty(t, tags)
}
