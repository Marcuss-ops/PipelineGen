// Package images (api/images) — types_search.go declares the
// unified ImageSearchResult DTO returned by the Step-10 territory-
// separated search endpoints.
//
// One DTO shape serves both /api/images/retrieved/search and
// /api/images/generated/search + the aggregator /api/images/search.
// Callers see the same fields regardless of which territory they
// queried.
//
// Per the July 2026 image-restructuring plan, the territory is
// surfaced as a top-level Origin field so callers don't have to
// distinguish response shapes — a single ImageSearchResult can
// have Origin="retrieved" or "generated" and the body shape stays
// identical.
//
// Field semantics:
//   - AssetID: canonical identifier for the matching image asset.
//     Today this maps to detail.ImageAsset.Hash (the
//     SHA256 of the file content). Future revisions may
//     use a UUID; the docs are pinned to Hash for now.
//   - Origin:  detail.ImageOrigin canonical constant
//     ("retrieved" | "generated" | "uploaded").
//   - Provider: detail.ImageProvider constant
//     ("wikipedia" | "duckduckgo" | "google-slides" |
//     "flux" | "nvidia" | ...).
//   - PreviewURL: external or relative URL for inline rendering.
//     Retrieved images: source_image_url or PathRel.
//     Generated images: source URL (GoogleSlides output)
//     or local PathRel.
//   - StyleID: registered style identifier (cinematic, anime, ...).
//     Empty when the asset was retrieved (no style context).
//     Generated assets always carry a style.
//   - License: license tag carried from the source (Wikipedia
//     returns "CC-BY-SA-4.0"; generated returns empty).
//   - Author: human-readable attribution (Wikipedia returns
//     "Wikipedia Contributors"; generated returns empty).
package images

// ImageSearchResult is the unified response DTO for territory-
// separated search. Same shape for retrieved, generated, and
// the aggregated /search?territory=all endpoints.
type ImageSearchResult struct {
	AssetID           string  `json:"asset_id"`
	Origin            string  `json:"origin"`
	Provider          string  `json:"provider"`
	Name              string  `json:"name,omitempty"`
	PreviewURL        string  `json:"preview_url"`
	DriveLink         string  `json:"drive_link,omitempty"`
	LegacyFileMD5     string  `json:"legacy_file_md5,omitempty"`
	SourcePageURL     string  `json:"source_page_url,omitempty"`
	Width             int     `json:"width,omitempty"`
	Height            int     `json:"height,omitempty"`
	Score             float64 `json:"score,omitempty"`
	StyleID           string  `json:"style_id,omitempty"`
	StyleVersion      string  `json:"style_version,omitempty"`
	License           string  `json:"license,omitempty"`
	Author            string  `json:"author,omitempty"`
	CacheHit          *bool   `json:"cache_hit,omitempty"`
	CacheSource       string  `json:"cache_source,omitempty"`
	RetrievalProvider string  `json:"retrieval_provider,omitempty"`
}

// ImageSearchResults is the canonical response envelope — an
// array of results + a count. Wrapping the array in an object
// leaves room for future fields (pagination, query echo).
type ImageSearchResults struct {
	Results []ImageSearchResult `json:"results"`
	Count   int                 `json:"count"`
}

// StyleInfo is the unified DTO for GET /api/images/generated/styles.
// Mirrors GenerationStyle fields the admin UI needs.
type StyleInfo struct {
	StyleID        string `json:"style_id"`
	Name           string `json:"name"`
	Version        int    `json:"version"`
	PromptSuffix   string `json:"prompt_suffix,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	DestinationKey string `json:"destination_key,omitempty"`
	Enabled        bool   `json:"enabled"`
}

// StyleInfo canonical shape (surface-3, July 2026, mirror of
// domain/asset.GenerationStyle minus the per-style allowlist
// fields). AllowedProviders / AllowedModels were retired from the
// API DTO once google-slides became the sole image-generation
// provider (commit d54728dc — surface 1 cut). The canonical
// GenerationStyle fields stay loadable from yaml for back-compat
// but are no longer exposed to callers via the admin styles
// endpoint.

// StylesResponse is the response envelope for the styles endpoint.
type StylesResponse struct {
	Styles []StyleInfo `json:"styles"`
	Count  int         `json:"count"`
}
