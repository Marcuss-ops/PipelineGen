package catalogsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

type testSourceReader struct{}

var _ SourceReader = testSourceReader{}

func (testSourceReader) GetFileMeta(context.Context, string) (*RemoteFileMeta, error) {
	return &RemoteFileMeta{}, nil
}

func (testSourceReader) ListFiles(context.Context, string) ([]RemoteFile, error) {
	return nil, nil
}

// TestUpsertPreservingExisting_DispatcherPath verifies the canonical PR1
// flow: when Deps.Dispatcher is wired (PR-D, June 2026 — the late-bind
// SetDispatcher setter was removed), upsertPreservingExisting performs an
// ATOMIC upsert of media_assets and INSERT into outbox_events in a
// single transaction. Both rows must exist after the call returns. There
// is no goroutine: the outbox event IS the indexing trigger.
func TestUpsertPreservingExisting_DispatcherPath(t *testing.T) {
	ctx := context.Background()
	db := drive.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := assets.NewClipsRepository(db, zap.NewNop())
	outboxEventsRepo := outboxevents.NewRepository(db)
	txmgr := outbox.NewManager(db, zap.NewNop())
	// Direct single-repo dispatcher for the test — production wiring uses
	// MultiClipsUpserter; single-repo is the simpler primitive that proves
	// atomic upsert+enqueue without the routing layer in the way. The
	// same *assets.ClipsRepository that implements ClipsUpserter also
	// implements ClipsStateWriter (the two-method split is a Go-type
	// partition, not a runtime one), so the production adapter idiom
	// `outbox.ClipsStateWriter(repo)` works unchanged in test fixtures
	// (closure of PR7 producer migration ticket item D).
	stateWriter := outbox.ClipsStateWriter(repo)
	dispatcher := outbox.NewDispatcher(repo, stateWriter, outboxEventsRepo, txmgr, zap.NewNop())

	// PR-D: construct the service via Deps{} (no SetDispatcher setter
	// exists post-2026-06). The dispatcher is captured at construction
	// time, mirroring the production wiring in BuildSyncBundle.
	//
	// Required-by-ctor fields that are NOT exercised by the dispatcher
	// path are passed as zero-valued struct pointers (Reader, AssetTree)
	// to satisfy NewService's nil-checks without invoking any method on
	// them.
	//
	// Wave G (June 2026): AssetIndex and ClipIndexer are REMOVED from
	// Deps. The legacy `&clipindexer.Service{}` zero-struct is gone —
	// the field no longer exists on the struct literal. The test
	// continues to pass the AssetTree zero-struct because the recursive
	// walker dereferences it (the dispatcher path itself does not).
	svc, err := NewService(Deps{
		Reader:     testSourceReader{},
		Targets:    nil, // no pre-configured targets
		AssetTree:  &assettree.Service{},
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
	})
	require.NoError(t, err)

	clip := &asset.Asset{
		ID:             "test_clip_001",
		Source:         "youtube",
		Name:           "Test clip",
		MediaType:      "video",
		Tags:           []string{"nature"},
		Group:          "youtube",
		LifecycleState: asset.StateActive,
	}
	clip.SetIsFolder(false)
	clip.SetLegacyFileMD5("abc123")
	clip.SetDriveLink("https://drive.google.com/file/d/abc")

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, repo, clip))

	// media_assets row must be present (atomic with the outbox write).
	stored, err := repo.GetClip(ctx, "test_clip_001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, asset.Source("youtube"), stored.Source)
	assert.Equal(t, "Test clip", stored.Name)
	assert.Equal(t, "abc123", stored.LegacyFileMD5())
	assert.Equal(t, "https://drive.google.com/file/d/abc", stored.DriveLink())

	// outbox_events row must be present — this is what the dispatcher
	// achieves via atomic transaction + outboxevents Pool pickup.
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

	repo := assets.NewClipsRepository(db, zap.NewNop())
	outboxEventsRepo := outboxevents.NewRepository(db)
	txmgr := outbox.NewManager(db, zap.NewNop())
	// Same dual-role adapter pattern as the dispatcher_path test:
	// ClipsStateWriter + ClipsUpserter split is a Go-type partition,
	// the same concrete *assets.ClipsRepository implements both.
	stateWriter := outbox.ClipsStateWriter(repo)
	dispatcher := outbox.NewDispatcher(repo, stateWriter, outboxEventsRepo, txmgr, zap.NewNop())

	svc, err := NewService(Deps{
		Reader:     testSourceReader{},
		Targets:    nil,
		AssetTree:  &assettree.Service{},
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
	})
	require.NoError(t, err)

	folder := &asset.Asset{
		ID:             "test_folder_001",
		Source:         "youtube",
		Name:           "Root Folder",
		LifecycleState: asset.StateActive,
	}
	folder.SetIsFolder(true)
	folder.SetFolderID("test_folder_001")

	require.NoError(t, svc.upsertPreservingExisting(ctx, repo, repo, folder))

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

// TestUpsertPreservingExisting_NilDispatcherReturnsError verifies that
// upsertPreservingExisting returns an error when the dispatcher is not wired.
// PR-D (June 2026): the ctor itself rejects nil dispatcher with
// ErrCatalogSyncNilDispatcher; this test instead exercises the in-method
// runtime defence-in-depth check that survives ctor-time wiring changes.
func TestUpsertPreservingExisting_NilDispatcherReturnsError(t *testing.T) {
	ctx := context.Background()
	db := drive.NewTestDBWithSchema(t, dispatcherTestSchema)
	defer db.Close()

	repo := assets.NewClipsRepository(db, zap.NewNop())

	// Construct a Service via the unexported struct literal to bypass the
	// ctor's nil-dispatcher guard — we want to exercise the runtime
	// defence-in-depth path in upsertPreservingExisting (see
	// sync_persist.go::upsertPreservingExisting).
	svc := &Service{log: zap.NewNop()}
	// Note: Deps.Dispatcher NOT passed — dispatcher is nil.

	clip := &asset.Asset{
		ID:             "legacy_clip_001",
		Source:         "youtube",
		Name:           "Legacy clip",
		MediaType:      "video",
		LifecycleState: asset.StateActive,
	}
	clip.SetIsFolder(false)
	clip.SetLegacyFileMD5("legacy_hash")

	err := svc.upsertPreservingExisting(ctx, repo, repo, clip)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dispatcher is nil")
}

// TestNewService_NilDepsRejected verifies the PR-D ctor validation surface:
// every REQUIRED dep (Reader / Dispatcher / Log) is rejected with its own
// typed sentinel error so composition wiring + tests can assert the precise
// missing dep. The single OPTIONAL dep (AssetTree) is accepted as nil at
// ctor time because every catalogsync call site nil-safe-guards it
// (verified via code-searcher audit, June 2026).
//
// Wave G (June 2026): AssetIndex and ClipIndexer are no longer on Deps
// (the legacy fields were unused and removed); the test surface is
// updated to reflect the 5-field struct.
//
// Mirrors the stockpipeline sentinel matrix in deps_struct_smoke_test.go;
// this file holds the catalogsync half. Subtests mirror the ctor
// validation order: Reader first, then Dispatcher, then Log.
func TestNewService_NilDepsRejected(t *testing.T) {
	log := zap.NewNop() // shared fixture: every subtest constructs Deps{Log: log}

	t.Run("nil reader returns ErrCatalogSyncNilReader", func(t *testing.T) {
		_, err := NewService(Deps{Log: log, Dispatcher: &outbox.Dispatcher{}})
		require.ErrorIs(t, err, ErrCatalogSyncNilReader)
	})
	t.Run("nil dispatcher returns ErrCatalogSyncNilDispatcher", func(t *testing.T) {
		// AssetTree is accepted as nil here: it is optional per the
		// post-review right-sizing (nil-safe guards in
		// sync_prune.go::pruneMissingFolders + sync_recursive.go:79,175).
		// Only the absence of Reader / Dispatcher / Log triggers a sentinel.
		_, err := NewService(Deps{Reader: testSourceReader{},
			Log: log})
		require.ErrorIs(t, err, ErrCatalogSyncNilDispatcher)
	})
	t.Run("nil log returns ErrCatalogSyncNilLog", func(t *testing.T) {
		_, err := NewService(Deps{
			Reader:     testSourceReader{},
			Dispatcher: &outbox.Dispatcher{},
		})
		require.ErrorIs(t, err, ErrCatalogSyncNilLog)
	})
	t.Run("all-non-nil happy path", func(t *testing.T) {
		_, err := NewService(Deps{
			Reader:     testSourceReader{},
			Targets:    nil,
			AssetTree:  nil, // optional, nil-safe guarded
			Dispatcher: &outbox.Dispatcher{},
			Log:        log,
		})
		require.NoError(t, err)
	})
	t.Run("invalid configured target is rejected", func(t *testing.T) {
		_, err := NewService(Deps{
			Reader: testSourceReader{},
			Targets: []Target{{
				Source:       "youtube",
				RootFolderID: "folder-1",
			}},
			Dispatcher: &outbox.Dispatcher{},
			Log:        log,
		})
		require.ErrorIs(t, err, ErrCatalogSyncInvalidTarget)
	})
	t.Run("optional deps nil is accepted", func(t *testing.T) {
		// AssetTree nil — ctor accepts because it's nil-safe guarded
		// at every catalogsync call site. Documenting this explicitly
		// so future maintainers see the optionality contract.
		_, err := NewService(Deps{
			Reader:     testSourceReader{},
			Dispatcher: &outbox.Dispatcher{},
			Log:        log,
		})
		require.NoError(t, err, "optional dep (AssetTree) must be acceptable as nil")
	})
}
