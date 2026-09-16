// Package media — media_asset_source_reader.go: the narrow PostgreSQL read of
// one media asset's `source`.
//
// MEDIA-SSOT P2-9 Phase 2. The subtitle_ready sampler gate used to read this
// column, together with an asset_subtitle_artifacts count, in a single statement
// issued through a package-global *sql.DB bound to the OPERATIONAL SQLite store.
// Two problems compounded there:
//
//  1. media_assets.source is a media fact, and PostgreSQL owns media_assets, so
//     the gate graded the source of a row that no canonical writer maintains.
//  2. The two tables do not share an engine — asset_subtitle_artifacts exists
//     ONLY on SQLite (0 PostgreSQL tables, no PostgreSQL writer) — so the
//     statement could not simply be re-pointed at the media SSOT.
//
// This reader answers exactly the media half. The artifact count stays an
// operational read behind its own port; see
// internal/capabilities/scripts/usecase.SamplerGateDeps.
//
// Removal condition: this is a permanent media read (the gate needs the source
// to decide whether subtitles are required). It must never grow an engine
// branch.
package media

import (
	"context"
	"database/sql"
	"fmt"
)

// MediaAssetSourceReader returns the canonical `source` of one media asset.
type MediaAssetSourceReader struct {
	db *sql.DB
}

// NewMediaAssetSourceReader returns a reader bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaAssetSourceReader(db *sql.DB) *MediaAssetSourceReader {
	if db == nil {
		panic("media.NewMediaAssetSourceReader: db is required")
	}
	return &MediaAssetSourceReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaAssetSourceReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// AssetSource returns the canonical source for one asset.
//
// COALESCE matches the retired statement: a NULL source resolves to the empty
// string, which detail.RequiresSubtitles treats as "subtitles not required", so
// an unclassified source keeps the gate's historical meaning instead of turning
// it into an error.
//
// A missing row IS an error (sql.ErrNoRows), also matching the retired
// statement: an unknown candidate must fail the gate loudly rather than be
// silently classified as requiring no subtitles.
func (r *MediaAssetSourceReader) AssetSource(ctx context.Context, assetID string) (string, error) {
	if r == nil || r.db == nil {
		return "", fmt.Errorf("media asset source reader: media SSOT handle is not configured")
	}
	var source string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(source, '') FROM media_assets WHERE id = $1`,
		assetID,
	).Scan(&source)
	if err != nil {
		return "", fmt.Errorf("media asset source reader: load source for %q: %w", assetID, err)
	}
	return source, nil
}
