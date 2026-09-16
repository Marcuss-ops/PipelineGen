// Package media — eligibility_reader.go: the narrow PostgreSQL read surface
// behind the canonical index-eligibility decision.
//
// MEDIA-SSOT P2-9 Phase 2: this replaces the
// `SELECT COALESCE(asset_kind, ”), COALESCE(media_type, ”) FROM media_assets
// WHERE id = ?` statement that internal/capabilities/mediaregistry ran against
// the operational SQLite handle. PostgreSQL is the media SSOT, so the taxonomy
// gate must grade the SSOT; grading the operational mirror let an asset be
// declared searchable (or not) from a row that no canonical writer maintains.
//
// The projection is deliberately two columns. That is all the eligibility
// policy consumes, so the port stays narrow instead of widening the shared
// media read model — narrow ports are how this package avoids becoming a second
// schema owner.
//
// Scope note: unlike the reindex path (see reindex_requester.go, which adds
// `AND deleted_at = ”` because it is about to emit work), this read keeps the
// retired statement's exact predicate — id match only, no deleted filter. The
// eligibility decision is a taxonomy classification, and the legacy statement
// classified soft-deleted rows identically; adding a filter here would silently
// change which assets are graded as searchable.
//
// This reader answers over PostgreSQL ONLY. It has no SQLite branch and never
// falls back: a closed media plane must surface as an error to the caller.
package media

import (
	"context"
	"database/sql"
	"fmt"
)

// MediaEligibilityReader reads the canonical taxonomy dimensions of a media
// asset. It satisfies the consumer-owned mediaregistry.AssetEligibilityReader
// port structurally, so this package does not import the capability.
type MediaEligibilityReader struct {
	db *sql.DB
}

// NewMediaEligibilityReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaEligibilityReader(db *sql.DB) *MediaEligibilityReader {
	if db == nil {
		panic("media.NewMediaEligibilityReader: db is required")
	}
	return &MediaEligibilityReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT (see the media read-bridge probes).
func (r *MediaEligibilityReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// AssetTaxonomy returns the canonical asset_kind and media_type for one asset.
//
// Both columns are coalesced to the empty string, matching the retired SQLite
// statement: an asset with no taxonomy resolves to the fail-closed REGISTERED
// default upstream rather than erroring. A missing row is an error (sql.ErrNoRows),
// also matching the retired statement, so an unknown asset never reads as a
// classified-but-not-searchable one.
func (r *MediaEligibilityReader) AssetTaxonomy(ctx context.Context, assetID string) (string, string, error) {
	if r == nil || r.db == nil {
		return "", "", fmt.Errorf("media eligibility reader: media SSOT handle is not configured")
	}
	var assetKind, mediaType string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(asset_kind, ''), COALESCE(media_type, '') FROM media_assets WHERE id = $1`,
		assetID,
	).Scan(&assetKind, &mediaType)
	if err != nil {
		return "", "", fmt.Errorf("media eligibility reader: load taxonomy for %q: %w", assetID, err)
	}
	return assetKind, mediaType, nil
}
