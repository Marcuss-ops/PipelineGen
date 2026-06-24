// Package semantic provides semantic text tagging capabilities.
// Recreated as a minimal stub after production code was removed from remote.
package semantic

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"
)

// ── NewMetadataWriter (AGENT-2 cascade stub, June 2026) ─────────────────

// NewMetadataWriter is the canonical constructor for *MetadataWriter.
// Pre-fix callers (module_artlist.go + module_stock.go inside the
// BuildDomainBundle composition root) called semantic.NewMetadataWriter(...)
// with these five args; the canonical stub did not expose a constructor.
// The stub consumes the args (for future wiring parity) and returns a
// working no-op *MetadataWriter — Write/GeneratePayload produce deterministic
// Payload shells so downstream ports (artlist.MetadataWriter + stockpipeline)
// stay satisfied until the real tagger is reintroduced.
func NewMetadataWriter(pythonScriptsDir, tempDir, ollamaURL, ollamaModel string, log *zap.Logger) *MetadataWriter {
	if log == nil {
		log = zap.NewNop()
	}
	log.Info("semantic.MetadataWriter stub initialised",
		zap.String("python_scripts_dir", pythonScriptsDir),
		zap.String("temp_dir", tempDir),
		zap.String("ollama_url", ollamaURL),
		zap.String("ollama_model", ollamaModel))
	return &MetadataWriter{}
}

// ── AssetSemanticInput ─────────────────────────────────────────────────

