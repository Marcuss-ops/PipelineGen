// cmd/admin/clip_drive_audit_test.go — contract tests for the DB↔Drive
// tree audit.
//
// Pins five invariants:
//   - the tree walk materializes nested per-video folders with the exact
//     relative paths used by folder_path (the uVoMqnwEdBQ regression shape);
//   - an aligned clip produces zero divergences and is counted as aligned;
//   - folder_id and folder_path mismatches are each reported independently;
//   - a drive_file_id absent from the tree is reported file_missing_on_drive
//     (fail-closed, never assumed aligned);
//   - clip-like files on Drive that media_assets does not reference are
//     reported as orphans, and non-clip files are ignored.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// fakeDriveTree is a canned ListFiles responder keyed by folder ID.
type fakeDriveTree struct {
	children map[string][]drive.DriveFileInfo
}

func newFakeDriveTree() *fakeDriveTree {
	return &fakeDriveTree{children: map[string][]drive.DriveFileInfo{}}
}

func (f *fakeDriveTree) folder(id, name string, parent string) *fakeDriveTree {
	f.children[parent] = append(f.children[parent], drive.DriveFileInfo{
		ID: id, Name: name, MimeType: driveFolderMimeType, Parents: []string{parent},
	})
	return f
}

func (f *fakeDriveTree) file(id, name string, parent string) *fakeDriveTree {
	f.children[parent] = append(f.children[parent], drive.DriveFileInfo{
		ID: id, Name: name, MimeType: "video/mp4", Parents: []string{parent},
	})
	return f
}

func (f *fakeDriveTree) list(_ context.Context, parentID string) ([]drive.DriveFileInfo, error) {
	return f.children[parentID], nil
}

const (
	auditRootID      = "clips-root"
	auditTomHolland  = "tom-holland"
	auditUncatID     = "youtube-uncategorized"
	auditUvoFolderID = "uvo-folder"
	auditUvoFileID   = "uvo-file"
)

// newAuditDriveTree mirrors the real production tree:
// clips-root/Tom Holland/youtube_uncategorized/uVoMqnwEdBQ/clip.mp4
func newAuditDriveTree() *fakeDriveTree {
	t := newFakeDriveTree()
	t.folder(auditTomHolland, "Tom Holland", auditRootID)
	t.folder(auditUncatID, "youtube_uncategorized", auditTomHolland)
	t.folder(auditUvoFolderID, "uVoMqnwEdBQ", auditUncatID)
	t.file(auditUvoFileID, "yt_uVoMqnwEdBQ_1890_1950_v1_clip.mp4", auditUvoFolderID)
	return t
}

func newAuditDB(t *testing.T) *sql.DB {
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
		folder_path TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create media_assets: %v", err)
	}
	return db
}

