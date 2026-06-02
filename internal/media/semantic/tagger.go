package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

// TranslationPayload holds translated metadata fields for a single target language.
type TranslationPayload struct {
	SearchText          string   `json:"search_text,omitempty"`
	SemanticDescription string   `json:"semantic_description,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	Subjects            []string `json:"subjects,omitempty"`
	Mood                []string `json:"mood,omitempty"`
}

// Payload is the semantic metadata output from the Python tagger.
// Same structure as images.SemanticMetadataPayload — one format, no duplication.
// ALL media types (image, video, audio, voiceover, clip, stock) use this single struct.
type Payload struct {
	AssetID             string                       `json:"asset_id,omitempty"`
	AssetType           string                       `json:"asset_type"`
	SemanticTier        string                       `json:"semantic_tier"` // "generated_rich" when enriched
	Source              string                       `json:"source"`
	MediaType           string                       `json:"media_type"`
	Generator           string                       `json:"generator"`
	Language            string                       `json:"language,omitempty"` // ISO 639-1 source language
	PromptOriginal      string                       `json:"prompt_original"`
	SemanticDescription string                       `json:"semantic_description"`
	SearchText          string                       `json:"search_text"`
	// Enriched fields for hybrid BM25+vector search (no LLM at runtime)
	ConceptTags         []string         `json:"concept_tags,omitempty"`    // synonym-expanded keywords
	VisualObjects       []string         `json:"visual_objects,omitempty"`  // physical objects in image
	EmotionalTone       []string         `json:"emotional_tone,omitempty"` // psychological intent
	SearchTextExpanded  string           `json:"search_text_expanded,omitempty"` // full FTS blob
	// Core taxonomy fields
	Subjects            []string         `json:"subjects"`
	SubjectSlugs        []string         `json:"subject_slugs"`
	Tags                []string         `json:"tags"`
	Categories          []string         `json:"categories"`
	Mood                []string         `json:"mood,omitempty"`
	Style               []string         `json:"style"`
	Confidence          float64          `json:"confidence"`
	EmbeddingStatus     string           `json:"embedding_status"`
	CreatedAt           string           `json:"created_at"`
	VisualEmbeddingJSON string           `json:"visual_embedding_json,omitempty"`
	PHash               string           `json:"phash,omitempty"`
	VisualDimensions    int              `json:"visual_dimensions,omitempty"`
	Assets              []map[string]any `json:"assets,omitempty"`
	// Multi-language translations (language code → translated fields)
	Translations map[string]TranslationPayload `json:"translations,omitempty"`
	// Type-specific extensions (video: fps/codec, audio: sample_rate/channels, image: width/height, etc.)
	// Extensions preserves per-media-type fields without bloating the core Payload.
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Tagger calls the Python semantic_tagger.py script and returns a Payload.
// mediaType can be "image", "video", "audio", or "voiceover".
// ollamaURL and ollamaModel are used to call Ollama for LLM enrichment at ingest time.
// Pass empty strings to skip LLM enrichment (taxonomy-only mode).
// language is the ISO 639-1 source language. translateLanguages are target languages for translation.
func Tagger(ctx context.Context, scriptsDir, prompt, style, mediaType, generator, ollamaURL, ollamaModel, language string, translateLanguages []string) (*Payload, error) {
	scriptPath := filepath.Join(scriptsDir, "semantic_tagger.py")
	args := []string{
		scriptPath,
		"--prompt", prompt,
		"--style", style,
		"--media-type", mediaType,
		"--generator", generator,
	}
	if language != "" {
		args = append(args, "--language", language)
	}
	if len(translateLanguages) > 0 {
		translateArg := translateLanguages[0]
		for i := 1; i < len(translateLanguages); i++ {
			translateArg += "," + translateLanguages[i]
		}
		args = append(args, "--translate-to", translateArg)
	}
	if ollamaURL != "" {
		args = append(args, "--ollama-url", ollamaURL)
	}
	if ollamaModel != "" {
		args = append(args, "--ollama-model", ollamaModel)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("semantic_tagger failed: %w (output: %s)", err, string(output))
	}

	var payload Payload
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode semantic_tagger output: %w", err)
	}

	return &payload, nil
}

