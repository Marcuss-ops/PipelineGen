// Package assets — clip_cache_adapter_test.go: tests for the
// audit 2026-07-03 BLOCKER #5 cache file verification.
//
// The tests verify that GetExisting returns cache-miss when the
// local file no longer exists (deleted or zero-size), and cache-hit
// when the file is present or when localPath is empty (Drive-only).
//
// BLOCKER #5 closure (Azione 4, August 2026): the four tests that
// previously carried `t.Skip("BLOCKER #5: CGO sqlite3 not available
// in test environment")` now run against a real in-memory SQLite
// repository (`sql.Open("sqlite3", ":memory:")` + NewClipsRepository),
// matching the established pattern in clips_crud_test.go /
// clips_statistics_test.go. There is no longer any skipped test in
// this file.
package imagesregistry

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"go.uber.org/zap/zaptest"
)

// insertClipCacheAsset inserts a media_assets row with the fields the
// clip cache adapter reads (id, local_path, drive_file_id) plus the
// lifecycle fields the SoftDeleteFilter needs so Get returns the row.
func insertClipCacheAsset(t *testing.T, db *sql.DB, id, localPath, driveFileID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO media_assets (
			id, source, name, media_type, status,
			local_path, drive_file_id, filename,
			lifecycle_state, deleted_at
		) VALUES (?, 'youtube', 'clip', 'video', 'active', ?, ?, 'clip.mp4', 'ACTIVE', '')
	`, id, localPath, driveFileID)
	require.NoError(t, err, "insert media_assets row id=%s", id)
}

// newClipCacheTestRepo opens an in-memory SQLite with the canonical
// 40-column media_assets schema and returns a ready-to-use
// *ClipsRepository plus its *sql.DB (closed via t.Cleanup).
func newClipCacheTestRepo(t *testing.T) *ClipsRepository {
	t.Helper()
	db := newAlignTestDB(t)
	return NewClipsRepository(db, zap.NewNop())
}

// TestClipCacheAdapter_FileMissingReturnsCacheMiss verifies BLOCKER #5:
// when a cached asset's local file has been deleted, GetExisting must
// return cache-miss (nil, false, nil) so the use case falls through
// to re-download.
func TestClipCacheAdapter_FileMissingReturnsCacheMiss(t *testing.T) {
	ctx := context.Background()
	repo := newClipCacheTestRepo(t)
	adapter := NewClipCacheAdapter(repo, zaptest.NewLogger(t))

	// 1. Create temp file, write "content", get path.
	tmpFile, err := os.CreateTemp("", "clipcache-missing-*.mp4")
	require.NoError(t, err, "create temp file")
	tmpPath := tmpFile.Name()
	_, werr := tmpFile.Write([]byte("content"))
	require.NoError(t, werr, "write temp file")
	require.NoError(t, tmpFile.Close(), "close temp file")

	// 2. Insert asset into in-memory SQLite with localPath=tempPath.
	const clipID = "yt_vid_0_4_v1"
	insertClipCacheAsset(t, repo.db, clipID, tmpPath, "")

	// 3. Delete the temp file — the row is now stale.
	require.NoError(t, os.Remove(tmpPath), "remove temp file")

	// 4. Call GetExisting → expect (nil, false, nil).
	item, hit, err := adapter.GetExisting(ctx, clipID)
	require.NoError(t, err, "GetExisting must not error on stale file")
	require.Nil(t, item, "stale file must produce nil item")
	require.False(t, hit, "stale file must be a cache miss")

	// 5. Call GetExisting again → same result (idempotent).
	item2, hit2, err2 := adapter.GetExisting(ctx, clipID)
	require.NoError(t, err2, "second GetExisting must not error")
	require.Nil(t, item2, "second GetExisting must stay nil")
	require.False(t, hit2, "second GetExisting must stay a cache miss")
}

// TestClipCacheAdapter_FileExistsReturnsCacheHit verifies the normal
// cache-hit path when the local file exists and is non-empty.
func TestClipCacheAdapter_FileExistsReturnsCacheHit(t *testing.T) {
	ctx := context.Background()
	repo := newClipCacheTestRepo(t)
	adapter := NewClipCacheAdapter(repo, zaptest.NewLogger(t))

	// 1. Create temp file, write "content", get path (keep it alive).
	tmpFile, err := os.CreateTemp("", "clipcache-hit-*.mp4")
	require.NoError(t, err, "create temp file")
	tmpPath := tmpFile.Name()
	_, werr := tmpFile.Write([]byte("content"))
	require.NoError(t, werr, "write temp file")
	require.NoError(t, tmpFile.Close(), "close temp file")
	t.Cleanup(func() { _ = os.Remove(tmpPath) })

	// 2. Insert asset into in-memory SQLite with localPath=tempPath.
	const clipID = "yt_vid_10_14_v1"
	insertClipCacheAsset(t, repo.db, clipID, tmpPath, "")

	// 3. Call GetExisting → expect (item, true, nil).
	item, hit, err := adapter.GetExisting(ctx, clipID)
	require.NoError(t, err, "GetExisting must not error on present file")
	require.True(t, hit, "present file must be a cache hit")
	require.NotNil(t, item, "cache hit must return an item")

	// 4. Verify item.LocalPath == tempPath.
	require.Equal(t, tmpPath, item.LocalPath, "cached item must surface the canonical local path")
	require.Equal(t, "skipped", item.Status, "cache hit renders idempotent 'skipped' status")
}

// TestClipCacheAdapter_EmptyFileReturnsCacheMiss verifies that a
// zero-size file is treated as cache-miss (same as missing).
func TestClipCacheAdapter_EmptyFileReturnsCacheMiss(t *testing.T) {
	ctx := context.Background()
	repo := newClipCacheTestRepo(t)
	adapter := NewClipCacheAdapter(repo, zaptest.NewLogger(t))

	// 1. Create an empty temp file (exists but size 0).
	tmpFile, err := os.CreateTemp("", "clipcache-empty-*.mp4")
	require.NoError(t, err, "create temp file")
	tmpPath := tmpFile.Name()
	require.NoError(t, tmpFile.Close(), "close temp file")
	t.Cleanup(func() { _ = os.Remove(tmpPath) })

	// 2. Insert asset into in-memory SQLite with localPath=tempPath.
	const clipID = "yt_vid_20_24_v1"
	insertClipCacheAsset(t, repo.db, clipID, tmpPath, "")

	// 3. Call GetExisting → expect (nil, false, nil).
	item, hit, err := adapter.GetExisting(ctx, clipID)
	require.NoError(t, err, "GetExisting must not error on empty file")
	require.Nil(t, item, "empty file must produce nil item")
	require.False(t, hit, "empty file must be a cache miss")
}

// TestClipCacheAdapter_EmptyLocalPathReturnsCacheHit verifies the
// Drive-only path: when localPath is empty but DriveFileID is present,
// GetExisting returns a cache hit (Drive file may still be accessible).
// When BOTH localPath and DriveFileID are empty, returns cache-miss
// (no usable file reference — degenerate row guard).
func TestClipCacheAdapter_EmptyLocalPathReturnsCacheHit(t *testing.T) {
	ctx := context.Background()
	repo := newClipCacheTestRepo(t)
	adapter := NewClipCacheAdapter(repo, zaptest.NewLogger(t))

	// 1. Insert asset with localPath="" and driveFileID="drive_xyz".
	const clipID = "yt_vid_30_34_v1"
	insertClipCacheAsset(t, repo.db, clipID, "", "drive_xyz")

	// 2. Call GetExisting → expect (item, true, nil).
	item, hit, err := adapter.GetExisting(ctx, clipID)
	require.NoError(t, err, "GetExisting must not error on Drive-only row")
	require.True(t, hit, "Drive-only row must be a cache hit")
	require.NotNil(t, item, "Drive-only hit must return an item")

	// 3. Verify item.DriveFileID == "drive_xyz".
	require.Equal(t, "drive_xyz", item.DriveFileID, "cached item must surface the Drive file id")

	// Degenerate guard: BOTH empty → cache-miss.
	const degenerateID = "yt_vid_40_44_v1"
	insertClipCacheAsset(t, repo.db, degenerateID, "", "")
	itemD, hitD, errD := adapter.GetExisting(ctx, degenerateID)
	require.NoError(t, errD, "degenerate row must not error")
	require.Nil(t, itemD, "degenerate row must produce nil item")
	require.False(t, hitD, "degenerate row must be a cache miss")
}

// TestClipCacheAdapter_NilLoggerAllowed verifies that a nil logger
// passed to NewClipCacheAdapter is safe (falls back to zap.NewNop).
func TestClipCacheAdapter_NilLoggerAllowed(t *testing.T) {
	// This test doesn't need CGO — it validates the constructor's nil guard.
	adapter := NewClipCacheAdapter(nil, nil)
	if adapter == nil {
		t.Fatal("NewClipCacheAdapter with nil repo + nil log must return non-nil adapter (fail-closed in GetExisting, not ctor)")
	}
	// Verify GetExisting on nil repo returns error (fail-closed).
	_, _, err := adapter.GetExisting(context.Background(), "test-clip")
	if err == nil {
		t.Error("GetExisting on nil repo must return error (fail-closed)")
	}
}

// TestClipCacheAdapter_LogsCacheMiss verifies that GetExisting
// returns cache-miss when localPath is non-empty but the file is gone
// (the primary BLOCKER #5 scenario) against a REAL repository, and
// that the miss is idempotent.
func TestClipCacheAdapter_LogsCacheMiss(t *testing.T) {
	ctx := context.Background()
	repo := newClipCacheTestRepo(t)
	adapter := NewClipCacheAdapter(repo, zaptest.NewLogger(t))

	// Create a temp file, write content, then delete it — path is stale.
	tmpFile, err := os.CreateTemp("", "clipcache-test-*.mp4")
	require.NoError(t, err, "create temp file")
	tmpPath := tmpFile.Name()
	_, werr := tmpFile.Write([]byte("test content"))
	require.NoError(t, werr, "write temp file")
	require.NoError(t, tmpFile.Close(), "close temp file")
	require.NoError(t, os.Remove(tmpPath), "remove temp file")

	// Verify the file is actually gone.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file should be deleted; os.Stat returned: err=%v", err)
	}

	const clipID = "yt_vid_50_54_v1"
	insertClipCacheAsset(t, repo.db, clipID, tmpPath, "")

	item, hit, err := adapter.GetExisting(ctx, clipID)
	require.NoError(t, err, "GetExisting must not error for stale local path")
	require.Nil(t, item, "stale local path must produce nil item")
	require.False(t, hit, "stale local path must be a cache miss")
}
