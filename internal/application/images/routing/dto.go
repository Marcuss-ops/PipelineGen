// Package routing — ImageSearchResolver routing layer (July 2026,
// image-territories action plan FASE 6).
//
// ImageOrigin (FASE 7 image-territories cleanup, July 2026): re-exported
// from internal/kernel/asset/image_taxonomy.go via a Go 1.9+ type
// alias. This collapses the previously-duplicate defined-type
// `routing.ImageOrigin string` into a single canonical type identity
// with `asset.ImageOrigin` so:
//
//   - `var x []routing.ImageOrigin` IS the same type as
//     `var y []asset.ImageOrigin` — no cast loop at composition seams.
//   - The canonical enum set (Retrieved / Generated / Uploaded) lives
//     at `internal/kernel/asset/image_taxonomy.go` and is the SSOT
//     per godlike/06 "one owner per fact". `routing.OriginRetrieved`
//     and `routing.OriginGenerated` are REMOVED — callers must use
//     `asset.ImageOriginRetrieved` / `asset.ImageOriginGenerated`.
//
// The composition-root adapter at internal/app/build_bundles_domain.go
// no longer needs the element-by-element cast loop because the alias
// unifies the types; pure type-correctness compiles the call sites
// that were previously blocked by `cannot use []routing.ImageOrigin as
// []asset.ImageOrigin`.
package routing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type ImageSearchTerritory string

const (
	TerritoryRetrieved ImageSearchTerritory = "retrieved"
	TerritoryGenerated ImageSearchTerritory = "generated"
	TerritoryAll       ImageSearchTerritory = "all"
)

func (t ImageSearchTerritory) IsValid() bool {
	switch t {
	case TerritoryRetrieved, TerritoryGenerated, TerritoryAll:
		return true
	default:
		return false
	}
}

// ImageOrigin is an alias for the canonical asset.ImageOrigin enum
// declared at internal/kernel/asset/image_taxonomy.go. Go 1.9+ type
// alias — same identity, so []routing.ImageOrigin and
// []asset.ImageOrigin are interchangeable at every callsite. One owner
// per fact: domain/asset. Existing routes that referenced the
// pre-alias `routing.OriginRetrieved` / `routing.OriginGenerated`
// constants have been moved to asset.ImageOriginRetrieved /
// asset.ImageOriginGenerated (the canonical names).
type ImageOrigin = asset.ImageOrigin

// ImageSearchResult is the common DTO returned by every searcher.
type ImageSearchResult struct {
	AssetID       string
	Origin        ImageOrigin
	Provider      string
	Name          string
	PreviewURL    string
	DriveLink     string
	LegacyFileMD5 string
	SourcePageURL string
	Width         int
	Height        int
	Score         float64
	StyleID       string
	StyleVersion  string
	License       string
	Author        string
}

// ImageFilter is the cross-territory filter for ImageSearcher.Search.
type ImageFilter struct {
	SubjectID string
	Origins   []ImageOrigin
	Providers []string
	StyleIDs  []string
	Tags      []string
	Limit     int
}

const DefaultLimit = 50
const MaxListImagesLimit = 500

func ResolvedLimit(l int) int {
	if l <= 0 {
		return DefaultLimit
	}
	if l > MaxListImagesLimit {
		return MaxListImagesLimit
	}
	return l
}