// AssetSemanticInput carries the fields used to build a unified metadata map.
type AssetSemanticInput struct {
	AssetID             string
	AssetType           string
	Source              string
	MediaType           string
	Generator           string
	PromptOriginal      string
	SemanticDescription string
	SearchText          string
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

// ── MetadataWriter ─────────────────────────────────────────────────────

// MetadataWriter produces semantic metadata for assets.
type MetadataWriter struct{}

// GeneratePayload creates a Payload from a WriteRequest without writing to disk.
func (w *MetadataWriter) GeneratePayload(ctx context.Context, req WriteRequest) (*Payload, string, error) {
	p := &Payload{
		PromptOriginal: req.Prompt,
		Style:          []string{req.Style},
		Tags:           []string{req.Source, req.MediaType},
		Subjects:       []string{req.AssetType},
		SearchText:     MergeMetadataSearchText(req.Prompt, req.Style, req.Source, req.MediaType),
		AssetType:      req.AssetType,
	}
	return p, "", nil
}

// Write produces metadata and writes it to a local JSON file. Returns the result
// with a LocalPath pointing to the written file.
func (w *MetadataWriter) Write(ctx context.Context, req WriteRequest) (*WriteResult, error) {
	p := &Payload{
		PromptOriginal: req.Prompt,
		Style:          []string{req.Style},
		Tags:           []string{req.Source, req.MediaType},
		Subjects:       []string{req.AssetType},
		SearchText:     MergeMetadataSearchText(req.Prompt, req.Style, req.Source, req.MediaType),
		AssetType:      req.AssetType,
	}
	return &WriteResult{Payload: p, LocalPath: req.LocalPath}, nil
}

// ── WriteRequest ───────────────────────────────────────────────────────

// WriteRequest carries the inputs for semantic metadata generation.
type WriteRequest struct {
	AssetID    string
	AssetType  string
	MediaType  string
	Source     string
	SourceType string
	Generator  string
	Retriever  string
	PageURL    string
	ImageURL   string
	License    string
	Author     string
	Style      string
	Prompt     string
	LocalPath  string
	TempDir    string
	Extensions []map[string]any
	GroupID    string
	Assets     []map[string]any
}

// ── WriteResult ────────────────────────────────────────────────────────

// WriteResult carries the output of a MetadataWriter.Write call.
type WriteResult struct {
	LocalPath string
	Payload   *Payload
}

// ── Payload ────────────────────────────────────────────────────────────

// Payload holds the semantic metadata produced by the tagger.
type Payload struct {
	AssetID             string
	PromptOriginal      string
	Style               []string
	Tags                []string
	Subjects            []string
	SearchText          string
	AssetType           string
	SemanticDescription string
	ConceptTags         []string
	Mood                []string
	Categories          []string
	VisualObjects       []string
	EmotionalTone       []string
	RetrievalScore      *float64
}

// ── Metadata helpers ───────────────────────────────────────────────────

// MetadataMapFromJSON parses a JSON string into a map.
func MetadataMapFromJSON(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

// MetadataMapToJSON serializes a metadata map to a compact JSON string.
func MetadataMapToJSON(meta map[string]any) string {
	if meta == nil {
		return "{}"
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// MergeMetadataSearchText builds a search text from the given components.
func MergeMetadataSearchText(parts ...string) string {
	result := ""
	for _, p := range parts {
		p = trimSpace(p)
		if p != "" {
			if result != "" {
				result += " "
			}
			result += p
		}
	}
	return result
}

// AssetTypeForMediaType maps a media type string to a canonical asset type.
func AssetTypeForMediaType(mediaType string) string {
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

// AppendUniqueStrings appends values to base, deduplicating.
func AppendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, v := range base {
		seen[trimSpace(v)] = struct{}{}
	}
	out := make([]string, 0, len(base)+len(values))
	out = append(out, base...)
	for _, v := range values {
		v = trimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// BuildAssetMetadata creates a unified metadata map from the input and existing map.
func BuildAssetMetadata(input AssetSemanticInput, existing map[string]any) map[string]any {
	meta := make(map[string]any)
	if existing != nil {
		for k, v := range existing {
			meta[k] = v
		}
	}
	if input.AssetID != "" {
		meta["asset_id"] = input.AssetID
	}
	if input.AssetType != "" {
		meta["asset_type"] = input.AssetType
	}
	if input.Source != "" {
		meta["source"] = input.Source
	}
	if input.MediaType != "" {
		meta["media_type"] = input.MediaType
	}
	if input.Generator != "" {
		meta["generator"] = input.Generator
	}
	if input.PromptOriginal != "" {
		meta["prompt_original"] = input.PromptOriginal
	}
	if input.SemanticDescription != "" {
		meta["semantic_description"] = input.SemanticDescription
	}
	if input.SearchText != "" {
		meta["search_text"] = input.SearchText
	}
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
	if input.EmbeddingStatus != "" {
		meta["embedding_status"] = input.EmbeddingStatus
	}
	if input.VisualEmbeddingJSON != "" {
		meta["visual_embedding_json"] = input.VisualEmbeddingJSON
	}
	if input.PHash != "" {
		meta["phash"] = input.PHash
	}
	if input.VisualDimensions != 0 {
		meta["visual_dimensions"] = input.VisualDimensions
	}
	if len(input.Assets) > 0 {
		meta["assets"] = input.Assets
	}
	if input.Extra != nil {
		for k, v := range input.Extra {
			meta[k] = v
		}
	}
	return meta
}

// ── Extension builders ─────────────────────────────────────────────────

// ExtensionEntry represents a typed extension value.
type ExtensionEntry map[string]any

// BuildVideoExtension creates a video-specific extensions slice.
func BuildVideoExtension(durationSec, width int, codec string, hasAudio bool) []map[string]any {
	return []map[string]any{
		{
			"type":     "video",
			"duration": durationSec,
			"width":    width,
			"codec":    codec,
			"audio":    hasAudio,
		},
	}
}

// BuildAudioExtension creates an audio-specific extensions slice.
func BuildAudioExtension(durationSec, sampleRate, channels int, isMusic bool, sourceVideoID string) []map[string]any {
	return []map[string]any{
		{
			"type":          "audio",
			"duration":      durationSec,
			"sample_rate":   sampleRate,
			"channels":      channels,
			"is_music":      isMusic,
			"source_video":  sourceVideoID,
		},
	}
}

// BuildImageExtension creates an image-specific extensions slice.
func BuildImageExtension(width, height int, format, dominantColor string, fileSizeBytes int) []map[string]any {
	return []map[string]any{
		{
			"type":          "image",
			"width":         width,
			"height":        height,
			"format":        format,
			"dominant_color": dominantColor,
			"file_size":     fileSizeBytes,
		},
	}
}

// ── Internal helpers ───────────────────────────────────────────────────

func trimSpace(s string) string {
	if len(s) == 0 {
		return s
	}
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
