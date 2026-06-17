package catalogsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/outbox"
	"velox/go-master/internal/storage"
)

// dispatcherTestSchema mirrors the canonical media_assets + media_index_outbox
// table layout used by PipelineGen. Kept locally because storage.NewTestDBWithSchema
// is a thin wrapper that only takes the CREATE statements as a string.
//
// The unique index on media_index_outbox(asset_id, content_hash, embedding_model,
// embedding_version, collection_version) makes Dispatcher.EnqueueAndIndex
// idempotent: a duplicate Enqueue is silently swallowed.
const dispatcherTestSchema = `
	CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]',
		tags_norm TEXT NOT NULL DEFAULT '',
		embedding_json TEXT NOT NULL DEFAULT '[]',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		url TEXT NOT NULL DEFAULT '',
		media_type TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		relative_path TEXT NOT NULL DEFAULT '',
		drive_file_id TEXT NOT NULL DEFAULT '',
		drive_folder_id TEXT NOT NULL DEFAULT '',
		drive_link TEXT NOT NULL DEFAULT '',
		download_link TEXT NOT NULL DEFAULT '',
		file_hash TEXT NOT NULL DEFAULT '',
		metadata_json TEXT NOT NULL DEFAULT '{}',
		visual_embedding TEXT NOT NULL DEFAULT '',
		transcript_embedding TEXT NOT NULL DEFAULT '',
		created_at TEXT,
		updated_at TEXT
	);
	CREATE TABLE media_index_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		asset_id TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		embedding_model TEXT NOT NULL DEFAULT '',
		embedding_version TEXT NOT NULL DEFAULT '',
		collection_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		payload_json TEXT NOT NULL DEFAULT '',
		attempt_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		next_attempt_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX ux_media_index_outbox_asset
		ON media_index_outbox(asset_id, content_hash, embedding_model, embedding_version, collection_version);
`

// TestUpsertPreservingExisting_DispatcherPath verifies the canonical PR1
// flow: when SetDispatcher is wired, upsertPreservingExisting performs an
// ATOMIC upsert of media_assets and INSERT into media_index_outbox in a
// single transaction. Both rows must exist after the call returns. There
// is no goroutine: the outbox entry IS the indexing trigger.
func TestUpsertPreservingExisting_DispatcherPath(t *testing.T) {
	ctx := context.Background()
	db := storage.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := clips.NewRepository(db, zap.NewNop())
	outboxRepo := outbox.NewRepository(db, zap.NewNop())
	txmgr := outbox.NewManager(db, zap.NewNop())
	// Direct single-repo dispatcher for the test — production wiring uses
	// MultiClipsUpserter; single-repo is the simpler primitive that proves
	// atomic upsert+enqueue without the routing layer in the way.
	dispatcher := outbox.NewDispatcher(repo, outboxRepo, txmgr, zap.NewNop())

	svc := &Service{log: zap.NewNop()}
	svc.SetDispatcher(dispatcher)

	clip := &models.MediaAsset{
		ID:        "test_clip_001",
		Source:    "youtube",
		Name:      "Test clip",
		MediaType: "video",
		IsFolder:  false,
		FileHash:  "abc123",
		Tags:      []string{"nature"},
		Group:     "youtube",
		DriveLink: "https://drive.google.com/file/d/abc",
	}

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, clip))

	// media_assets row must be present (atomic with the outbox write).
	stored, err := repo.GetClip(ctx, "test_clip_001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "youtube", stored.Source)
	assert.Equal(t, "Test clip", stored.Name)
	assert.Equal(t, "abc123", stored.FileHash)
	assert.Equal(t, "https://drive.google.com/file/d/abc", stored.DriveLink)

	// media_index_outbox row must be present — this is what the legacy
	// SafeGoFunc(IndexClip) pattern achieved via fire-and-forget goroutine.
	// The dispatcher achieves it via atomic transaction + worker pickup.
	var outboxCount int
	require.NoError(t,
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_index_outbox WHERE asset_id = ?", "test_clip_001").Scan(&outboxCount),
	)
	assert.Equal(t, 1, outboxCount, "outbox row must be inserted in the same tx as the upsert")
}

// TestUpsertPreservingExisting_DispatcherPath_FolderSkipsOutbox verifies
// the folder edge case: Dispatcher routes folders through the upsert path
// only, skipping the outbox enqueue (folders are not vector-indexable).
func TestUpsertPreservingExisting_DispatcherPath_FolderSkipsOutbox(t *testing.T) {
	ctx := context.Background()
	db := storage.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := clips.NewRepository(db, zap.NewNop())
	outboxRepo := outbox.NewRepository(db, zap.NewNop())
	txmgr := outbox.NewManager(db, zap.NewNop())
	dispatcher := outbox.NewDispatcher(repo, outboxRepo, txmgr, zap.NewNop())

	svc := &Service{log: zap.NewNop()}
	svc.SetDispatcher(dispatcher)

	folder := &models.MediaAsset{
		ID:       "test_folder_001",
		Source:   "youtube",
		Name:     "Root Folder",
		IsFolder: true,
		FolderID: "test_folder_001",
	}

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, folder))

	// media_assets row must be present (folder metadata is canonical).
	// NOTE: clips.Repository does NOT persist the typed IsFolder field —
	// it is a struct-only flag used at write time. Assert presence via
	// row existence + the FolderID/name round-trip rather than the
	// unpersisted IsFolder bit.
	stored, err := repo.GetClip(ctx, "test_folder_001")
	require.NoError(t, err)
	require.NotNil(t, stored, "folder metadata must be persisted even though IsFolder is not a DB column")
	assert.Equal(t, "Root Folder", stored.Name)
	assert.Equal(t, "test_folder_001", stored.FolderID)

	// NO outbox row — folders are not vector-indexable. Dispatcher must
	// skip the enqueue for folders, otherwise we'd burn embedding budget
	// on rows that have no text/visual/audio content to embed.
	var outboxCount int
	require.NoError(t,
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_index_outbox WHERE asset_id = ?", "test_folder_001").Scan(&outboxCount),
	)
	assert.Equal(t, 0, outboxCount, "folders must not produce an embedding job in the outbox")
}

// TestUpsertPreservingExisting_NilDispatcherLegacyPath verifies the
// backwards-compatibility fallback: when SetDispatcher has not been
// called (partial wiring / unit tests), the legacy repo.UpsertClip path
// is taken. The SafeGoFunc IndexClip trigger remains available in this
// mode.
func TestUpsertPreservingExisting_NilDispatcherLegacyPath(t *testing.T) {
	ctx := context.Background()
	db := storage.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := clips.NewRepository(db, zap.NewNop())

	svc := &Service{log: zap.NewNop()}
	// Note: SetDispatcher NOT called.

	clip := &models.MediaAsset{
		ID:        "legacy_clip_001",
		Source:    "youtube",
		Name:      "Legacy clip",
		MediaType: "video",
		IsFolder:  false,
		FileHash:  "legacy_hash",
	}

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, clip))

	// media_assets row must be present (upsert).
	stored, err := repo.GetClip(ctx, "legacy_clip_001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "legacy_hash", stored.FileHash)

	// NO outbox row — legacy path doesn't use the outbox.
	var outboxCount int
	require.NoError(t,
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_index_outbox WHERE asset_id = ?", "legacy_clip_001").Scan(&outboxCount),
	)
	assert.Equal(t, 0, outboxCount, "legacy path does NOT enqueue outbox jobs")
}
