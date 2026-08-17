// Package clipindexer — eligibility.go: the single IndexEligibilityResolver.
//
// Registered ≠ searchable. This resolver reads the asset's canonical taxonomy
// dimensions (media_assets.asset_kind + media_type, migration 195) and returns
// the searchability decision from the canonical policy
// (mediaregistry.AssetTaxonomy.IndexEligibility). The MediaIndexer consults it
// before any embedding work, so voiceover / final_audio / bgm / sfx stay
// REGISTERED without being projected into Qdrant.
package clipindexer

import (
	"context"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// errTaxonomySchemaUnavailable identifies a pre-migration media_assets table.
// It is the only eligibility error that IndexAsset may bridge to the legacy
// indexing path; ordinary registry read failures remain fail-closed.
var errTaxonomySchemaUnavailable = fmt.Errorf("taxonomy schema unavailable")

// Compile-time assertions.
var _ MediaIndexer = (*Service)(nil)
var _ IndexEligibilityResolver = (*Service)(nil)

// Eligibility resolves the searchability decision for an asset from its
// canonical taxonomy dimensions. A missing row or an empty taxonomy (asset
// predating migration 195) resolves to REGISTERED, which is the fail-closed
// default: an asset is only SEARCHABLE once it is explicitly classified as
// video/image.
func (s *Service) Eligibility(ctx context.Context, assetID string) (capregistry.IndexEligibility, error) {
	var assetKind, mediaType string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(asset_kind, ''), COALESCE(media_type, '') FROM media_assets WHERE id = ?`,
		assetID,
	).Scan(&assetKind, &mediaType)
	if err != nil {
		if strings.Contains(err.Error(), "no such column: asset_kind") || strings.Contains(err.Error(), "no such column: media_type") {
			return capregistry.IndexEligibilityRegistered, fmt.Errorf("%w: %v", errTaxonomySchemaUnavailable, err)
		}
		return capregistry.IndexEligibilityRegistered, err
	}
	return capregistry.AssetTaxonomy{
		AssetKind: capregistry.AssetKind(assetKind),
		MediaType: capregistry.MediaType(mediaType),
	}.IndexEligibility(), nil
}
