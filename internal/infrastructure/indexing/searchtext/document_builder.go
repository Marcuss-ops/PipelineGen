// Package searchtext provides the canonical SearchDocumentBuilder
// implementation. It assembles search text directly from an asset's
// structured fields so that the final text is deterministic and free
// of tag-contamination between ProviderTags, VLMTags, and the
// aggregated Tags list.
package searchtext

import (
	"context"
	"fmt"
	"strings"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/application/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// AssetSearchDocumentBuilder builds canonical search text from the
// asset's typed fields and metadata. It intentionally does NOT mutate
// the asset.Tags field; callers are expected to run RebuildTags()
// separately if they want to refresh the aggregated tag list.
type AssetSearchDocumentBuilder struct {
	// base allows a source-specific strategy to contribute title,
	// category, and description. When nil, the builder still emits
	// those fields itself.
	base appsearchtext.SearchTextBuilder
}

// NewAssetSearchDocumentBuilder creates a builder. Passing nil base
// is allowed and yields a metadata-only document.
func NewAssetSearchDocumentBuilder(base appsearchtext.SearchTextBuilder) *AssetSearchDocumentBuilder {
	return &AssetSearchDocumentBuilder{base: base}
}

// Build assembles the search text from the asset fields requested by
// the user plan: ProviderTags, VLMTags, description, creator,
// categories, location, scene_type, and OCR.
func (b *AssetSearchDocumentBuilder) Build(ctx context.Context, a asset.Asset) (string, error) {
	if a.ID == "" {
		return "", fmt.Errorf("searchtext.AssetSearchDocumentBuilder: asset ID must not be empty")
	}

	var parts []string

	// Source-specific base contribution (title, category, description).
	if b.base != nil {
		baseText, err := b.base.Build(ctx, appsearchtext.SearchTextInput{
			AssetID:     a.ID,
			Source:      string(a.Source),
			MediaType:   string(a.MediaType),
			Title:       a.Name,
			Description: a.GetMetadataString("description"),
			Tags:        a.Tags,
			Category:    a.Category,
		})
		if err == nil && baseText != "" {
			parts = append(parts, baseText)
		}
	} else {
		// Minimal source-agnostic prefix.
		if a.Name != "" {
			parts = append(parts, a.Name)
		}
		if a.Category != "" {
			parts = append(parts, a.Category)
		}
		if desc := a.GetMetadataString("description"); desc != "" {
			parts = append(parts, truncate(desc, maxDescriptionChars))
		}
	}

	// Structured tag slices with clear provenance.
	parts = append(parts, a.ProviderTags...)
	parts = append(parts, a.VLMTags...)

	// Free-text metadata fields.
	appendIfNonEmpty(&parts,
		a.GetMetadataString("creator"),
		a.GetMetadataString("location"),
		a.GetMetadataString("scene_type"),
		a.GetMetadataString("vlm_ocr_text"),
		a.GetMetadataString("text_on_screen"),
	)

	// Category-like lists from metadata.
	parts = append(parts, getMetadataStringSlice(a.Metadata, "provider_categories")...)
	parts = append(parts, getMetadataStringSlice(a.Metadata, "categories")...)

	return joinNonEmpty(" ", parts...), nil
}

// appendIfNonEmpty appends every non-empty, trimmed string to the slice.
func appendIfNonEmpty(out *[]string, parts ...string) {
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			*out = append(*out, p)
		}
	}
}

// getMetadataStringSlice extracts a string slice from a metadata map,
// tolerating both []string and []any JSON-decoded representations.
func getMetadataStringSlice(m asset.Metadata, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
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

// Compile-time assertion: AssetSearchDocumentBuilder satisfies the
// application-layer SearchDocumentBuilder port.
var _ appsearchtext.SearchDocumentBuilder = (*AssetSearchDocumentBuilder)(nil)
