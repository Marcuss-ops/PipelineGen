// Package mediaregistry — index_eligibility_resolver.go: the single
// SQL-backed resolver for the canonical index-eligibility policy.
//
// "Registered" is not "searchable": this resolver reads an asset's canonical
// taxonomy dimensions (media_assets.asset_kind + media_type, migration 195)
// and returns the searchability decision from AssetTaxonomy.IndexEligibility.
// The MediaIndexer consults it before any embedding work, so voiceover /
// final_audio / bgm / sfx stay REGISTERED without being projected into Qdrant.
package mediaregistry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// RowQuerier is the narrow query surface ResolveIndexEligibility needs.
// *storage.SQLiteDB satisfies it via its embedded *sql.DB.
type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrTaxonomySchemaUnavailable identifies a pre-migration media_assets table.
// It is the only eligibility error that IndexAsset may bridge to the legacy
// indexing path; ordinary registry read failures remain fail-closed.
var ErrTaxonomySchemaUnavailable = fmt.Errorf("taxonomy schema unavailable")

// ResolveIndexEligibility resolves the searchability decision for an asset
// from its canonical taxonomy dimensions. A missing row or an empty taxonomy
// (asset predating migration 195) resolves to REGISTERED, which is the
// fail-closed default: an asset is only SEARCHABLE once it is explicitly
// classified as video/image.
func ResolveIndexEligibility(ctx context.Context, q RowQuerier, assetID string) (IndexEligibility, error) {
	var assetKind, mediaType string
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(asset_kind, ''), COALESCE(media_type, '') FROM media_assets WHERE id = ?`,
		assetID,
	).Scan(&assetKind, &mediaType)
	if err != nil {
		if strings.Contains(err.Error(), "no such column: asset_kind") || strings.Contains(err.Error(), "no such column: media_type") {
			return IndexEligibilityRegistered, fmt.Errorf("%w: %v", ErrTaxonomySchemaUnavailable, err)
		}
		return IndexEligibilityRegistered, err
	}
	return AssetTaxonomy{
		AssetKind: AssetKind(assetKind),
		MediaType: MediaType(mediaType),
	}.IndexEligibility(), nil
}
