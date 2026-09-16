// Package media_test — drive_file_lister_test.go pins the MEDIA-SSOT P2-9
// Phase 2 read migration for the "which media assets still carry a Drive file
// id" listing.
//
// WHY THIS EXISTS. internal/capabilities/assets/ingest used to answer this
// question with raw SQL against the operational SQLite handle it was handed as
// `db`. PostgreSQL is the media SSOT, so that statement could only ever see an
// empty catalog — a Drive sweep built on it was blind to every committed asset.
// The listing now resolves through PostgreSQL (media.MediaDriveFileLister) via
// a consumer-owned narrow port.
//
// The predicate is deliberately IDENTICAL to the retired SQLite statement, so
// this test asserts the exact semantics that must survive the engine swap:
//
//   - a row with an empty drive_file_id is NOT listed
//   - a row whose lifecycle_state is 'DELETED' is NOT listed
//   - a live row with a drive_file_id IS listed
//
// MEASURED SCHEMA DIVERGENCE. Two of the retired statement's guards are dead on
// the media SSOT, because the PostgreSQL columns are declared NOT NULL while the
// SQLite ones were nullable:
//
//	drive_file_id     NOT NULL DEFAULT ''   → `drive_file_id IS NOT NULL` never filters
//	lifecycle_state   NOT NULL             → `lifecycle_state <> 'DELETED'` never sees NULL
//
// (Both reject an explicit NULL with SQLSTATE 23502.) The guards are kept anyway
// so the statement stays byte-equivalent with the one it replaced — dropping
// them would be a behavioural bet on the constraints, and the two checks are
// free. TestMediaSSOTDriveFileIdColumnsAreNotNull pins that reasoning: if either
// constraint is ever relaxed, that test fails and this predicate must be
// re-decided deliberately instead of silently changing the result set.
package media_test

import (
	"context"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func TestMediaDriveFileListerMatchesRetiredSQLitePredicate(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		liveWithDrive  = "yt_drivefile_live_v1"
		deletedWithDri = "yt_drivefile_deleted_v1"
		emptyDriveFile = "yt_drivefile_empty_v1"
	)
	for _, id := range []string{liveWithDrive, deletedWithDri, emptyDriveFile} {
		seedIndexableAsset(t, db, id)
	}

	// The canonical committer does not populate drive_file_id / lifecycle_state
	// for these fixtures, so set the exact shapes under test.
	for _, stmt := range []struct {
		sql string
		arg []any
	}{
		{`UPDATE media_assets SET drive_file_id = 'drive-live', lifecycle_state = 'ACTIVE' WHERE id = $1`, []any{liveWithDrive}},
		{`UPDATE media_assets SET drive_file_id = 'drive-deleted', lifecycle_state = 'DELETED' WHERE id = $1`, []any{deletedWithDri}},
		{`UPDATE media_assets SET drive_file_id = '', lifecycle_state = 'ACTIVE' WHERE id = $1`, []any{emptyDriveFile}},
	} {
		if _, err := db.ExecContext(ctx, stmt.sql, stmt.arg...); err != nil {
			t.Fatalf("shape fixture %v: %v", stmt.arg, err)
		}
	}

	ids, err := pgmedia.NewMediaDriveFileLister(db).ListAssetIDsWithDriveFileID(ctx)
	if err != nil {
		t.Fatalf("ListAssetIDsWithDriveFileID: %v", err)
	}

	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got[liveWithDrive] {
		t.Errorf("live asset with a drive_file_id must be listed; got %v", ids)
	}
	for _, id := range []string{deletedWithDri, emptyDriveFile} {
		if got[id] {
			t.Errorf("%s must NOT be listed (retired predicate excludes it); got %v", id, ids)
		}
	}
	if len(ids) != 1 {
		t.Errorf("expected exactly 1 listed asset, got %d: %v", len(ids), ids)
	}
}

// TestMediaSSOTDriveFileIdColumnsAreNotNull pins the two schema facts that make
// part of the retired SQLite predicate unreachable on PostgreSQL. The predicate
// keeps the NULL guards for byte-equivalence with the statement it replaced, but
// that is only a safe over-approximation while the columns cannot be NULL, so
// assert the constraint rather than assume it. If a future migration relaxes
// either column, this test fails and the predicate has to be re-decided
// deliberately.
func TestMediaSSOTDriveFileIdColumnsAreNotNull(t *testing.T) {
	db := newMediaTestDB(t)
	for _, column := range []string{"drive_file_id", "lifecycle_state"} {
		var isNullable string
		if err := db.QueryRow(`SELECT is_nullable FROM information_schema.columns
			WHERE table_name = 'media_assets' AND column_name = $1`, column).Scan(&isNullable); err != nil {
			t.Fatalf("read media_assets.%s nullability: %v", column, err)
		}
		if isNullable != "NO" {
			t.Errorf("media_assets.%s is_nullable = %q, want NO — the drive-file-id predicate relies on this", column, isNullable)
		}
	}
}

// TestMediaDriveFileListerFailsClosedWithoutHandle pins the no-fallback
// contract: a lister with no media handle reports an error rather than
// degrading onto a second engine. media_assets is PostgreSQL-only, so a
// silent "no assets found" would be indistinguishable from an empty SSOT —
// exactly the failure mode this migration removed.
func TestMediaDriveFileListerFailsClosedWithoutHandle(t *testing.T) {
	requirePostgresDSN(t)
	var l *pgmedia.MediaDriveFileLister
	if _, err := l.ListAssetIDsWithDriveFileID(context.Background()); err == nil {
		t.Fatal("expected a fail-closed error from a nil media drive-file lister")
	}
	// A zero-value lister (no handle) must fail the same way, never answer.
	if _, err := (&pgmedia.MediaDriveFileLister{}).ListAssetIDsWithDriveFileID(context.Background()); err == nil {
		t.Fatal("expected a fail-closed error from a lister without a media handle")
	}
}
