// Package media_test — media_clip_resolver_test.go pins the PostgreSQL parity
// for the two resolver arms that replaced the retired SQLite
// ClipsRepository methods (MEDIA LEGACY READ-PLANE DEMOLITION, 2026-09-20):
//
//	ResolveByYouTubeVideoID      — the `yt_<videoID>_%` fan-out
//	ResolveByExternalProviderID  — google_drive → drive_file_id,
//	                               otherwise source + the metadata
//	                               external_id projection
//
// The properties worth pinning are the ones a naive port would have broken:
// the fan-out must be ordered by id (the caller indexes into it), a
// soft-deleted member must never surface as resolved evidence, absence must
// stay an empty result rather than an error, and the provider switch must
// route exactly as the retired SQLite form did.
//
// Live-PostgreSQL test: gated behind TEST_POSTGRES_DSN (see testmain_test.go);
// skipped, never faked, when the DSN is unset (godlike/07).
package media_test

import (
	"context"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// The fixtures below INSERT directly into media_assets (rather than committing
// through the canonical committer) so the test controls the exact identity
// columns each arm matches on — including ids the committer's own derivation
// would never produce.
func TestMediaSearcher_ResolveByYouTubeVideoID(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const vid = "dQw4w9WgXcQ"
	for _, id := range []string{"yt_" + vid + "_0", "yt_" + vid + "_10", "yt_otherVideo_0"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO media_assets (id, source, name, lifecycle_state)
			VALUES ($1, 'youtube', $2, 'ACTIVE')`, id, id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	// A soft-deleted member must never surface as resolved evidence — the
	// retired SQLite form applied SoftDeleteFilter() here.
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'DELETED' WHERE id = $1`, "yt_"+vid+"_10"); err != nil {
		t.Fatalf("soft-delete fixture: %v", err)
	}

	searcher := pgmedia.NewMediaSearcher(db)

	got, err := searcher.ResolveByYouTubeVideoID(ctx, vid)
	if err != nil {
		t.Fatalf("ResolveByYouTubeVideoID: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("fan-out: want exactly 1 row (deleted member excluded), got %d", len(got))
	}
	if got[0].ID != "yt_"+vid+"_0" {
		t.Errorf("fan-out id: want %q, got %q", "yt_"+vid+"_0", got[0].ID)
	}

	// Empty video id is a no-op, NOT an unbounded `LIKE 'yt__%'` scan.
	empty, err := searcher.ResolveByYouTubeVideoID(ctx, "   ")
	if err != nil {
		t.Fatalf("empty video id must not error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty video id must resolve to no rows; got %d", len(empty))
	}
}

func TestMediaSearcher_ResolveByExternalProviderID(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		driveAsset = "asset-ext-drive"
		driveID    = "drive-ext-1"
		artAsset   = "asset-ext-artlist"
		artExtID   = "art-42"
		otherAsset = "asset-ext-artlist-other"
		goneAsset  = "asset-ext-gone"
	)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, lifecycle_state, drive_file_id, metadata_json)
		VALUES
		  ($1, 'youtube', 'Drive asset', 'ACTIVE', $2, ''),
		  ($3, 'artlist', 'Artlist asset', 'ACTIVE', '', '{"external_id":"art-42"}'),
		  ($4, 'artlist', 'Artlist other', 'ACTIVE', '', '{"external_id":"art-99"}'),
		  ($5, $6, 'Deleted asset', 'ACTIVE', '', '{"external_id":"art-42"}')`,
		driveAsset, driveID, artAsset, otherAsset, goneAsset, "artlist"); err != nil {
		t.Fatalf("seed external-provider fixtures: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'DELETED' WHERE id = $1`, goneAsset); err != nil {
		t.Fatalf("soft-delete fixture: %v", err)
	}

	searcher := pgmedia.NewMediaSearcher(db)

	// (a) google_drive routes through the canonical drive_file_id column.
	byDrive, err := searcher.ResolveByExternalProviderID(ctx, "google_drive", driveID)
	if err != nil {
		t.Fatalf("ResolveByExternalProviderID(google_drive): %v", err)
	}
	if len(byDrive) != 1 || byDrive[0].ID != driveAsset {
		t.Fatalf("google_drive arm: want exactly [%s], got %d row(s)", driveAsset, len(byDrive))
	}

	// (b) every other provider routes through source + metadata external_id.
	byArt, err := searcher.ResolveByExternalProviderID(ctx, "artlist", artExtID)
	if err != nil {
		t.Fatalf("ResolveByExternalProviderID(artlist): %v", err)
	}
	if len(byArt) != 1 || byArt[0].ID != artAsset {
		t.Fatalf("artlist arm: want exactly [%s] (deleted twin excluded, other external_id excluded), got %d row(s)", artAsset, len(byArt))
	}

	// (c) a provider with no matching external_id is an empty result, not an error.
	miss, err := searcher.ResolveByExternalProviderID(ctx, "artlist", "no-such-external-id")
	if err != nil {
		t.Fatalf("no-match must not error: %v", err)
	}
	if len(miss) != 0 {
		t.Fatalf("no-match must resolve to zero rows; got %d", len(miss))
	}

	// (d) empty provider or external id is a no-op (no unbounded scan).
	for _, tc := range []struct{ provider, external string }{
		{"", "art-42"},
		{"artlist", "   "},
	} {
		got, err := searcher.ResolveByExternalProviderID(ctx, tc.provider, tc.external)
		if err != nil {
			t.Fatalf("ResolveByExternalProviderID(%q, %q) must not error: %v", tc.provider, tc.external, err)
		}
		if len(got) != 0 {
			t.Fatalf("ResolveByExternalProviderID(%q, %q) must resolve to zero rows; got %d",
				tc.provider, tc.external, len(got))
		}
	}
}
