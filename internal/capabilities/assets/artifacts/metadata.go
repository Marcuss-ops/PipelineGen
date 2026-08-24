package assets

import (
	"encoding/json"
	"strings"
)

type defaultMetadataPort struct{}

func (defaultMetadataPort) MetadataMapFromJSON(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return m
}
func (defaultMetadataPort) MetadataMapToJSON(meta map[string]any) string {
	if meta == nil {
		return "{}"
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(data)
}
func (defaultMetadataPort) MergeMetadataSearchText(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
func (defaultMetadataPort) AssetTypeForMediaType(mediaType string) string {
	switch mediaType {
	case "image", "photo", "picture":
		return "image"
	case "video", "clip":
		return "video"
	case "audio", "sound", "music":
		return "audio"
	case "document", "text":
		return "document"
	default:
		return mediaType
	}
}
func (defaultMetadataPort) BuildAssetMetadata(input MetadataInput, existing map[string]any) map[string]any {
	meta := make(map[string]any, len(existing)+8)
	for k, v := range existing {
		meta[k] = v
	}
	put := func(key, value string) {
		if value != "" {
			meta[key] = value
		}
	}
	put("asset_id", input.AssetID)
	put("asset_type", input.AssetType)
	put("source", input.Source)
	put("media_type", input.MediaType)
	put("generator", input.Generator)
	put("prompt_original", input.PromptOriginal)
	put("semantic_description", input.SemanticDescription)
	put("search_text", input.SearchText)
	if len(input.Subjects) > 0 {
		meta["subjects"] = input.Subjects
	}
	if len(input.SubjectSlugs) > 0 {
		meta["subject_slugs"] = input.SubjectSlugs
	}
	if len(input.Tags) > 0 {
		meta["tags"] = input.Tags
	}
	if len(input.Categories) > 0 {
		meta["categories"] = input.Categories
	}
	if len(input.Mood) > 0 {
		meta["mood"] = input.Mood
	}
	if len(input.Style) > 0 {
		meta["style"] = input.Style
	}
	if input.Confidence != 0 {
		meta["confidence"] = input.Confidence
	}
	put("embedding_status", input.EmbeddingStatus)
	put("visual_embedding_json", input.VisualEmbeddingJSON)
	put("phash", input.PHash)
	if input.VisualDimensions != 0 {
		meta["visual_dimensions"] = input.VisualDimensions
	}
	if len(input.Assets) > 0 {
		meta["assets"] = input.Assets
	}
	for k, v := range input.Extra {
		meta[k] = v
	}
	return meta
}
func defaultMetadata() MetadataPort { return defaultMetadataPort{} }
