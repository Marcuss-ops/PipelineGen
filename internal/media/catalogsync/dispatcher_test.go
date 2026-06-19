package catalogsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// dispatcherTestSchema composes the canonical media_assets CREATE TABLE
// (see internal/storage/canonical.go::CanonicalMediaAssetsSchema) plus the
// outbox_events companion tables that Dispatcher reads/writes.
//
// The unique index on outbox_events(event_key) makes Dispatcher.EnqueueAndIndex
// idempotent: a duplicate Enqueue with the same event_key is silently swallowed.
const dispatcherTestSchema = drive.CanonicalMediaAssetsSchema + `
	CREATE TABLE outbox_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		aggregate_id TEXT NOT NULL DEFAULT '',
		aggregate_type TEXT NOT NULL DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '',
		event_key TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		attempt_count INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 10,
		last_error TEXT NOT NULL DEFAULT '',
		next_attempt_at TEXT,
		worker_id TEXT NOT NULL DEFAULT '',
		lease_id TEXT NOT NULL DEFAULT '',
		lease_expiry TEXT,
		completed_at TEXT,
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX ux_outbox_events_event_key
		ON outbox_events(event_key);
	CREATE INDEX idx_outbox_events_status
		ON outbox_events(status, next_attempt_at);
`

// TestUpsertPreservingExisting_DispatcherPath verifies the canonical PR1
// flow: when SetDispatcher is wired, upsertPreservingExisting performs an
// ATOMIC upsert of media_assets and INSERT into outbox_events in a
// single transaction. Both rows must exist after the call returns. There
// is no goroutine: the outbox event IS the indexing trigger.
func TestUpsertPreservingExisting_DispatcherPath(t *testing.T) {
	ctx := context.Background()
	db := drive.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := sqlite.NewClipsRepository(db, zap.NewNop())
	outboxEventsRepo := outboxevents.NewRepository(db)
	txmgr := outbox.NewManager(db, zap.NewNop())
	// Direct single-repo dispatcher for the test — production wiring uses
	// MultiClipsUpserter; single-repo is the simpler primitive that proves
	// atomic upsert+enqueue without the routing layer in the way.
	dispatcher := outbox.NewDispatcher(repo, outboxEventsRepo, txmgr, zap.NewNop())

	svc := &Service{log: zap.NewNop()}
	svc.SetDispatcher(dispatcher)

	clip := &assets.Asset{
		ID:             "test_clip_001",
		Source:         "youtube",
		Name:           "Test clip",
		MediaType:      "video",
		Tags:           []string{"nature"},
		Group:          "youtube",
		LifecycleState: assets.StateReady,
	}
	clip.SetIsFolder(false)
	clip.SetFileHash("abc123")
	clip.SetDriveLink("https://drive.google.com/file/d/abc")

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, clip))

	// media_assets row must be present (atomic with the outbox write).
	stored, err := repo.GetClip(ctx, "test_clip_001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, assets.Source("youtube"), stored.Source)
	assert.Equal(t, "Test clip", stored.Name)
	assert.Equal(t, "abc123", stored.FileHash())
	assert.Equal(t, "https://drive.google.com/file/d/abc", stored.DriveLink())

	// outbox_events row must be present — this is what the legacy
	// SafeGoFunc(IndexClip) pattern achieved via fire-and-forget goroutine.
	// The dispatcher achieves it via atomic transaction + outboxevents Pool pickup.
	var outboxCount int
	require.NoError(t,
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ? AND event_type = 'asset.index.requested'", "test_clip_001").Scan(&outboxCount),
	)
	assert.Equal(t, 1, outboxCount, "outbox_events row must be inserted in the same tx as the upsert")
}

// TestUpsertPreservingExisting_DispatcherPath_FolderSkipsOutbox verifies
// the folder edge case: Dispatcher routes folders through the upsert path
// only, skipping the outbox enqueue (folders are not vector-indexable).
func TestUpsertPreservingExisting_DispatcherPath_FolderSkipsOutbox(t *testing.T) {
	ctx := context.Background()
	db := drive.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := sqlite.NewClipsRepository(db, zap.NewNop())
	outboxEventsRepo := outboxevents.NewRepository(db)
	txmgr := outbox.NewManager(db, zap.NewNop())
	dispatcher := outbox.NewDispatcher(repo, outboxEventsRepo, txmgr, zap.NewNop())

	svc := &Service{log: zap.NewNop()}
	svc.SetDispatcher(dispatcher)

	folder := &assets.Asset{
		ID:             "test_folder_001",
		Source:         "youtube",
		Name:           "Root Folder",
		LifecycleState: assets.StateReady,
	}
	folder.SetIsFolder(true)
	folder.SetFolderID("test_folder_001")

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, folder))

	// media_assets row must be present (folder metadata is canonical).
	stored, err := repo.GetClip(ctx, "test_folder_001")
	require.NoError(t, err)
	require.NotNil(t, stored, "folder metadata must be persisted even though IsFolder is not a DB column")
	assert.Equal(t, "Root Folder", stored.Name)
	assert.Equal(t, "test_folder_001", stored.FolderID())

	// NO outbox_events row — folders are not vector-indexable.
	var outboxCount int
	require.NoError(t,
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?", "test_folder_001").Scan(&outboxCount),
	)
	assert.Equal(t, 0, outboxCount, "folders must not produce an indexing event in the outbox")
}

// TestUpsertPreservingExisting_NilDispatcherLegacyPath verifies the
// backwards-compatibility fallback: when SetDispatcher has not been
// called (partial wiring / unit tests), the legacy repo.UpsertClip path
// is taken.
func TestUpsertPreservingExisting_NilDispatcherLegacyPath(t *testing.T) {
	ctx := context.Background()
	db := drive.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	svc := &Service{log: zap.NewNop()}
	// Note: SetDispatcher NOT called.

	clip := &assets.Asset{
		ID:             "legacy_clip_001",
		Source:         "youtube",
		Name:           "Legacy clip",
		MediaType:      "video",
		LifecycleState: assets.StateReady,
	}
	clip.SetIsFolder(false)
	clip.SetFileHash("legacy_hash")

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, clip))

	// media_assets row must be present (upsert).
	stored, err := repo.GetClip(ctx, "legacy_clip_001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "legacy_hash", stored.FileHash())

	// NO outbox_events row — legacy path doesn't use the outbox.
	var outboxCount int
	require.NoError(t,
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?", "legacy_clip_001").Scan(&outboxCount),
	)
	assert.Equal(t, 0, outboxCount, "legacy path does NOT enqueue outbox events")
}
