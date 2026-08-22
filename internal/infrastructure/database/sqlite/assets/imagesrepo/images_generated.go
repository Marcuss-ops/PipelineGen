// Package assets — generated/retrieved image detail operations.
//
// images_generated.go owns the generated/retrieved detail methods:
// GetGeneratedDetails, GetRetrievedDetails, ListImagesByOrigin + constants.
// Extracted from images_repository.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package imagesrepo

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

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

// ListImagesByOrigin returns all image media_assets rows with the
// specified origin (e.g. asset.ImageOriginGenerated), ordered by
// created_at DESC. The default limit is 200; the hard cap is also
// 200 (per PR-GENERATED-SEARCH-FIX spec, July 2026).
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
		SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider
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

	images := make([]asset.ImageAsset, 0)
	for rows.Next() {
		img, err := scanImageAssetFromRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, *img)
	}
	return images, rows.Err()
}

// GetGeneratedDetails returns the per-asset generated_image_details row
// for the given asset_id, or (nil, nil) iff no row exists. The (nil, nil)
// branch mirrors the LEFT-OUTER-JOIN semantics that FASE 4B BACKFILL
// relies on for pre-FASE-4 legacy rows.
//
// FASE 4A EXPAND (July 2026, image-territories action plan): the read
// path is separate from the GetImage* methods. BACKFILL (4B) will JOIN
// this row in via LEFT OUTER; CUTOVER (4C) will invert precedence.
// CONTRACT (4D, deferred) will drop this standalone method once JOIN
// reads become canonical.
func (r *ImagesRepository) GetGeneratedDetails(ctx context.Context, assetID string) (*asset.GeneratedImageDetail, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, prompt_original, prompt_resolved, style_id, style_version,
		       model, seed, generation_job_id, source_hash
		FROM generated_image_details
		WHERE asset_id = ?
	`, assetID)
	var d asset.GeneratedImageDetail
	err := row.Scan(&d.AssetID, &d.PromptOriginal, &d.PromptResolved,
		&d.StyleID, &d.StyleVersion, &d.Model, &d.Seed,
		&d.GenerationJobID, &d.SourceHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetRetrievedDetails mirrors GetGeneratedDetails for the
// retrieved_image_details row. Same (nil, nil) semantics for pre-FASE-4
// legacy rows.
func (r *ImagesRepository) GetRetrievedDetails(ctx context.Context, assetID string) (*asset.RetrievedImageDetail, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, source_image_url, source_page_url, license, author,
		       search_query, retrieved_at, provider
		FROM retrieved_image_details
		WHERE asset_id = ?
	`, assetID)
	var d asset.RetrievedImageDetail
	err := row.Scan(&d.AssetID, &d.SourceImageURL, &d.SourcePageURL,
		&d.License, &d.Author, &d.SearchQuery, &d.RetrievedAt,
		&d.Provider)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
