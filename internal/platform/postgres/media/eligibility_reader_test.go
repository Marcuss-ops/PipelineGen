// Package media_test — eligibility_reader_test.go pins the MEDIA-SSOT P2-9
// Phase 2 eligibility read migration.
//
// mediaregistry used to resolve the taxonomy gate with
// `SELECT COALESCE(asset_kind, ”), COALESCE(media_type, ”) FROM media_assets
// WHERE id = ?` on whatever handle the caller held; the only production caller
// held the operational SQLite handle while PostgreSQL owned media_assets. The
// read now has a PostgreSQL implementation, so this test asserts the two
// properties that make the swap safe:
//
//   - the projection and COALESCE semantics are identical to the retired
//     statement (unclassified asset → empty strings, not an error)
//   - an unknown asset is an error (sql.ErrNoRows), so a missing row cannot be
//     mistaken for a classified-but-not-searchable one
package media_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func TestMediaEligibilityReaderReturnsCanonicalTaxonomy(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		classified   = "yt_eligibility_classified_v1"
		unclassified = "yt_eligibility_unclassified_v1"
	)
	for _, id := range []string{classified, unclassified} {
		seedIndexableAsset(t, db, id)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET asset_kind = 'stock_video', media_type = 'video' WHERE id = $1`,
		classified); err != nil {
		t.Fatalf("shape classified fixture: %v", err)
	}
	// An empty taxonomy is the fail-closed default upstream (REGISTERED), so the
	// reader must report empties rather than erroring — COALESCE parity with the
	// retired statement.
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET asset_kind = '', media_type = '' WHERE id = $1`,
		unclassified); err != nil {
		t.Fatalf("shape unclassified fixture: %v", err)
	}

	r := pgmedia.NewMediaEligibilityReader(db)

	kind, mediaType, err := r.AssetTaxonomy(ctx, classified)
	if err != nil {
		t.Fatalf("AssetTaxonomy(classified): %v", err)
	}
	if kind != "stock_video" || mediaType != "video" {
		t.Errorf("classified = (%q, %q), want (stock_video, video)", kind, mediaType)
	}

	kind, mediaType, err = r.AssetTaxonomy(ctx, unclassified)
	if err != nil {
		t.Fatalf("AssetTaxonomy(unclassified): %v", err)
	}
	if kind != "" || mediaType != "" {
		t.Errorf("unclassified = (%q, %q), want empty strings", kind, mediaType)
	}

	// Unknown asset: an error, never a zero-valued success.
	if _, _, err := r.AssetTaxonomy(ctx, "yt_eligibility_absent_v1"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("AssetTaxonomy(absent) err = %v, want sql.ErrNoRows", err)
	}
}

// TestMediaEligibilityReaderFailsClosedWithoutHandle pins the no-fallback
// contract: a reader without a media handle errors instead of answering an
// empty taxonomy, which upstream would read as the REGISTERED default and
// silently stop embedding a searchable asset.
func TestMediaEligibilityReaderFailsClosedWithoutHandle(t *testing.T) {
	requirePostgresDSN(t)
	var r *pgmedia.MediaEligibilityReader
	if _, _, err := r.AssetTaxonomy(context.Background(), "any"); err == nil {
		t.Fatal("expected a fail-closed error from a nil media eligibility reader")
	}
	if _, _, err := (&pgmedia.MediaEligibilityReader{}).AssetTaxonomy(context.Background(), "any"); err == nil {
		t.Fatal("expected a fail-closed error from a reader without a media handle")
	}
}
