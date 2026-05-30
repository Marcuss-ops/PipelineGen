package semantic

import (
	"encoding/json"
	"fmt"
	"strings"
)

type AssetSemanticInput struct {
	AssetID             string
	AssetType           string
	Source              string
	MediaType           string
	Generator           string
	PromptOriginal      string
	SemanticDescription string
	SearchText          string
	// LLM-generated enriched fields (produced at ingest time, stored in DB)
	ConceptTags         []string // synonym-expanded concepts
	VisualObjects       []string // physical objects in the image
	EmotionalTone       []string // psychological/emotional intent
	SearchTextExpanded  string   // full FTS blob for BM25+vector search
	// Taxonomy fields
	Subjects            []string
	SubjectSlugs        []string
	Tags                []string
	Categories          []string
	Mood                []string
	Style               []string
	Confidence          float64
	EmbeddingStatus     string
	VisualEmbeddingJSON string
	PHash               string
	VisualDimensions    int
	Assets              []map[string]any
	Extra               map[string]any
}

func BuildAssetMetadata(in AssetSemanticInput, existing map[string]any) map[string]any {
	if existing == nil {
		existing = make(map[string]any)
	}
	setIfEmpty := func(key string, value any) {
		if _, ok := existing[key]; !ok {
			existing[key] = value
		}
	}
	setIfEmpty("asset_id", strings.TrimSpace(in.AssetID))
	setIfEmpty("asset_type", strings.TrimSpace(in.AssetType))
	setIfEmpty("source", strings.TrimSpace(in.Source))
	setIfEmpty("media_type", strings.TrimSpace(in.MediaType))
	setIfEmpty("generator", strings.TrimSpace(in.Generator))
	setIfEmpty("prompt_original", strings.TrimSpace(in.PromptOriginal))
	setIfEmpty("semantic_description", strings.TrimSpace(in.SemanticDescription))
	setIfEmpty("search_text", strings.TrimSpace(in.SearchText))
	// LLM-enriched fields
	if len(in.ConceptTags) > 0 {
		setIfEmpty("concept_tags", in.ConceptTags)
	}
	if len(in.VisualObjects) > 0 {
		setIfEmpty("visual_objects", in.VisualObjects)
	}
	if len(in.EmotionalTone) > 0 {
		setIfEmpty("emotional_tone", in.EmotionalTone)
	}
	if strings.TrimSpace(in.SearchTextExpanded) != "" {
		setIfEmpty("search_text_expanded", strings.TrimSpace(in.SearchTextExpanded))
	}
	// Taxonomy fields
	if len(in.Subjects) > 0 {
		setIfEmpty("subjects", in.Subjects)
	}
	if len(in.SubjectSlugs) > 0 {
		setIfEmpty("subject_slugs", in.SubjectSlugs)
	}
	if len(in.Tags) > 0 {
		setIfEmpty("tags", in.Tags)
	}
	if len(in.Categories) > 0 {
		setIfEmpty("categories", in.Categories)
	}
	if len(in.Mood) > 0 {
		setIfEmpty("mood", in.Mood)
	}
	if len(in.Style) > 0 {
		setIfEmpty("style", in.Style)
	}
	if in.Confidence > 0 {
		setIfEmpty("confidence", in.Confidence)
	}
	if strings.TrimSpace(in.EmbeddingStatus) != "" {
		setIfEmpty("embedding_status", strings.TrimSpace(in.EmbeddingStatus))
	}
	if strings.TrimSpace(in.VisualEmbeddingJSON) != "" {
		setIfEmpty("visual_embedding_json", strings.TrimSpace(in.VisualEmbeddingJSON))
	}
	if strings.TrimSpace(in.PHash) != "" {
		setIfEmpty("phash", strings.TrimSpace(in.PHash))
	}
	if in.VisualDimensions > 0 {
		setIfEmpty("visual_dimensions", in.VisualDimensions)
	}
	if len(in.Assets) > 0 {
		setIfEmpty("assets", in.Assets)
	}
	if in.Extra != nil {
		for k, v := range in.Extra {
			if _, ok := existing[k]; !ok {
				existing[k] = v
			}
		}
	}
	return existing
}

func MetadataMapFromJSON(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return make(map[string]any)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return make(map[string]any)
	}
	return meta
}

func MetadataMapToJSON(meta map[string]any) string {
	if meta == nil {
		return "{}"
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

func MergeMetadataSearchText(parts ...string) string {
	return NormalizeSearchText(parts...)
}

func AppendUniqueStrings(base []string, items ...string) []string {
	return UniqueAppend(base, items...)
}
