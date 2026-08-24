// cmd/admin/backfill_clip_folder_path_test.go — contract tests for the
// DB↔Drive folder-path realignment backfill.
//
// Pins five invariants:
//   - the parent-chain walk resolves nested per-video folders (the exact
//     uVoMqnwEdBQ regression: physical path ≠ request folder);
//   - clips directly inside a channel folder resolve to that folder
//     (folder_path without youtube_uncategorized);
//   - a parent chain that never reaches the root fails closed;
//   - a Drive parent cycle is bounded by maxFolderDepth;
//   - the DB update path is idempotent (second run updates nothing) and
//     dry-run (apply=false) never writes.
package backfill

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// fakeDriveMeta is a canned Drive metadata map keyed by file/folder ID.
type fakeDriveMeta map[string]*drive.FileMeta

func (f fakeDriveMeta) getMeta(_ context.Context, id string) (*drive.FileMeta, error) {
	meta, ok := f[id]
	if !ok {
		return nil, fmt.Errorf("drive: not found: %s", id)
	}
	return meta, nil
}

func meta(id, name string, trashed bool, parents ...string) *drive.FileMeta {
	return &drive.FileMeta{ID: id, Name: name, Trashed: trashed, Parents: parents}
}

const (
	driveRootID = "root-1"

	// Tom Holland / youtube_uncategorized / uVoMqnwEdBQ
	rootID        = "root-1"
	tomHollandID  = "1omaKrmSHurA9y"
	uncatID       = "1FNcE8JQW7p2cSRNK6Ifb7zYB_Mw1dcG2"
	uVoFolderID   = "1V0c2z29-XX3biIkYULIrP0B8z0AJnbRx"
	uVoFileID     = "file-uvo"
	uVoFilePath   = "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ"
	tomHollandDir = "Tom Holland"
)

func newNestedDriveTree() fakeDriveMeta {
	return fakeDriveMeta{
		uVoFileID:    meta(uVoFileID, "clip.mp4", false, uVoFolderID),
		uVoFolderID:  meta(uVoFolderID, "uVoMqnwEdBQ", false, uncatID),
		uncatID:      meta(uncatID, "youtube_uncategorized", false, tomHollandID),
		tomHollandID: meta(tomHollandID, tomHollandDir, false, rootID),
		rootID:       meta(rootID, "Media Root", false),
	}
}

