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

// RetrievalInfo holds details about the provenance of retrieved web assets.
type RetrievalInfo struct {
	Provider string `json:"provider,omitempty"`
	PageURL  string `json:"page_url,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	License  string `json:"license,omitempty"`
	Author   string `json:"author,omitempty"`
}

// Payload is the semantic metadata output from the Python tagger.
type Payload struct {
	SchemaVersion       string                        `json:"schema_version,omitempty"`
	AssetID             string                        `json:"asset_id,omitempty"`
	AssetType           string                        `json:"asset_type"`
	SemanticTier        string                        `json:"semantic_tier"` // "generated_rich", "retrieved_rich", etc.
	Source              string                        `json:"source"`        // generated or retrieved
	SourceType          string                        `json:"source_type,omitempty"`
	MediaType           string                        `json:"media_type"`
	Generator           string                        `json:"generator,omitempty"`
	Retriever           string                        `json:"retriever,omitempty"`
	Language            string                        `json:"language,omitempty"` // ISO 639-1 source language
	PromptOriginal      string                        `json:"prompt_original"`
	SemanticDescription string                        `json:"semantic_description"`
	SearchText          string                        `json:"search_text"`
	ConceptTags         []string                      `json:"concept_tags,omitempty"`
	VisualObjects       []string                      `json:"visual_objects,omitempty"`
	EmotionalTone       []string                      `json:"emotional_tone,omitempty"`
	AttributionText     string                        `json:"attribution_text,omitempty"`
	Subjects            []string                      `json:"subjects"`
	SubjectSlugs        []string                      `json:"subject_slugs"`
	Tags                []string                      `json:"tags"`
	Categories          []string                      `json:"categories"`
	Mood                []string                      `json:"mood,omitempty"`
	Style               []string                      `json:"style"`
	RetrievalScore      *float64                      `json:"retrieval_score,omitempty"`
	EmbeddingStatus     string                        `json:"embedding_status"`
	CreatedAt           string                        `json:"created_at"`
	VisualEmbeddingJSON string                        `json:"visual_embedding_json,omitempty"`
	PHash               string                        `json:"phash,omitempty"`
	VisualDimensions    int                           `json:"visual_dimensions,omitempty"`
	Assets              []map[string]any              `json:"assets,omitempty"`
	Retrieval           *RetrievalInfo                `json:"retrieval,omitempty"`
	Translations        map[string]TranslationPayload `json:"translations,omitempty"`
	Extensions          map[string]any                `json:"extensions,omitempty"`

	// VLM visual analysis fields (populated when --vlm-image-url is provided)
	VLMVisualAnalysis *VLMVisualAnalysis `json:"vlm_visual_analysis,omitempty"`
}

// VLMVisualAnalysis holds structured visual metadata from VLM analysis.
type VLMVisualAnalysis struct {
	SceneType      string   `json:"scene_type"`      // talking_head, interview, podcast, b_roll, landscape, etc.
	VisualObjects  []string `json:"visual_objects"`  // physical objects visible in the frame
	Mood           []string `json:"mood"`            // emotional/atmospheric descriptors
	TextOnScreen   []string `json:"text_on_screen"`  // logos, titles, subtitles visible
	DominantColors []string `json:"dominant_colors"` // hex color codes
	Composition    string   `json:"composition"`     // centered, rule_of_thirds, etc.
	Lighting       string   `json:"lighting"`        // natural, studio, dramatic, etc.
	RawDescription string   `json:"raw_description"` // full VLM text output
}

// TaggerRequest contains the input for calling the Python semantic tagger.
type TaggerRequest struct {
	Prompt             string
	Style              string
	MediaType          string
	Generator          string
	SourceType         string // "generated" or "retrieved"
	Retriever          string // e.g. "wikipedia", "searxng"
	PageURL            string
	ImageURL           string
	License            string
	Author             string
	OllamaURL          string
	OllamaModel        string
	Language           string
	TranslateLanguages []string

	// VLM visual analysis (optional — when set, the tagger calls the VLM endpoint)
	VLMImageURL string // URL or base64 data URI of the frame to analyze
	VLMEndpoint string // VLM service URL (e.g. "http://127.0.0.1:8000")
}

// Tagger calls the Python semantic_tagger sub-package via `python3 -m` and
// returns a Payload.
//
// After the package split (`scripts/bridges/semantic_tagger/`), invocation
// switched from `python3 scripts/bridges/semantic_tagger.py …` to
// `python3 -m scripts.bridges.semantic_tagger …`. We set `cmd.Dir` to the
// project root so Python can resolve `scripts` as a top-level package.
//
// The package layout (sub-modules taxonomy / text_processing / extraction /
// llm / vlm / orchestrator) does not affect the JSON envelope — only the
// entry point changed.
func Tagger(ctx context.Context, scriptsDir string, req TaggerRequest) (*Payload, error) {
	args := []string{
		"-m", "scripts.bridges.semantic_tagger",
		"--prompt", req.Prompt,
		"--style", req.Style,
		"--media-type", req.MediaType,
		"--generator", req.Generator,
	}
	if req.SourceType != "" {
		args = append(args, "--source-type", req.SourceType)
	}
	if req.Retriever != "" {
		args = append(args, "--retriever", req.Retriever)
	}
	if req.PageURL != "" {
		args = append(args, "--page-url", req.PageURL)
	}
	if req.ImageURL != "" {
		args = append(args, "--image-url", req.ImageURL)
	}
	if req.License != "" {
		args = append(args, "--license", req.License)
	}
	if req.Author != "" {
		args = append(args, "--author", req.Author)
	}
	if req.Language != "" {
		args = append(args, "--language", req.Language)
	}
	if len(req.TranslateLanguages) > 0 {
		translateArg := req.TranslateLanguages[0]
		for i := 1; i < len(req.TranslateLanguages); i++ {
			translateArg += "," + req.TranslateLanguages[i]
		}
		args = append(args, "--translate-to", translateArg)
	}
	if req.OllamaURL != "" {
		args = append(args, "--ollama-url", req.OllamaURL)
	}
	if req.OllamaModel != "" {
		args = append(args, "--ollama-model", req.OllamaModel)
	}
	if req.VLMImageURL != "" {
		args = append(args, "--vlm-image-url", req.VLMImageURL)
	}
	if req.VLMEndpoint != "" {
		args = append(args, "--vlm-endpoint", req.VLMEndpoint)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	// Run from the project root (one level above `scripts/`) so Python's
	// `-m scripts.…` resolves correctly. Falls back to leaving the CWD
	// untouched when `scriptsDir` doesn't carry that information.
	if scriptsDir != "" {
		if projectRoot := filepath.Dir(scriptsDir); projectRoot != "" && projectRoot != "." {
			cmd.Dir = projectRoot
		}
	}
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
