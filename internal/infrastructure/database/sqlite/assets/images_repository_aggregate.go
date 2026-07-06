// Package assets — images_repository_aggregate.go: aggregate/list-by-origin operations.
//
// Extracted from images_repository.go (July 2026, PR-IMAGES-REPO-SPLIT).
// Owns: ListImagesByOrigin (with limit constants) + ListAll.
package assets

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Origin-based list constants ─────────────────────────────────────────────

// ListImagesByOriginDefaultLimit is the canonical default limit for
// the generated-territory read seam. Per PR-GENERATED-SEARCH-FIX
// spec (July 2026, Blocco 1 of cut-false-success-first), the
// value is 200; the cap is also 200 (the handler does NOT need to
// query "more than the default" today).
const ListImagesByOriginDefaultLimit = 200

// ListImagesByOriginMaxLimit is the hard cap on the generated-territory
// read seam. The cap equals the default so all callers get the same
// result set; this is a godlike/07 minimum-blast-radius surface —
// future relax-the-cap decisions MUST be reviewed in light of the
// response-envelope payload size (each ImageSearchResult DTO is
// ~500 bytes; 200 rows = ~100 KB JSON payload, well under 1 MB).
const ListImagesByOriginMaxLimit = 200

// ── Origin-based list ───────────────────────────────────────────────────────

// ListImagesByOrigin returns all image media_assets rows with the
// specified origin (e.g. asset.ImageOriginGenerated), ordered by
// created_at DESC. The default limit is 200; the hard cap is also
// 200 (per PR-GENERATED-SEARCH-FIX spec, July 2026). Used as the
// canonical read-only entry for the generated territory at
// GET /api/images/generated/search.
//
// godlike/07 minimum-blast-radius surface: callers asking for
// `limit=1000` get 200 rows, callers passing limit=0 get the canonical
// default of 200, callers passing negative values get 200 (defensive
// — negative limits would be a SQL driver-error class).
//
// godlike/06 SSOT one-canonical-owner-per-fact: this is the SOLE
// canonical read seam for origin-based image queries. The handler
// at internal/api/images/territory_handlers.go::GeneratedSearch is
// the SOLE production caller today; future callers (CLI tools,
// admin commands) MUST route through this method to preserve the
// limit cap + ordering invariants. The thin-delegate on
// *imgservice.Service is the application-layer envelope; the port
// interface GeneratedSearchServicePort is the structural contract
// for adapter injection.
func (r *ImagesRepository) ListImagesByOrigin(ctx context.Context, origin asset.ImageOrigin, limit int) ([]asset.ImageAsset, error) {
	if r == nil {
		return nil, nil
	}
	if limit <= 0 || limit > ListImagesByOriginMaxLimit {
		limit = ListImagesByOriginMaxLimit
	}
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image' AND origin = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := r.db.QueryContext(ctx, query, string(origin), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []asset.ImageAsset
	for rows.Next() {
		img, err := scanImageAssetFromRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, *img)
	}
	return images, rows.Err()
}

// ── List all ────────────────────────────────────────────────────────────────

// ListAll lists all image assets.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) ListAll(ctx context.Context) ([]*asset.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image'
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []*asset.ImageAsset
	for rows.Next() {
		img, err := scanImageAssetFromRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}
