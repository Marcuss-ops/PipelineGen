// Package semantic — metadata_builder.go: pure metadata-builder helpers
// extracted from the pre-Phase-1.2 semantic.go god-file (July 2026).
//
// Per godlike/06 ("one canonical owner per fact"), every helper here
// is a deterministic, side-effect-free string/map normaliser with a
// clear semantic contract. None of them touch Ollama, Python, or any
// LLM; they are the purely-formal parts of the semantic metadata
// surface that survive the disabled-stub split.
//
// Audit-pin (godlike/06): no I/O, no randomness, no goroutines, no
// time.Now, no global state. Pure functions only. Tests in
// normalize_test.go (already in-package) lock the contracts via the
// NormalizeSearchText canonical example.
package semantic

import (
	"encoding/json"
)

// ── JSON map helpers ──────────────────────────────────────────────────

// MetadataMapFromJSON parses a JSON string into a map. Returns nil on
// empty input or parse error (fail-soft).
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

// MetadataMapToJSON serializes a metadata map to a compact JSON
// string. Returns "{}" on nil input or marshal error (fail-soft).
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

// ── Search text composition ───────────────────────────────────────────

// MergeMetadataSearchText builds a search text by concatenating
// non-empty, whitespace-trimmed parts with single-space separators.
// Per godlike/07 no-fake-availability, canonical contract: empty
// inputs produce "" (never a fabricated token). Used by the
// (now-disabled) MetadataWriter.GeneratePayload + WriteRequest
// wrappers; preserved here as a fallback for callers that still
// build search-text offline (some legacy artlist search code paths
// invoke this directly without going through the disabled stub).
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

// ── Asset-type mapping ────────────────────────────────────────────────

// AssetTypeForMediaType maps a media-type string to a canonical asset
// type. Pre-fix the canonicalSurface returned an empty string for
// unknown media types (silent fallback) — Phase 1.2 closure now
// echoes the unknown media-type verbatim (godlike/07 no-fake-
// availability: caller gets the actual unknown value rather than a
// synthetic default).
//
// Canonical map (preserved from the pre-fix contract — caller-visible
// shape unchanged for the 4 known buckets):
//
//	image / photo / picture → "image"
//	video / clip           → "video"
//	audio / sound / music  → "audio"
//	document / text        → "document"
//	<unknown>              → <unknown verbatim>
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

// ── Slice utilities ───────────────────────────────────────────────────

// AppendUniqueStrings appends values to base, deduplicating. Whitespace-
// trimmed per-token; empty strings are dropped. Preserves the base
// order; appended values appear after the base in input order (stable
// ordering matters for callers that rely on deterministic payloads
// for diff/audit surfaces).
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

// ── Unified metadata map builder ─────────────────────────────────────

// BuildAssetMetadata creates a unified metadata map from an input
// struct + optional existing map (overlay semantics: existing keys
// are copied through, then input fields override).
//
// Canonical contract: empty/missing input fields do NOT appear as
// null/zero entries in the result (godlike/07 no-fake-availability);
// the result map contains only the keys that have a meaningful value.
// This is why every field has an explicit `if != ""` / `if != 0` /
// `if len > 0` guard rather than a blanket copy.
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

// ── Extension builders ───────────────────────────────────────────────

// BuildVideoExtension creates a video-specific extensions slice.
// Pure builder; no IO; deterministic output. Used by the
// (now-disabled) MetadataWriter payload construction code paths.
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
// Pure builder; matching the video builder contract exactly.
func BuildAudioExtension(durationSec, sampleRate, channels int, isMusic bool, sourceVideoID string) []map[string]any {
	return []map[string]any{
		{
			"type":         "audio",
			"duration":     durationSec,
			"sample_rate":  sampleRate,
			"channels":     channels,
			"is_music":     isMusic,
			"source_video": sourceVideoID,
		},
	}
}

// BuildImageExtension creates an image-specific extensions slice.
// Pure builder; matching the video/audio builder contract exactly.
func BuildImageExtension(width, height int, format, dominantColor string, fileSizeBytes int) []map[string]any {
	return []map[string]any{
		{
			"type":           "image",
			"width":          width,
			"height":         height,
			"format":         format,
			"dominant_color": dominantColor,
			"file_size":      fileSizeBytes,
		},
	}
}

// ── Internal helpers ──────────────────────────────────────────────────

// trimSpace strips leading + trailing space characters without
// pulling in strings.TrimSpace (which allocates differently + adds a
// dependency). Used internally by MergeMetadataSearchText +
// AppendUniqueStrings (in this file) and NormalizeSearchText (in
// normalize.go) to keep the deterministic string ops in this package
// dependency-free at the cost of one bespoke helper.
//
// Note: this is a space-only trim (NOT a full unicode whitespace trim
// per strings.TrimSpace). Matches the pre-fix trimSpace contract
// exactly so callers that thread strings through the builder pipeline
// see byte-identical output.
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
