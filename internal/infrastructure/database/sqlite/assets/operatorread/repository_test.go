package operatorread

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/operator"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/stretchr/testify/require"
)

func testSchema() string {
	return `
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    asset_state TEXT NOT NULL DEFAULT 'DISCOVERED',
    index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
    file_hash TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    embedding_json TEXT NOT NULL DEFAULT '',
    collection_version TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE asset_locations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL DEFAULT '',
    uri TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    web_view_link TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE outbox_events (
    event_type TEXT NOT NULL DEFAULT '',
    aggregate_id TEXT NOT NULL,
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE asset_processing (
    asset_id TEXT NOT NULL,
    step TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);
`
}

func seedTestData(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)

	// active indexed asset with local + drive
	storage.MustExec(t, db, `INSERT INTO media_assets
		(id, name, filename, source, provider, media_type, lifecycle_state, asset_state, index_state,
		 file_hash, metadata_json, embedding_json, collection_version, error, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asset-1", "Beluga underwater", "beluga.mp4", "artlist", "artlist", "clip",
		"ACTIVE", "READY", "INDEXED",
		"hash-1", `{"indexed_content_hash":"hash-1","embedding_model_version":"siglip-v2"}`,
		"[0.1]", "media_assets_v3", "", now, now)

	// pending asset with outbox event
	storage.MustExec(t, db, `INSERT INTO media_assets
		(id, name, filename, source, provider, media_type, lifecycle_state, asset_state, index_state,
		 file_hash, metadata_json, embedding_json, collection_version, error, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asset-2", "Pending asset", "pending.mp4", "stock", "stock", "clip",
		"ACTIVE", "DISCOVERED", "DISCOVERED",
		"hash-2", `{}`, "", "", "", now, now)

	// failed embedding asset
	storage.MustExec(t, db, `INSERT INTO media_assets
		(id, name, filename, source, provider, media_type, lifecycle_state, asset_state, index_state,
		 file_hash, metadata_json, embedding_json, collection_version, error, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asset-3", "Failed asset", "failed.mp4", "youtube_clip", "youtube", "clip",
		"ACTIVE", "FAILED_RETRYABLE", "EMBEDDING_FAILED",
		"hash-3", `{}`, "", "", "embedding failed", now, now)

	// stale asset
	storage.MustExec(t, db, `INSERT INTO media_assets
		(id, name, filename, source, provider, media_type, lifecycle_state, asset_state, index_state,
		 file_hash, metadata_json, embedding_json, collection_version, error, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asset-4", "Stale asset", "stale.mp4", "artlist", "artlist", "clip",
		"ACTIVE", "INDEXED", "INDEXED",
		"hash-4-new", `{"indexed_content_hash":"hash-4-old"}`, "", "", "", now, now)

	// not indexable
	storage.MustExec(t, db, `INSERT INTO media_assets
		(id, name, filename, source, provider, media_type, lifecycle_state, asset_state, index_state,
		 file_hash, metadata_json, embedding_json, collection_version, error, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asset-5", "Sound effect", "sfx.mp3", "artlist", "artlist", "sound_effect",
		"ACTIVE", "READY", "NOT_INDEXABLE",
		"hash-5", `{}`, "", "", "", now, now)

	// deleted asset (should be excluded by default)
	storage.MustExec(t, db, `INSERT INTO media_assets
		(id, name, filename, source, provider, media_type, lifecycle_state, asset_state, index_state,
		 file_hash, metadata_json, embedding_json, collection_version, error, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asset-6", "Deleted asset", "deleted.mp4", "stock", "stock", "clip",
		"DELETED", "READY", "DELETED",
		"hash-6", `{}`, "", "", "", now, now)

	// locations
	storage.MustExec(t, db, `INSERT INTO asset_locations (asset_id, location_kind, uri, is_primary) VALUES (?, ?, ?, ?)`, "asset-1", "local", "/tmp/asset-1.mp4", 1)
	storage.MustExec(t, db, `INSERT INTO asset_locations (asset_id, location_kind, uri, is_primary) VALUES (?, ?, ?, ?)`, "asset-1", "drive", "drive://asset-1", 0)
	storage.MustExec(t, db, `INSERT INTO asset_locations (asset_id, location_kind, uri, is_primary) VALUES (?, ?, ?, ?)`, "asset-2", "drive", "drive://asset-2", 1)

	// outbox events
	storage.MustExec(t, db, `INSERT INTO outbox_events (aggregate_id, event_key, status) VALUES (?, ?, ?)`, "asset-2", "asset.index.requested", "pending")
	storage.MustExec(t, db, `INSERT INTO outbox_events (aggregate_id, event_key, status) VALUES (?, ?, ?)`, "asset-2", "asset.index.requested", "pending")

	// processing records
	storage.MustExec(t, db, `INSERT INTO asset_processing (asset_id, step, status, updated_at) VALUES (?, ?, ?, ?)`, "asset-3", "embedding", "failed", now)
}

func newTestReader(t *testing.T) (*InventoryReader, *sql.DB) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, testSchema())
	seedTestData(t, db)
	return NewInventoryReader(db, nil), db
}