func insertAuditRow(t *testing.T, db *sql.DB, id, driveFileID, folderID, folderPath string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, source, drive_file_id, folder_id, folder_path)
		 VALUES (?, 'youtube', ?, ?, ?)`,
		id, driveFileID, folderID, folderPath,
	); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestClipDriveAudit_AlignedClipZeroDivergence(t *testing.T) {
	db := newAuditDB(t)
	insertAuditRow(t, db, "yt_uVoMqnwEdBQ_1890_1950_v1", auditUvoFileID, auditUvoFolderID, "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ")

	report, err := clipDriveAudit(context.Background(), db, newAuditDriveTree().list, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.ClipsTotal != 1 || report.Summary.Aligned != 1 || report.Summary.Divergences != 0 {
		t.Fatalf("summary = %+v, want 1 clip / 1 aligned / 0 divergences", report.Summary)
	}
	if len(report.Divergences) != 0 {
		t.Fatalf("expected no divergences, got %+v", report.Divergences)
	}
}

func TestClipDriveAudit_ReportsFolderIDAndPathMismatches(t *testing.T) {
	db := newAuditDB(t)
	// The pre-backfill state: DB records the REQUEST folder (Tom Holland)
	// while the file physically lives in the nested per-video folder.
	insertAuditRow(t, db, "yt_uVoMqnwEdBQ_1890_1950_v1", auditUvoFileID, auditTomHolland, "Tom Holland")

	report, err := clipDriveAudit(context.Background(), db, newAuditDriveTree().list, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.Aligned != 0 || report.Summary.Divergences != 1 {
		t.Fatalf("summary = %+v, want 0 aligned / 1 divergence", report.Summary)
	}
	if report.Summary.FolderIDMismatch != 1 || report.Summary.FolderPathMismatch != 1 {
		t.Fatalf("expected folder_id + folder_path mismatch, summary = %+v", report.Summary)
	}
	d := report.Divergences[0]
	if d.DriveFolderID != auditUvoFolderID || d.DriveFolderPath != "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ" {
		t.Fatalf("divergence drive side = %q/%q, want uvo-folder / nested path", d.DriveFolderID, d.DriveFolderPath)
	}
}

func TestClipDriveAudit_FileMissingOnDriveFailsClosed(t *testing.T) {
	db := newAuditDB(t)
	insertAuditRow(t, db, "yt_ghost_1_v1", "deleted-file-id", auditUvoFolderID, "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ")

	report, err := clipDriveAudit(context.Background(), db, newAuditDriveTree().list, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.FileMissingOnDrive != 1 || report.Summary.Divergences != 1 {
		t.Fatalf("summary = %+v, want 1 file_missing / 1 divergence", report.Summary)
	}
	if report.Divergences[0].Issues[0] != "file_missing_on_drive" {
		t.Fatalf("issue = %v, want file_missing_on_drive", report.Divergences[0].Issues)
	}
}

func TestClipDriveAudit_NoDriveFileIDReported(t *testing.T) {
	db := newAuditDB(t)
	insertAuditRow(t, db, "yt_uVoMqnwEdBQ_200_212_v1", "", auditTomHolland, "Tom Holland")

	report, err := clipDriveAudit(context.Background(), db, newAuditDriveTree().list, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.NoDriveFileID != 1 || report.Summary.Divergences != 1 {
		t.Fatalf("summary = %+v, want 1 no_drive_file_id / 1 divergence", report.Summary)
	}
	if report.Divergences[0].Issues[0] != "no_drive_file_id" {
		t.Fatalf("issue = %v, want no_drive_file_id", report.Divergences[0].Issues)
	}
}

func TestClipDriveAudit_OrphanDetection(t *testing.T) {
	db := newAuditDB(t)
	// The tree's canonical clip is referenced in the DB, so it is NOT an
	// orphan; only genuinely unreferenced clip-like files are reported.
	insertAuditRow(t, db, "yt_uVoMqnwEdBQ_1890_1950_v1", auditUvoFileID, auditUvoFolderID, "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ")
	tree := newAuditDriveTree()
	// An orphan clip-like file on Drive with no media_assets row.
	tree.file("orphan-file", "yt_e35PVH3ksFA_420_480_v1_orphan.mp4", auditUvoFolderID)
	// A non-clip file must NOT be reported as an orphan.
	tree.file("manifest-file", "manifest.json", auditUvoFolderID)

	report, err := clipDriveAudit(context.Background(), db, tree.list, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.OrphanClipFiles != 1 {
		t.Fatalf("orphans = %d, want 1 (manifest.json must be excluded)", report.Summary.OrphanClipFiles)
	}
	if len(report.OrphanFiles) != 1 || report.OrphanFiles[0].Name != "yt_e35PVH3ksFA_420_480_v1_orphan.mp4" {
		t.Fatalf("orphan files = %+v", report.OrphanFiles)
	}
}

func TestClipDriveAudit_ExcludesNonClipRows(t *testing.T) {
	db := newAuditDB(t)
	insertAuditRow(t, db, "planner:abc:1", auditUvoFileID, auditTomHolland, "Tom Holland")

	report, err := clipDriveAudit(context.Background(), db, newAuditDriveTree().list, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.ClipsTotal != 0 {
		t.Fatalf("clips_total = %d, want 0 (non-yt_ rows excluded)", report.Summary.ClipsTotal)
	}
}

func TestClipDriveAudit_OrphansUnaffectedByLimit(t *testing.T) {
	// A --limit must narrow only the per-clip divergence audit. Orphan
	// detection compares against the FULL drive_file_id set, so a clip row
	// beyond the limit must NOT appear as a false orphan.
	db := newAuditDB(t)
	insertAuditRow(t, db, "yt_uVoMqnwEdBQ_1890_1950_v1", auditUvoFileID, auditUvoFolderID, "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ")
	insertAuditRow(t, db, "yt_second_1_v1", "second-file", auditTomHolland, "Tom Holland")
	tree := newAuditDriveTree()
	tree.file("second-file", "yt_second_1_v1_second.mp4", auditTomHolland)

	report, err := clipDriveAudit(context.Background(), db, tree.list, auditRootID, 1)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.ClipsTotal != 1 {
		t.Fatalf("clips_total = %d, want 1 with --limit 1", report.Summary.ClipsTotal)
	}
	if report.Summary.OrphanClipFiles != 0 {
		t.Fatalf("orphans = %d, want 0 (limit must not create false orphans)", report.Summary.OrphanClipFiles)
	}
}

func TestClipDriveAudit_RespectsLimit(t *testing.T) {
	db := newAuditDB(t)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("yt_clip-%d_v1", i)
		insertAuditRow(t, db, id, auditUvoFileID, auditUvoFolderID, "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ")
	}

	report, err := clipDriveAudit(context.Background(), db, newAuditDriveTree().list, auditRootID, 2)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Summary.ClipsTotal != 2 {
		t.Fatalf("clips_total = %d, want 2 with --limit 2", report.Summary.ClipsTotal)
	}
}

func TestClipDriveAudit_WalkFailureSurfacedNotSilentlyAligned(t *testing.T) {
	db := newAuditDB(t)
	// Root listing fails: the walk must surface the failure and NOT report
	// clips as aligned on the basis of an incomplete tree.
	flaky := func(_ context.Context, parentID string) ([]drive.DriveFileInfo, error) {
		return nil, fmt.Errorf("drive: listing %s failed", parentID)
	}
	insertAuditRow(t, db, "yt_uVoMqnwEdBQ_1890_1950_v1", auditUvoFileID, auditUvoFolderID, "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ")

	report, err := clipDriveAudit(context.Background(), db, flaky, auditRootID, 0)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(report.WalkFailures) == 0 {
		t.Fatal("expected at least one walk failure to be surfaced")
	}
	if report.Summary.Aligned != 0 {
		t.Fatalf("aligned = %d, want 0 with an incomplete tree", report.Summary.Aligned)
	}
	// The clip is reported as missing on Drive (fail-closed) rather than
	// silently aligned.
	if report.Summary.FileMissingOnDrive != 1 {
		t.Fatalf("file_missing = %d, want 1 (fail-closed on incomplete walk)", report.Summary.FileMissingOnDrive)
	}
}
