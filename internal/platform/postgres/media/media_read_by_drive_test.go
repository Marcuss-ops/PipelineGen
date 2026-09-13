// Package media — media_read_by_drive_test.go pins the PostgreSQL read parity
// for the deletion path's Drive-identity lookup (MEDIA-SSOT read-side,
// September 2026).
package media_test

import (
	"context"
	"testing"
)

// TestPostgresMediaCommitter_GetClipByDriveFileID pins parity with the legacy
// SQLite reader (imagesregistry.AssetStoreSQLite.GetClipByDriveFileID):
// drive_file_id / drive_link / download_link are matched with LIKE containment,
// a miss returns (nil, nil) — never a fake match — and an empty id is an error.
func TestPostgresMediaCommitter_GetClipByDriveFileID(t *testing.T) {
	committer, db := newPostgresCommitter(t)
	ctx := context.Background()

	const driveFileID = "1AbCdEfDriveFileID"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, lifecycle_state, drive_file_id, drive_link, download_link)
		VALUES ('asset-drive-1', 'clip', 'Drive clip', 'ACTIVE', $1,
		        'https://drive.google.com/file/d/' || $1 || '/view', '')`,
		driveFileID); err != nil {
		t.Fatalf("seed media_assets: %v", err)
	}

	// (a) exact drive_file_id match.
	clip, err := committer.GetClipByDriveFileID(ctx, driveFileID)
	if err != nil {
		t.Fatalf("GetClipByDriveFileID(exact): %v", err)
	}
	if clip == nil {
		t.Fatal("exact drive_file_id match must resolve the asset")
	}
	if clip.ID != "asset-drive-1" {
		t.Errorf("asset id: want asset-drive-1, got %q", clip.ID)
	}
	if got := string(clip.LifecycleState); got != "ACTIVE" {
		t.Errorf("lifecycle_state: want ACTIVE, got %q", got)
	}
	if got := clip.GetMetadataString("drive_file_id"); got != driveFileID {
		t.Errorf("drive_file_id metadata: want %q, got %q", driveFileID, got)
	}

	// (b) containment match on the drive_link (the legacy reader's LIKE
	// semantics) — the full link must resolve the same row.
	byLink, err := committer.GetClipByDriveFileID(ctx, "https://drive.google.com/file/d/"+driveFileID+"/view")
	if err != nil {
		t.Fatalf("GetClipByDriveFileID(link): %v", err)
	}
	if byLink == nil || byLink.ID != "asset-drive-1" {
		t.Fatalf("drive_link containment match must resolve the asset; got %+v", byLink)
	}

	// (c) unknown id → (nil, nil), not an error.
	missing, err := committer.GetClipByDriveFileID(ctx, "no-such-drive-file")
	if err != nil {
		t.Fatalf("unknown drive id must not error: %v", err)
	}
	if missing != nil {
		t.Fatalf("unknown drive id must resolve to nil; got %+v", missing)
	}

	// (d) empty id → typed error (no unbounded LIKE '%%' scan).
	if _, err := committer.GetClipByDriveFileID(ctx, "   "); err == nil {
		t.Fatal("empty drive file id must be rejected")
	}
}
