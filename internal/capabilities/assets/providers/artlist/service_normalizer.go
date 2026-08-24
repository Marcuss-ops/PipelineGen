package artlist

import (
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// existingAssetToImportResponse maps a persisted asset to the import
// response shape returned when a clip is already imported.
func existingAssetToImportResponse(a *asset.Asset) *ImportClipResponse {
	resp := &ImportClipResponse{
		OK:           true,
		ClipID:       a.ID,
		Name:         a.Name,
		ClipPageURL:  a.ClipPageURL,
		ThumbnailURL: a.ThumbnailURL,
		Status:       "already_imported",
		Tags:         a.Tags,
		Metadata:     make(map[string]any),
	}
	resp.Metadata["provider_tags"] = a.ProviderTags
	if a.Metadata != nil {
		resp.Metadata = a.Metadata
		resp.Creator, _ = a.Metadata["creator"].(string)
		resp.Country, _ = a.Metadata["country"].(string)
		resp.Location, _ = a.Metadata["location"].(string)
		resp.Categories = stringSliceFromMetadata(a.Metadata, "provider_categories")
		resp.PreviewURL, _ = a.Metadata["preview_url"].(string)
		resp.Description = a.Description()
	}
	return resp
}

// stringSliceFromMetadata safely extracts a []string from a metadata map.
// It tolerates both []string and []any (JSON round-trip) representations.
func stringSliceFromMetadata(meta map[string]any, key string) []string {
	v, ok := meta[key]
	if !ok {
		return nil
	}
	if ss, ok := v.([]string); ok {
		return ss
	}
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// candidateToAsset maps a provider-level candidate to the canonical
// asset model, preserving all Artlist-specific metadata in the JSON
// Metadata field.
func candidateToAsset(c *Candidate, clipPageURL string) *asset.Asset {
	id := c.ID
	if id == "" {
		id = extractClipIDFromURL(clipPageURL)
	}
	name := c.Title
	if name == "" {
		name = id
	}

	providerTags := make([]string, len(c.Keywords))
	copy(providerTags, c.Keywords)

	searchTerms := make([]string, 0, len(providerTags)+1)
	searchTerms = append(searchTerms, name)
	searchTerms = append(searchTerms, providerTags...)

	mediaType := c.MediaType
	if mediaType == "" {
		mediaType = asset.MediaType("video")
	}
	provider := c.Provider
	if provider == "" {
		provider = "artlist"
	}

	clip := &asset.Asset{
		ID:           id,
		Name:         name,
		Filename:     name + ".mp4",
		Source:       asset.Source("artlist"),
		MediaType:    mediaType,
		ProviderTags: providerTags,
		SearchTerms:  deduplicateStrings(searchTerms),
		SourceURL:    c.SourceRef,
		ClipPageURL:  clipPageURL,
		Metadata: map[string]any{
			"creator":             c.Creator,
			"provider_tags":       providerTags,
			"provider_categories": c.Categories,
			"metadata_origin":     "artlist",
			"external_id":         c.ExternalID,
			"provider":            provider,
			"media_type":          string(mediaType),
		},
	}
	clip.RebuildTags()
	if c.Description != "" {
		clip.SetDescription(c.Description)
	}
	if c.ThumbnailURL != "" {
		clip.ThumbnailURL = c.ThumbnailURL
	}
	if c.PreviewURL != "" {
		clip.Metadata["preview_url"] = c.PreviewURL
	}
	if c.PageURL != "" {
		clip.ClipPageURL = c.PageURL
		clip.Metadata["page_url"] = c.PageURL
	} else if clipPageURL != "" {
		clip.ClipPageURL = clipPageURL
		clip.Metadata["page_url"] = clipPageURL
	}
	if c.Duration > 0 {
		clip.Duration = c.Duration
		clip.Metadata["duration"] = c.Duration.String()
	} else if c.DurationMs > 0 {
		clip.Duration = time.Duration(c.DurationMs) * time.Millisecond
		clip.Metadata["duration"] = clip.Duration.String()
	}
	if c.Width > 0 {
		clip.Metadata["width"] = c.Width
	}
	if c.Height > 0 {
		clip.Metadata["height"] = c.Height
	}
	if c.FPSNumerator > 0 && c.FPSDenominator > 0 {
		clip.Metadata["fps"] = float64(c.FPSNumerator) / float64(c.FPSDenominator)
	}
	if c.LicenseClass != "" {
		clip.Metadata["license_class"] = c.LicenseClass
	}
	if c.CollectionID != "" {
		clip.Metadata["collection_id"] = c.CollectionID
	}
	if c.CollectionTitle != "" {
		clip.Metadata["collection_title"] = c.CollectionTitle
	}
	for k, v := range c.RawMetadata {
		clip.Metadata[k] = v
	}
	return clip
}

// extractClipIDFromURL pulls the numeric clip id from an Artlist
// detail page URL. Falls back to the full URL when no numeric id is
// present.
func extractClipIDFromURL(u string) string {
	// artlist.io/stock-footage/clip/<slug>/<id>
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return u
}
