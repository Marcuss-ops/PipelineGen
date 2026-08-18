package asset

import (
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// IsAIImageSource reports whether source identifies an AI/generated
// image provider. URLs are never considered AI sources.
func IsAIImageSource(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return false
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return false
	}
	d, ok := DefaultProviderRegistry().Match(source)
	return ok && d.Origin == ImageOriginGenerated
}

// ClassifyImageOrigin returns the canonical ImageOrigin for a media
// source/generator pair. It is the single factory for image origin
// classification and must be used by every image metadata writer.
func ClassifyImageOrigin(source, generator string) ImageOrigin {
	if IsAIImageSource(source) || IsAIImageSource(generator) {
		return ImageOriginGenerated
	}
	if strings.EqualFold(strings.TrimSpace(source), "upload") {
		return ImageOriginUploaded
	}
	return ImageOriginRetrieved
}

// ClassifyImageProvider returns the canonical ImageProvider for a media
// source/generator pair. It is the single factory for image provider
// classification and must be used by every image metadata writer.
func ClassifyImageProvider(source, generator string) ImageProvider {
	if provider := imageProviderFromValue(source); provider != ProviderUnknown {
		return provider
	}
	return imageProviderFromValue(generator)
}

func imageProviderFromValue(value string) ImageProvider {
	d, ok := DefaultProviderRegistry().Match(value)
	if !ok {
		return ProviderUnknown
	}
	return d.ID
}

// CanonicalImageMetadataBuilder is the single place where image asset
// metadata JSON is assembled. It guarantees that the canonical "origin"
// and "provider" keys always match the computed asset provenance and are
// never silently hardcoded to "generated".
type CanonicalImageMetadataBuilder struct {
	data     ImageMetadataMap
	origin   ImageOrigin
	provider ImageProvider
}

// NewCanonicalImageMetadataBuilder derives canonical origin and provider
// from the source/generator pair up front.
func NewCanonicalImageMetadataBuilder(source, generator string) *CanonicalImageMetadataBuilder {
	origin := ClassifyImageOrigin(source, generator)
	provider := ClassifyImageProvider(source, generator)
	return &CanonicalImageMetadataBuilder{
		data: ImageMetadataMap{
			"origin":   string(origin),
			"provider": string(provider),
		},
		origin:   origin,
		provider: provider,
	}
}

// WithBaseInfo fills the canonical fallback keys produced when the
// semantic tagger is unavailable.
func (b *CanonicalImageMetadataBuilder) WithBaseInfo(description, style, hash string, tags []string, width, height int) *CanonicalImageMetadataBuilder {
	b.data["prompt_original"] = description
	b.data["semantic_description"] = ""
	b.data["style"] = style
	b.data["tags"] = tags
	b.data["content_hash"] = hash
	b.data["width"] = width
	b.data["height"] = height
	b.data["embedding_version_visual"] = defaults.VisualEmbeddingModelVersion
	return b
}

// WithGenerator records the canonical generator label in the metadata.
func (b *CanonicalImageMetadataBuilder) WithGenerator(generator string) *CanonicalImageMetadataBuilder {
	if generator != "" {
		b.data["generator"] = generator
	}
	return b
}

// WithExtra merges extra metadata keys into the canonical payload.
// origin and provider are protected: they are derived from the
// source/generator pair and cannot be overwritten by extra data.
// If extra contains a "tags" slice, it is merged with the existing
// tags.
func (b *CanonicalImageMetadataBuilder) WithExtra(extra ImageMetadataMap) *CanonicalImageMetadataBuilder {
	if extra == nil {
		return b
	}
	for k, v := range extra {
		if k == "origin" || k == "provider" {
			continue
		}
		if k == "tags" {
			switch t := v.(type) {
			case []string:
				b.data["tags"] = uniqueAppend(b.tags(), t...)
			case []any:
				b.data["tags"] = uniqueAppend(b.tags(), toStringSlice(t)...)
			default:
				b.data[k] = v
			}
			continue
		}
		b.data[k] = v
	}
	return b
}

