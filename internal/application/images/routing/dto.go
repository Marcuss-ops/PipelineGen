// Package routing — ImageSearchResolver routing layer (July 2026,
// image-territories action plan FASE 6).
package routing

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

type ImageOrigin string

const (
	OriginRetrieved ImageOrigin = "retrieved"
	OriginGenerated ImageOrigin = "generated"
)

// ImageSearchResult is the common DTO returned by every searcher.
type ImageSearchResult struct {
	AssetID       string
	Origin        ImageOrigin
	Provider      string
	Name          string
	PreviewURL    string
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
