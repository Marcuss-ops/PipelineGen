package imagesregistry

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type recordingFolderProjection struct {
	upserts []string
	inserts []string
	deletes []string
	failOn  string
}

func (p *recordingFolderProjection) UpsertFolder(_ context.Context, folder *detail.ClipFolder) error {
	if p.failOn == "upsert" {
		return sql.ErrConnDone
	}
	p.upserts = append(p.upserts, folder.ID)
	return nil
}

func (p *recordingFolderProjection) InsertFolderIfAbsent(_ context.Context, folder *detail.ClipFolder) error {
	if p.failOn == "insert" {
		return sql.ErrConnDone
	}
	p.inserts = append(p.inserts, folder.ID)
	return nil
}

func (p *recordingFolderProjection) DeleteFolder(_ context.Context, id string) error {
	if p.failOn == "delete" {
		return sql.ErrConnDone
	}
	p.deletes = append(p.deletes, id)
	return nil
}

const clipFoldersSchema = `
CREATE TABLE clip_folders (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL DEFAULT '',
	source_url TEXT NOT NULL DEFAULT '',
	video_id TEXT NOT NULL DEFAULT '',
	folder_id TEXT NOT NULL DEFAULT '',
	folder_path TEXT NOT NULL DEFAULT '',
	local_folder_path TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	manifest_txt_path TEXT NOT NULL DEFAULT '',
	manifest_json_path TEXT NOT NULL DEFAULT '',
	clip_count INTEGER NOT NULL DEFAULT 0,
	processed_count INTEGER NOT NULL DEFAULT 0,
	failed_count INTEGER NOT NULL DEFAULT 0,
	skipped_count INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	metadata TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
	search_key TEXT NOT NULL DEFAULT ''
);`

func newFolderProjectionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "folders.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(clipFoldersSchema)
	require.NoError(t, err)
	return db
}

func TestFolderStore_DualWritesToProjection(t *testing.T) {
	db := newFolderProjectionTestDB(t)
	store := NewClipsRepository(db, zap.NewNop())
	projection := &recordingFolderProjection{}
	store.SetFolderProjection(projection)

	ctx := context.Background()
	require.True(t, store.FolderProjectionWired())

	folder := &detail.ClipFolder{ID: "folder-1", Source: "stock", FolderID: "drive-1", FolderPath: "/a/b"}
	require.NoError(t, store.UpsertFolder(ctx, folder))
	require.Equal(t, []string{"folder-1"}, projection.upserts)

	require.NoError(t, store.DeleteFolder(ctx, "folder-1"))
	require.Equal(t, []string{"folder-1"}, projection.deletes)

	require.NoError(t, store.UpsertDriveFolder(ctx, DriveFolderAttrs{
		Source: "stock", FolderID: "drive-2", FolderPath: "/c", GroupName: "g",
	}))
	require.Equal(t, []string{"drive-2"}, projection.inserts)
}

func TestFolderStore_ProjectionFailureFailsClosed(t *testing.T) {
	db := newFolderProjectionTestDB(t)
	store := NewClipsRepository(db, zap.NewNop())
	store.SetFolderProjection(&recordingFolderProjection{failOn: "upsert"})

	err := store.UpsertFolder(context.Background(), &detail.ClipFolder{ID: "x"})
	require.ErrorIs(t, err, sql.ErrConnDone, "a projection failure must surface, not split the stores")
}

func TestFolderStore_NoProjectionIsNoOp(t *testing.T) {
	db := newFolderProjectionTestDB(t)
	store := NewClipsRepository(db, zap.NewNop())

	ctx := context.Background()
	require.False(t, store.FolderProjectionWired())
	require.NoError(t, store.UpsertFolder(ctx, &detail.ClipFolder{ID: "solo"}))
	n, err := store.BackfillFolderProjection(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestFolderStore_BackfillReplaysOperationalRows(t *testing.T) {
	db := newFolderProjectionTestDB(t)
	store := NewClipsRepository(db, zap.NewNop())

	ctx := context.Background()
	require.NoError(t, store.UpsertFolder(ctx, &detail.ClipFolder{ID: "legacy-1", Source: "artlist", FolderPath: "/x"}))
	require.NoError(t, store.UpsertFolder(ctx, &detail.ClipFolder{ID: "legacy-2", Source: "stock", FolderPath: "/y"}))

	projection := &recordingFolderProjection{}
	store.SetFolderProjection(projection)
	n, err := store.BackfillFolderProjection(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.ElementsMatch(t, []string{"legacy-1", "legacy-2"}, projection.inserts)
	require.Empty(t, projection.upserts, "backfill must not clobber newer projection rows")
}