func TestInventoryReader_List_NoFilters(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 5)
	require.Equal(t, int64(5), page.Total)
	require.False(t, page.HasMore)
}

func TestInventoryReader_List_FilterBySource(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Source: "artlist", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	for _, item := range page.Items {
		require.Equal(t, "artlist", item.Source)
	}
}

func TestInventoryReader_List_Pagination(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	page1, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	require.True(t, page1.HasMore)
	require.NotEmpty(t, page1.NextCursor)

	page2, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 2, Offset: 2})
	require.NoError(t, err)
	require.Len(t, page2.Items, 2)
	require.True(t, page2.HasMore)
}

func TestInventoryReader_List_IndexHealthCases(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)

	byID := make(map[string]*operator.AssetInventoryItem)
	for _, item := range page.Items {
		byID[item.ID] = item
	}

	require.Equal(t, operator.IndexHealthIndexed, operator.IndexHealthCode(byID["asset-1"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthPending, operator.IndexHealthCode(byID["asset-2"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthFailed, operator.IndexHealthCode(byID["asset-3"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthStale, operator.IndexHealthCode(byID["asset-4"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthNotIndexable, operator.IndexHealthCode(byID["asset-5"].IndexHealth.Code))
}

func TestInventoryReader_List_StorageFlags(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)

	byID := make(map[string]*operator.AssetInventoryItem)
	for _, item := range page.Items {
		byID[item.ID] = item
	}

	require.True(t, byID["asset-1"].HasLocalFile)
	require.True(t, byID["asset-1"].HasDriveFile)
	require.False(t, byID["asset-2"].HasLocalFile)
	require.True(t, byID["asset-2"].HasDriveFile)
}

func TestInventoryReader_List_PendingOutbox(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)

	var found bool
	for _, item := range page.Items {
		if item.ID == "asset-2" {
			require.Equal(t, 2, item.PendingOutboxEvents)
			found = true
		}
	}
	require.True(t, found)
}

func TestInventoryReader_Get(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	inspection, err := reader.Get(ctx, "asset-1")
	require.NoError(t, err)
	require.NotNil(t, inspection)
	require.Equal(t, "Beluga underwater", inspection.Name)
	require.Equal(t, "hash-1", inspection.ContentHash)
	require.Equal(t, "hash-1", inspection.IndexedContentHash)
	require.Equal(t, "siglip-v2", inspection.EmbeddingVersion)
	require.Len(t, inspection.Locations, 2)
	require.Len(t, inspection.OutboxEvents, 0)
}

func TestInventoryReader_Get_WithProcessingAndOutbox(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	inspection, err := reader.Get(ctx, "asset-3")
	require.NoError(t, err)
	require.NotNil(t, inspection)
	require.Len(t, inspection.Processing, 1)
	require.Equal(t, "embedding", inspection.Processing[0].Step)
}

func TestInventoryReader_Get_NotFound(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	inspection, err := reader.Get(ctx, "missing")
	require.NoError(t, err)
	require.Nil(t, inspection)
}

func TestInventoryReader_Facets(t *testing.T) {
	reader, _ := newTestReader(t)
	ctx := context.Background()

	facets, err := reader.Facets(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, facets.MediaTypes)
	require.NotEmpty(t, facets.LifecycleStates)
	require.NotEmpty(t, facets.AssetStates)
	require.NotEmpty(t, facets.IndexStates)
	require.NotEmpty(t, facets.Sources)
	require.NotEmpty(t, facets.Providers)
}
