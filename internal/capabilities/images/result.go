// Package catalog — result.go declares the read-only result
// shapes returned by catalog search operations.
//
// Per the July 2026 image-restructuring plan, catalog.* is the
// surface that handlers and back-office consumers query to find
// existing image assets without going through generation or
// retrieval. The territory is read-only — no creation, no
// modification, no ingest.
//
// These types are intentionally thin wrappers over the canonical
// detail.ImageAsset (defined in internal/domain/asset): a catalog
// query returns either a fully-populated asset (when callers need
// full metadata) or an AssetSummary (when callers only need
// preview info).
package images

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// CatalogSearchResult is the canonical result of a catalog search.
// Carries the matching assets + lightweight pagination metadata.
type CatalogSearchResult struct {
	// Assets are the matched image assets, ordered by relevance
	// (catalog-internal scoring) and then by ID.
	Assets []detail.ImageAsset

	// TotalCount is the unbounded match count (independent of
	// Limit on the request). Used by paginating callers.
	TotalCount int

	// NextCursor is the cursor for the next page, or empty when
	// the current page is the last. Cursor encoding is opaque to
	// callers — pass back as-is on subsequent calls.
	NextCursor string

	// FilterEcho is the normalised (de-aliased) filter that
	// produced this result. Useful for diagnostic UIs that show
	// "we matched X under filter Y".
	FilterEcho ImageFilter
}

// AssetSummary is the lightweight projection of an asset that
// catalog listings return when full metadata is unnecessary.
//
// NOTE: AssetSummary only carries fields that exist on
// detail.ImageAsset today. Style / Author / UpdatedAt are derived
// from MetadataJSON in a follow-up step (tracked separately under
// catalog-followups in architecture/issues.yaml; this is NOT the
// territory-split concern that PR-GENERATED-SEARCH-FIX closed).
type AssetSummary struct {
	Hash        string              // canonical asset hash (primary key)
	SubjectID   string              // canonical subject ID (slug)
	SlugID      string              // alias for SubjectID (kept by callers)
	PathRel     string              // relative file path on disk
	Origin      detail.ImageOrigin   // territory classification
	Provider    detail.ImageProvider // sub-classification (wikipedia, flux, etc.)
	Description string
	License     string
	CreatedAt   time.Time
	Tags        []string
	// DriveFileID is exposed at the summary layer because admin
	// UIs and audit dashboards need to verifyDrive-side presence
	// without going through the full asset payload.
	DriveFileID string
	// Style/Author/UpdatedAt are intentionally omitted at the
	// summary layer (they're tracked as future-work under
	// catalog-followups in architecture/issues.yaml, separate
	// from the territory-split concern addressed in
	// PR-GENERATED-SEARCH-FIX).
}

// SummaryFromAsset projects an detail.ImageAsset down to an
// AssetSummary. Used internally by catalog search; exposed for
// testability + callers that pre-project their own asset slice.
func SummaryFromAsset(a detail.ImageAsset) AssetSummary {
	return AssetSummary{
		Hash:        a.Hash,
		SubjectID:   a.SubjectID,
		SlugID:      a.SlugID,
		PathRel:     a.PathRel,
		Origin:      a.Origin,
		Provider:    a.Provider,
		Description: a.Description,
		License:     a.License,
		CreatedAt:   a.CreatedAt,
		Tags:        a.Tags,
		DriveFileID: a.DriveFileID,
	}
}

// SummariesFromAssets projects a slice in-place.
func SummariesFromAssets(assets []detail.ImageAsset) []AssetSummary {
	out := make([]AssetSummary, 0, len(assets))
	for _, a := range assets {
		out = append(out, SummaryFromAsset(a))
	}
	return out
}
