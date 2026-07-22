package images

import (
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// CanonicalImageMetadataBuilder is the single place where image asset
// metadata JSON is assembled. It guarantees that the canonical "origin"
// and "provider" keys always match the computed asset provenance and are
// never silently hardcoded to "generated".
type CanonicalImageMetadataBuilder struct {
	data     map[string]any
	origin   asset.ImageOrigin
	provider asset.ImageProvider
}

// NewCanonicalImageMetadataBuilder derives canonical origin and provider
// from the source/generator pair up front.
func NewCanonicalImageMetadataBuilder(source, generator string) *CanonicalImageMetadataBuilder {
	origin := classifyImageOrigin(source, generator)
	provider := classifyImageProvider(source, generator)
	return &CanonicalImageMetadataBuilder{
		data: map[string]any{
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

// WithSemanticPayload merges the semantic tagger payload into the
// metadata using the canonical snake_case key map from the semantic
// package. Origin and provider are never allowed to be overwritten by
// the payload; they are the single source of truth derived from the
// source/generator classification.
func (b *CanonicalImageMetadataBuilder) WithSemanticPayload(payload *semantic.Payload) *CanonicalImageMetadataBuilder {
	if payload == nil {
		return b
	}

	input := semantic.AssetSemanticInput{
		AssetID:             payload.AssetID,
		PromptOriginal:      payload.PromptOriginal,
		SemanticDescription: payload.SemanticDescription,
		SearchText:          payload.SearchText,
		Subjects:            payload.Subjects,
		Tags:                payload.Tags,
		Categories:          payload.Categories,
		Mood:                payload.Mood,
		Style:               payload.Style,
	}
	payloadMeta := semantic.BuildAssetMetadata(input, nil)
	for k, v := range payloadMeta {
		if k == "origin" || k == "provider" || k == "tags" {
			continue
		}
		b.data[k] = v
	}

	// Fields not yet covered by semantic.BuildAssetMetadata.
	if len(payload.ConceptTags) > 0 {
		b.data["concept_tags"] = payload.ConceptTags
	}
	if len(payload.VisualObjects) > 0 {
		b.data["visual_objects"] = payload.VisualObjects
	}
	if len(payload.EmotionalTone) > 0 {
		b.data["emotional_tone"] = payload.EmotionalTone
	}
	if payload.RetrievalScore != nil {
		b.data["retrieval_score"] = *payload.RetrievalScore
	}

	if len(payload.Tags) > 0 {
		existingTags := b.tags()
		b.data["tags"] = uniqueAppend(existingTags, payload.Tags...)
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
func (b *CanonicalImageMetadataBuilder) Build() (string, asset.ImageOrigin, asset.ImageProvider) {
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

	var payload map[string]any
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