// WithProvenance adds web-retrieval provenance keys. Empty values are
// ignored.
func (b *CanonicalImageMetadataBuilder) WithProvenance(imageURL, pageURL, sourceName, query string) *CanonicalImageMetadataBuilder {
	if imageURL != "" {
		b.data["source_image_url"] = imageURL
	}
	if pageURL != "" {
		b.data["source_page_url"] = pageURL
	}
	if sourceName != "" {
		b.data["source_name"] = sourceName
	}
	if query != "" {
		b.data["source_query"] = query
	}
	return b
}

// Build returns the JSON string and the canonical origin/provider that
// should be copied onto the asset record.
func (b *CanonicalImageMetadataBuilder) Build() (string, ImageOrigin, ImageProvider) {
	bytes, _ := json.Marshal(b.data)
	return string(bytes), b.origin, b.provider
}

// tags returns the current tags slice, if any.
func (b *CanonicalImageMetadataBuilder) tags() []string {
	v, ok := b.data["tags"].([]string)
	if !ok || v == nil {
		return nil
	}
	return v
}

// AppendImageProvenance merges retrieval provenance keys into an
// existing metadata JSON string without touching the canonical
// origin/provider fields. It returns the updated JSON or the original
// string if parsing fails.
func AppendImageProvenance(metadataJSON, imageURL, pageURL, sourceName, query string) string {
	if metadataJSON == "" || metadataJSON == "{}" {
		metadataJSON = "{}"
	}

	var payload ImageMetadataMap
	if err := json.Unmarshal([]byte(metadataJSON), &payload); err != nil {
		return metadataJSON
	}

	if imageURL != "" {
		payload["source_image_url"] = imageURL
	}
	if pageURL != "" {
		payload["source_page_url"] = pageURL
	}
	if sourceName != "" {
		payload["source_name"] = sourceName
	}
	if query != "" {
		payload["source_query"] = query
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return metadataJSON
	}
	return string(out)
}

// AppendImageMetadataField merges a single metadata field into an existing
// metadata JSON payload. It is used for orthogonal provenance fields that
// are not part of the canonical AppendImageProvenance signature.
func AppendImageMetadataField(metadataJSON, key string, value any) string {
	if metadataJSON == "" || metadataJSON == "{}" {
		metadataJSON = "{}"
	}

	var payload ImageMetadataMap
	if err := json.Unmarshal([]byte(metadataJSON), &payload); err != nil {
		return metadataJSON
	}

	payload[key] = value

	out, err := json.Marshal(payload)
	if err != nil {
		return metadataJSON
	}
	return string(out)
}

func uniqueAppend(slice []string, items ...string) []string {
	seen := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(slice)+len(items))
	out = append(out, slice...)
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

func toStringSlice(v []any) []string {
	out := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ImageMetadata is the typed value object for the provenance/identity
// fields carried by ImageAsset.MetadataJSON. It replaces ad-hoc
// map[string]any + unchecked string-key reads (primitive obsession) with
// typed field access while keeping MetadataJSON as the durable wire shape.
//
// Only the keys consumed by the application read paths are typed here;
// the canonical writer remains CanonicalImageMetadataBuilder, which owns
// the full wire contract (godlike/06 SSOT).
type ImageMetadata struct {
	Generator     string `json:"generator,omitempty"`
	SourceName    string `json:"source_name,omitempty"`
	SourceQuery   string `json:"source_query,omitempty"`
	ResolvedQuery string `json:"resolved_query,omitempty"`
}

// ImageMetadata decodes ImageAsset.MetadataJSON into the typed value
// object. Empty or malformed JSON yields the zero value, matching the
// previous map[string]any readers which silently ignored decode errors.
func (a *ImageAsset) ImageMetadata() ImageMetadata {
	if a == nil {
		return ImageMetadata{}
	}
	raw := strings.TrimSpace(a.MetadataJSON)
	if raw == "" || raw == "{}" {
		return ImageMetadata{}
	}
	var m ImageMetadata
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ImageMetadata{}
	}
	m.Generator = strings.TrimSpace(m.Generator)
	m.SourceName = strings.TrimSpace(m.SourceName)
	m.SourceQuery = strings.TrimSpace(m.SourceQuery)
	m.ResolvedQuery = strings.TrimSpace(m.ResolvedQuery)
	return m
}
