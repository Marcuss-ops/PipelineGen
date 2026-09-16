// Package media_test — media_asset_source_reader_test.go pins the MEDIA-SSOT
// P2-9 Phase 2 read migration for the sampler's subtitle_ready gate.
//
// The gate's media half (media_assets.source) moved off the operational SQLite
// handle; its operational half (asset_subtitle_artifacts) deliberately did not,
// because that table has no PostgreSQL home. These tests assert the media half's
// contract: an error (not an empty success) for a missing row, so an unknown
// candidate can never be silently classified as requiring no subtitles.
//
// MEASURED SCHEMA DIVERGENCE. The reader keeps the retired statement's
// `COALESCE(source, ”)` for byte-equivalence, but that guard is unreachable on
// the media SSOT: media_assets.source is declared NOT NULL (an explicit NULL is
// rejected with SQLSTATE 23502), whereas the SQLite column was nullable.
// TestMediaSSOTAssetSourceIsNotNull pins the constraint, so relaxing it fails
// here instead of silently changing what the gate classifies.
package media_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func TestMediaAssetSourceReaderReturnsCanonicalSource(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		withSource    = "yt_asset_source_present_v1"
		blankSource   = "yt_asset_source_blank_v1"
		unknownSource = "yt_asset_source_absent_v1"
	)
	for _, id := range []string{withSource, blankSource} {
		seedIndexableAsset(t, db, id)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET source = 'youtube' WHERE id = $1`, withSource); err != nil {
		t.Fatalf("shape source fixture: %v", err)
	}
	// An explicitly empty source is the closest reachable form of the retired
	// statement's NULL case, and is what detail.RequiresSubtitles maps to
	// "subtitles not required".
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET source = '' WHERE id = $1`, blankSource); err != nil {
		t.Fatalf("shape blank-source fixture: %v", err)
	}

	r := pgmedia.NewMediaAssetSourceReader(db)

	got, err := r.AssetSource(ctx, withSource)
	if err != nil {
		t.Fatalf("AssetSource(present): %v", err)
	}
	if got != "youtube" {
		t.Errorf("source = %q, want %q", got, "youtube")
	}

	got, err = r.AssetSource(ctx, blankSource)
	if err != nil {
		t.Fatalf("AssetSource(blank): %v", err)
	}
	if got != "" {
		t.Errorf("source = %q, want empty", got)
	}

	// Unknown asset: an error, never an empty success.
	if _, err := r.AssetSource(ctx, unknownSource); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("AssetSource(absent) err = %v, want sql.ErrNoRows", err)
	}
}

// TestMediaSSOTAssetSourceIsNotNull pins the constraint that makes the reader's
// COALESCE guard unreachable. The guard is kept for byte-equivalence with the
// retired SQLite statement, which is only a safe over-approximation while the
// column cannot be NULL; if a migration relaxes it, this test fails and the
// classification has to be re-decided deliberately.
func TestMediaSSOTAssetSourceIsNotNull(t *testing.T) {
	db := newMediaTestDB(t)
	var isNullable string
	if err := db.QueryRow(`SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'media_assets' AND column_name = 'source'`).Scan(&isNullable); err != nil {
		t.Fatalf("read media_assets.source nullability: %v", err)
	}
	if isNullable != "NO" {
		t.Errorf("media_assets.source is_nullable = %q, want NO — the reader's COALESCE guard relies on this", isNullable)
	}
}

// TestMediaAssetSourceReaderFailsClosedWithoutHandle pins the no-fallback
// contract: an unwired reader errors instead of answering "" — which the gate
// would read as "subtitles not required" and pass a clip it never verified.
func TestMediaAssetSourceReaderFailsClosedWithoutHandle(t *testing.T) {
	requirePostgresDSN(t)
	var r *pgmedia.MediaAssetSourceReader
	if _, err := r.AssetSource(context.Background(), "any"); err == nil {
		t.Fatal("expected a fail-closed error from a nil media asset source reader")
	}
	if _, err := (&pgmedia.MediaAssetSourceReader{}).AssetSource(context.Background(), "any"); err == nil {
		t.Fatal("expected a fail-closed error from a reader without a media handle")
	}
}
