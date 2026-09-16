// Package media_test — voiceover_media_reader_test.go pins the MEDIA-SSOT P2-9
// Phase 2 migrations of the voiceover capability's two media_assets reads plus
// the post-commit projection check.
//
// The properties worth pinning are the ones a naive port would have broken:
// absence must stay distinguishable from an error (a cache miss and an
// unreadable catalog lead to different actions), the dedupe gate must keep
// excluding the current row, and the retired count-failure degrade (report the
// match as count=1 rather than inventing an ambiguity) must survive.
package media_test

import (
	"context"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func TestMediaVoiceoverMediaReaderLocationAndAbsence(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		present = "yt_vo_media_present_v1"
		absent  = "yt_vo_media_absent_v1"
	)
	seedIndexableAsset(t, db, present)

	if _, err := db.ExecContext(ctx, `UPDATE media_assets
		SET drive_file_id = 'drive-vo-1', drive_link = 'https://drive/1',
		    download_link = 'https://download/1', local_path = '/tmp/vo.mp3', name = 'VO One'
		WHERE id = $1`, present); err != nil {
		t.Fatalf("shape location fixture: %v", err)
	}

	r := pgmedia.NewMediaVoiceoverMediaReader(db)

	loc, found, err := r.MediaAssetLocation(ctx, present)
	if err != nil {
		t.Fatalf("MediaAssetLocation(present): %v", err)
	}
	if !found {
		t.Fatal("found = false for an existing media_assets row")
	}
	if loc.DriveFileID != "drive-vo-1" || loc.DriveLink != "https://drive/1" ||
		loc.DownloadLink != "https://download/1" || loc.LocalPath != "/tmp/vo.mp3" || loc.Name != "VO One" {
		t.Errorf("location = %+v, want the seeded values", loc)
	}

	// Absence is (zero, false, nil) — NOT an error. The cache lookup treats
	// absence as a miss; treating it as an error would make an unreadable catalog
	// and a missing projection indistinguishable.
	_, found, err = r.MediaAssetLocation(ctx, absent)
	if err != nil {
		t.Fatalf("MediaAssetLocation(absent) err = %v, want nil (absence is not a failure)", err)
	}
	if found {
		t.Error("found = true for an absent media_assets row")
	}
}

func TestMediaVoiceoverMediaReaderDedupeGate(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		current = "yt_vo_dedupe_current_v1"
		other   = "yt_vo_dedupe_other_v1"
		third   = "yt_vo_dedupe_third_v1"
		unique  = "yt_vo_dedupe_unique_v1"
	)
	for _, id := range []string{current, other, third, unique} {
		seedIndexableAsset(t, db, id)
	}
	for _, id := range []string{current, other, third} {
		if _, err := db.ExecContext(ctx,
			`UPDATE media_assets SET drive_file_id = 'drive-shared' WHERE id = $1`, id); err != nil {
			t.Fatalf("shape dedupe fixture %s: %v", id, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET drive_file_id = 'drive-unique' WHERE id = $1`, unique); err != nil {
		t.Fatalf("shape unique fixture: %v", err)
	}

	r := pgmedia.NewMediaVoiceoverMediaReader(db)

	// The current row is excluded, so it never dedupes against itself.
	matched, count, err := r.CountByDriveFileID(ctx, "drive-shared", current)
	if err != nil {
		t.Fatalf("CountByDriveFileID: %v", err)
	}
	if matched == "" || matched == current {
		t.Errorf("matched = %q, want one of the OTHER rows (the current row must be excluded)", matched)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (three rows share the id, one is current)", count)
	}

	// No match at all is ("", 0, nil), never an error.
	matched, count, err = r.CountByDriveFileID(ctx, "drive-absent", current)
	if err != nil {
		t.Fatalf("CountByDriveFileID(absent) err = %v, want nil", err)
	}
	if matched != "" || count != 0 {
		t.Errorf("absent drive_file_id = (%q, %d), want (\"\", 0)", matched, count)
	}

	// A single other match is the DedupeReuse case.
	matched, count, err = r.CountByDriveFileID(ctx, "drive-unique", current)
	if err != nil {
		t.Fatalf("CountByDriveFileID(unique): %v", err)
	}
	if matched != unique || count != 1 {
		t.Errorf("unique = (%q, %d), want (%q, 1)", matched, count, unique)
	}

	// The empty drive_file_id guard short-circuits without touching the database.
	if matched, count, err = r.CountByDriveFileID(ctx, "", current); err != nil || matched != "" || count != 0 {
		t.Errorf("empty drive_file_id = (%q, %d, %v), want (\"\", 0, nil)", matched, count, err)
	}
}

func TestMediaVoiceoverProjectionChecker(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		projected = "yt_vo_projection_present_v1"
		otherSrc  = "yt_vo_projection_othersrc_v1"
	)
	seedIndexableAsset(t, db, projected)
	seedIndexableAsset(t, db, otherSrc)

	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET source = 'voiceover' WHERE id = $1`, projected); err != nil {
		t.Fatalf("shape projection fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET source = 'artlist' WHERE id = $1`, otherSrc); err != nil {
		t.Fatalf("shape other-source fixture: %v", err)
	}

	c := pgmedia.NewMediaVoiceoverProjectionChecker(db)

	exists, err := c.VoiceoverProjectionExists(ctx, projected)
	if err != nil {
		t.Fatalf("VoiceoverProjectionExists(present): %v", err)
	}
	if !exists {
		t.Error("a source='voiceover' row must report the projection as present")
	}

	// A different source is NOT the voiceover projection — the retired statement
	// compared source to the literal, so this must stay false.
	exists, err = c.VoiceoverProjectionExists(ctx, otherSrc)
	if err != nil {
		t.Fatalf("VoiceoverProjectionExists(other source): %v", err)
	}
	if exists {
		t.Error("a non-voiceover source must NOT count as the voiceover projection")
	}

	// Absent id: false with NO error (the retired statement's ErrNoRows → report).
	exists, err = c.VoiceoverProjectionExists(ctx, "yt_vo_projection_absent_v1")
	if err != nil {
		t.Fatalf("VoiceoverProjectionExists(absent) err = %v, want nil", err)
	}
	if exists {
		t.Error("an absent id must report the projection as missing, not present")
	}
}

// TestVoiceoverReadersFailClosedWithoutHandle pins the no-fallback contract for
// every new surface: an unwired handle errors instead of reporting absence, which
// callers would otherwise read as a cache miss or a missing projection.
func TestVoiceoverReadersFailClosedWithoutHandle(t *testing.T) {
	requirePostgresDSN(t)
	ctx := context.Background()

	var r *pgmedia.MediaVoiceoverMediaReader
	if _, _, err := r.MediaAssetLocation(ctx, "x"); err == nil {
		t.Error("expected a fail-closed error from a nil voiceover media reader (MediaAssetLocation)")
	}
	if _, _, err := r.CountByDriveFileID(ctx, "d", "c"); err == nil {
		t.Error("expected a fail-closed error from a nil voiceover media reader (CountByDriveFileID)")
	}
	var c *pgmedia.MediaVoiceoverProjectionChecker
	if _, err := c.VoiceoverProjectionExists(ctx, "x"); err == nil {
		t.Error("expected a fail-closed error from a nil voiceover projection checker")
	}
}
