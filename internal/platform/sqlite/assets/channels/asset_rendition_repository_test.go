// Package assets — asset_rendition_repository_test.go
//
// Pins the canonical round-trip contract for asset_renditions.
package channels

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newRenditionTestDB opens a fresh in-memory SQLite database and creates
// the minimal media_assets, asset_locations and asset_renditions schema.
func newRenditionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT,
			name TEXT,
			created_at TEXT,
			updated_at TEXT,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '');

		CREATE TABLE asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL,
			location_kind TEXT NOT NULL,
			uri TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);

		CREATE TABLE asset_renditions (
			id TEXT PRIMARY KEY,
			asset_id TEXT NOT NULL,
			location_id INTEGER,
			kind TEXT NOT NULL DEFAULT 'master',
			container TEXT,
			codec TEXT,
			width INTEGER,
			height INTEGER,
			fps REAL,
			bitrate INTEGER,
			color_space TEXT,
			sha256 TEXT,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
			FOREIGN KEY (location_id) REFERENCES asset_locations(id) ON DELETE SET NULL
		);
	`)
	require.NoError(t, err, "create test schema")
	return db
}

func seedRenditionMediaAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets (id, source, name) VALUES (?, ?, ?)`, id, "test", "Test Asset")
	require.NoError(t, err, "seed media_assets row")
}

func seedRenditionLocation(t *testing.T, db *sql.DB, assetID string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO asset_locations (asset_id, location_kind, uri) VALUES (?, ?, ?)`,
		assetID, "local", "/tmp/test.mp4",
	)
	require.NoError(t, err, "seed asset_locations row")
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func TestAssetRenditionRepository_CreateRoundTrip(t *testing.T) {
	db := newRenditionTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetRenditionRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedRenditionMediaAsset(t, db, "asset-001")
	locID := seedRenditionLocation(t, db, "asset-001")

	rendition := &detail.AssetRendition{
		AssetID:    "asset-001",
		LocationID: &locID,
		Kind:       detail.RenditionKindProxy,
		Container:  "mp4",
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		FPS:        29.97,
		Bitrate:    5000000,
		ColorSpace: "bt709",
		SHA256:     "deadbeef",
		SizeBytes:  1024,
	}

	id, err := repo.Create(ctx, rendition)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, id, got.ID)
	assert.Equal(t, "asset-001", got.AssetID)
	require.NotNil(t, got.LocationID)
	assert.Equal(t, locID, *got.LocationID)
	assert.Equal(t, detail.RenditionKindProxy, got.Kind)
	assert.Equal(t, "mp4", got.Container)
	assert.Equal(t, "h264", got.Codec)
	assert.Equal(t, 1920, got.Width)
	assert.Equal(t, 1080, got.Height)
	assert.InDelta(t, 29.97, got.FPS, 0.001)
	assert.Equal(t, int64(5000000), got.Bitrate)
	assert.Equal(t, "bt709", got.ColorSpace)
	assert.Equal(t, "deadbeef", got.SHA256)
	assert.Equal(t, int64(1024), got.SizeBytes)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestAssetRenditionRepository_ListByAsset(t *testing.T) {
	db := newRenditionTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetRenditionRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedRenditionMediaAsset(t, db, "asset-002")

	_, err = repo.Create(ctx, &detail.AssetRendition{AssetID: "asset-002", Kind: detail.RenditionKindMaster})
	require.NoError(t, err)
	_, err = repo.Create(ctx, &detail.AssetRendition{AssetID: "asset-002", Kind: detail.RenditionKindProxy})
	require.NoError(t, err)

	renditions, err := repo.ListByAsset(ctx, "asset-002")
	require.NoError(t, err)
	assert.Len(t, renditions, 2)
}

func TestAssetRenditionRepository_ListByLocation(t *testing.T) {
	db := newRenditionTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetRenditionRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedRenditionMediaAsset(t, db, "asset-003")
	locID := seedRenditionLocation(t, db, "asset-003")

	_, err = repo.Create(ctx, &detail.AssetRendition{AssetID: "asset-003", LocationID: &locID, Kind: detail.RenditionKindMaster})
	require.NoError(t, err)

	renditions, err := repo.ListByLocation(ctx, locID)
	require.NoError(t, err)
	assert.Len(t, renditions, 1)
}

func TestAssetRenditionRepository_CreateWithoutLocation(t *testing.T) {
	db := newRenditionTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetRenditionRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedRenditionMediaAsset(t, db, "asset-006")

	id, err := repo.Create(ctx, &detail.AssetRendition{
		AssetID:   "asset-006",
		Kind:      detail.RenditionKindMaster,
		Container: "mp4",
		Codec:     "h264",
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.LocationID)
}

func TestAssetRenditionRepository_CreateRejectsInvalidKind(t *testing.T) {
	db := newRenditionTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetRenditionRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedRenditionMediaAsset(t, db, "asset-005")

	_, err = repo.Create(ctx, &detail.AssetRendition{
		AssetID: "asset-005",
		Kind:    detail.RenditionKind("invalid"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Kind")
}

func TestAssetRenditionRepository_UpdateAndDelete(t *testing.T) {
	db := newRenditionTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetRenditionRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedRenditionMediaAsset(t, db, "asset-004")

	id, err := repo.Create(ctx, &detail.AssetRendition{AssetID: "asset-004", Kind: detail.RenditionKindMaster})
	require.NoError(t, err)

	rend, err := repo.Get(ctx, id)
	require.NoError(t, err)
	rend.Kind = detail.RenditionKindProxy
	rend.Width = 1280
	rend.Height = 720
	require.NoError(t, repo.Update(ctx, rend))

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, detail.RenditionKindProxy, got.Kind)
	assert.Equal(t, 1280, got.Width)
	assert.Equal(t, 720, got.Height)

	require.NoError(t, repo.Delete(ctx, id))
	got, err = repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, got)
}