func TestResolveClipFolderPath_NestedPerVideoFolder(t *testing.T) {
	tree := newNestedDriveTree()
	leaf, path, err := resolveClipFolderPath(context.Background(), tree.getMeta, uVoFileID, driveRootID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if leaf != uVoFolderID {
		t.Fatalf("leaf folder = %q, want %q (physical parent, not request folder)", leaf, uVoFolderID)
	}
	if path != uVoFilePath {
		t.Fatalf("path = %q, want %q", path, uVoFilePath)
	}
}

func TestResolveClipFolderPath_DirectInChannelFolder(t *testing.T) {
	fileID := "file-direct"
	tree := newNestedDriveTree()
	tree[fileID] = meta(fileID, "direct.mp4", false, tomHollandID)

	leaf, path, err := resolveClipFolderPath(context.Background(), tree.getMeta, fileID, driveRootID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if leaf != tomHollandID {
		t.Fatalf("leaf folder = %q, want %q", leaf, tomHollandID)
	}
	if path != tomHollandDir {
		t.Fatalf("path = %q, want %q (no youtube_uncategorized segment)", path, tomHollandDir)
	}
}

func TestResolveClipFolderPath_FileDirectlyInRoot(t *testing.T) {
	fileID := "file-at-root"
	tree := newNestedDriveTree()
	tree[fileID] = meta(fileID, "root.mp4", false, rootID)

	leaf, path, err := resolveClipFolderPath(context.Background(), tree.getMeta, fileID, driveRootID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if leaf != rootID {
		t.Fatalf("leaf folder = %q, want root %q", leaf, rootID)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty (file at root)", path)
	}
}

func TestResolveClipFolderPath_RootNotReachedFailsClosed(t *testing.T) {
	orphan := "orphan-folder"
	fileID := "file-orphan"
	tree := fakeDriveMeta{
		fileID: meta(fileID, "orphan.mp4", false, orphan),
		orphan: meta(orphan, "Outside Root", false),
	}
	_, _, err := resolveClipFolderPath(context.Background(), tree.getMeta, fileID, driveRootID)
	if err == nil {
		t.Fatal("expected error when the parent chain never reaches the root")
	}
}

func TestResolveClipFolderPath_CycleBoundedByDepth(t *testing.T) {
	a, b := "folder-a", "folder-b"
	fileID := "file-cycle"
	tree := fakeDriveMeta{
		fileID: meta(fileID, "cycle.mp4", false, a),
		a:      meta(a, "A", false, b),
		b:      meta(b, "B", false, a), // A ↔ B cycle
	}
	_, _, err := resolveClipFolderPath(context.Background(), tree.getMeta, fileID, driveRootID)
	if err == nil {
		t.Fatal("expected error when the parent chain cycles")
	}
}

func TestResolveClipFolderPath_TrashedFileFailsClosed(t *testing.T) {
	tree := newNestedDriveTree()
	tree[uVoFileID] = meta(uVoFileID, "trashed.mp4", true, uVoFolderID)
	_, _, err := resolveClipFolderPath(context.Background(), tree.getMeta, uVoFileID, driveRootID)
	if err == nil {
		t.Fatal("expected error for a trashed file")
	}
}

// newClipFolderBackfillDB creates an in-memory media_assets table with the
// minimal columns the backfill touches.
func newClipFolderBackfillDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL DEFAULT '',
		drive_file_id TEXT NOT NULL DEFAULT '',
		folder_id TEXT NOT NULL DEFAULT '',
		folder_path TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create media_assets: %v", err)
	}
	return db
}

func insertClipFolderRow(t *testing.T, db *sql.DB, id, driveFileID, folderID, folderPath string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, source, drive_file_id, folder_id, folder_path, updated_at)
		 VALUES (?, 'youtube', ?, ?, ?, '')`,
		id, driveFileID, folderID, folderPath,
	); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func readClipFolderRow(t *testing.T, db *sql.DB, id string) (folderID, folderPath string) {
	t.Helper()
	if err := db.QueryRow(`SELECT folder_id, folder_path FROM media_assets WHERE id = ?`, id).
		Scan(&folderID, &folderPath); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return folderID, folderPath
}

func TestBackfillClipFolderPath_UpdatesAndIsIdempotent(t *testing.T) {
	db := newClipFolderBackfillDB(t)
	tree := newNestedDriveTree()
	resolve := func(_ context.Context, driveFileID string) (string, string, error) {
		return resolveClipFolderPath(context.Background(), tree.getMeta, driveFileID, driveRootID)
	}

	// Row with the stale request folder (the uVoMqnwEdBQ regression).
	insertClipFolderRow(t, db, "yt_clip-1", uVoFileID, tomHollandID, tomHollandDir)
	// Row already aligned (must stay untouched).
	insertClipFolderRow(t, db, "yt_clip-2", uVoFileID, uVoFolderID, uVoFilePath)

	stats, err := backfillClipFolderPath(context.Background(), db, resolve, 0, 4, true)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Matched != 2 || stats.Updated != 1 || stats.AlreadyAligned != 1 || stats.Failed != 0 {
		t.Fatalf("run 1: matched=%d updated=%d already=%d failed=%d, want 2/1/1/0",
			stats.Matched, stats.Updated, stats.AlreadyAligned, stats.Failed)
	}
	if folderID, path := readClipFolderRow(t, db, "yt_clip-1"); folderID != uVoFolderID || path != uVoFilePath {
		t.Fatalf("yt_clip-1 after apply: folder_id=%q folder_path=%q, want %q/%q", folderID, path, uVoFolderID, uVoFilePath)
	}

	// Second run must be a pure no-op.
	stats, err = backfillClipFolderPath(context.Background(), db, resolve, 0, 4, true)
	if err != nil {
		t.Fatalf("backfill run 2: %v", err)
	}
	if stats.Updated != 0 || stats.AlreadyAligned != 2 {
		t.Fatalf("run 2: updated=%d already=%d, want 0/2 (idempotent)", stats.Updated, stats.AlreadyAligned)
	}
}

func TestBackfillClipFolderPath_DryRunNeverWrites(t *testing.T) {
	db := newClipFolderBackfillDB(t)
	tree := newNestedDriveTree()
	resolve := func(_ context.Context, driveFileID string) (string, string, error) {
		return resolveClipFolderPath(context.Background(), tree.getMeta, driveFileID, driveRootID)
	}
	insertClipFolderRow(t, db, "yt_clip-1", uVoFileID, tomHollandID, tomHollandDir)

	stats, err := backfillClipFolderPath(context.Background(), db, resolve, 0, 4, false)
	if err != nil {
		t.Fatalf("backfill dry-run: %v", err)
	}
	if stats.Updated != 1 {
		t.Fatalf("dry-run: updated=%d, want 1 (would change)", stats.Updated)
	}
	if folderID, path := readClipFolderRow(t, db, "yt_clip-1"); folderID != tomHollandID || path != tomHollandDir {
		t.Fatalf("dry-run must not write: folder_id=%q folder_path=%q", folderID, path)
	}
}

func TestBackfillClipFolderPath_FailedRowsAreCountedNotSilentlySkipped(t *testing.T) {
	db := newClipFolderBackfillDB(t)
	tree := newNestedDriveTree()
	resolve := func(_ context.Context, driveFileID string) (string, string, error) {
		if driveFileID == "missing-file" {
			return "", "", fmt.Errorf("drive: not found: missing-file")
		}
		return resolveClipFolderPath(context.Background(), tree.getMeta, driveFileID, driveRootID)
	}
	insertClipFolderRow(t, db, "yt_clip-ok", uVoFileID, tomHollandID, tomHollandDir)
	insertClipFolderRow(t, db, "yt_clip-bad", "missing-file", tomHollandID, tomHollandDir)

	stats, err := backfillClipFolderPath(context.Background(), db, resolve, 0, 4, true)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Matched != 2 || stats.Updated != 1 || stats.Failed != 1 {
		t.Fatalf("matched=%d updated=%d failed=%d, want 2/1/1", stats.Matched, stats.Updated, stats.Failed)
	}
	// The failed row must keep its original values.
	if folderID, _ := readClipFolderRow(t, db, "yt_clip-bad"); folderID != tomHollandID {
		t.Fatalf("failed row must not be modified: folder_id=%q", folderID)
	}
}

func TestBackfillClipFolderPath_ExcludesNonClipRows(t *testing.T) {
	db := newClipFolderBackfillDB(t)
	tree := newNestedDriveTree()
	resolve := func(_ context.Context, driveFileID string) (string, string, error) {
		return resolveClipFolderPath(context.Background(), tree.getMeta, driveFileID, driveRootID)
	}
	// A planner/stock row with source='youtube' but a non-yt_ id must stay
	// out of scope (it lives under a different Drive root).
	insertClipFolderRow(t, db, "planner:abc:1", uVoFileID, tomHollandID, tomHollandDir)

	stats, err := backfillClipFolderPath(context.Background(), db, resolve, 0, 4, true)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Matched != 0 {
		t.Fatalf("matched=%d, want 0 (non-yt_ rows must be excluded)", stats.Matched)
	}
}

func TestBackfillClipFolderPath_RespectsLimit(t *testing.T) {
	db := newClipFolderBackfillDB(t)
	tree := newNestedDriveTree()
	resolve := func(_ context.Context, driveFileID string) (string, string, error) {
		return resolveClipFolderPath(context.Background(), tree.getMeta, driveFileID, driveRootID)
	}
	for i := 0; i < 3; i++ {
		insertClipFolderRow(t, db, fmt.Sprintf("yt_clip-%d", i), uVoFileID, tomHollandID, tomHollandDir)
	}

	stats, err := backfillClipFolderPath(context.Background(), db, resolve, 2, 4, true)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Matched != 2 || stats.Updated != 2 {
		t.Fatalf("matched=%d updated=%d, want 2/2 with --limit 2", stats.Matched, stats.Updated)
	}
}
