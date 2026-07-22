// Package script — model_output.go defines the canonical structured
// output the LLM must emit for every script generation. The contract
// is stable and versioned: schema_version tracks forward-compatible
// evolution; the decoder rejects unsupported versions.
//
//	ModelScriptOutputV1 { text, specscene }
//
// Every generation endpoint produces this exact shape; no endpoint-
// specific output arrays (clip_scenes, image_scenes, voiceover_scenes)
// exist as durable contracts. Legacy arrays are converted to this
// canonical object by the compatibility decoder during migration;
// new writers MUST emit this object directly.
//
// No durable field uses any, any, or map[string]any.
package script

import (
	"fmt"
	"strings"
)

// ── Scene kind ─────────────────────────────────────────────────────

// SceneKind tags each scene with its primary visual treatment.
type SceneKind string

const (
	// SceneNarration — voiceover or on-screen narration with no
	// associated visual asset.
	SceneNarration SceneKind = "narration"

	// SceneIntro — introductory narration for the video.
	SceneIntro SceneKind = "intro"

	// SceneOutro — concluding narration / call-to-action for the video.
	SceneOutro SceneKind = "outro"

	// SceneClip — the scene is anchored to a selected YouTube clip.
	SceneClip SceneKind = "clip"

	// SceneImage — the scene is illustrated by an AI-generated image.
	SceneImage SceneKind = "image"

	// SceneMixed — the scene combines multiple visual elements
	// (e.g. clip overlaid with generated imagery).
	SceneMixed SceneKind = "mixed"
)

// Valid reports whether k is a known scene kind.
func (k SceneKind) Valid() bool {
	switch k {
	case SceneNarration, SceneIntro, SceneOutro, SceneClip, SceneImage, SceneMixed:
		return true
	}
	return false
}

// ── Canonical model output ─────────────────────────────────────────

// ModelScriptOutputV1 is the canonical structured output the LLM
// must return for every script generation. SchemaVersion is always 1
// for this contract; the decoder validates and rejects unknown
// versions.
//
// JSON shape (model-emitted):
//
//	{
//	  "schema_version": 1,
//	  "text": "Complete generated script...",
//	  "specscene": { "version": 1, "scenes": [...] }
//	}
//
// PR 3 (June 2026): WordCount / ModelUsed / CacheStatus are
// engine-stamped provenance fields, NOT part of the model-emitted
// JSON shape. The decoder ignores them on read; the engine sets them
// in-place after decoding so that processors (which receive the
// canonical typed MSOV1) can read WordCount / ModelUsed / CacheStatus
// uniformly without extra wrapping.
type ModelScriptOutputV1 struct {
	// SchemaVersion is the version of this output contract.
	// Currently always 1.
	SchemaVersion int `json:"schema_version"`

	// Text is the complete generated script prose. Must be non-empty.
	Text string `json:"text"`

	// SpecScene is the structured scene breakdown. Always present;
	// may contain zero scenes for pure prose generation.
	SpecScene SpecSceneOutput `json:"specscene"`

	// WordCount is the model's reported token count, stamped by
	// the engine post-decode. The pre-PR-3 ProcessInput envelope
	// carried this as a separate field; the PR 3 typed walk
	// surfaces it on the model directly. omitempty so the
	// model-emitted JSON shape is unaffected.
	WordCount int `json:"word_count,omitempty"`

	// ModelUsed is the engine's provenance stamp for which
	// model produced this output ("llama3:8b", "qwen2.5:14b",
	// ""). omitempty.
	ModelUsed string `json:"model_used,omitempty"`

	// CacheStatus is "exact_hit" (memory gate hit) or
	// "generated". omitempty.
	CacheStatus string `json:"cache_status,omitempty"`
}

// Validate checks structural invariants. Returns a ModelOutputError
// with structured details on failure, or nil when the output is
// structurally valid.
//
// Rules:
//   - SchemaVersion must be 1.
//   - Text must be non-empty (after trimming whitespace).
//   - SpecScene must be valid (specscene version, scenes, indexes).
func (m *ModelScriptOutputV1) Validate() error {
	var details []string

	if m.SchemaVersion != 1 {
		details = append(details,
			fmt.Sprintf("unsupported schema_version %d (expected 1)", m.SchemaVersion))
	}
	if strings.TrimSpace(m.Text) == "" {
		details = append(details, "text is required and must be non-empty")
	}
	if err := m.SpecScene.Validate(); err != nil {
		if me, ok := err.(*ModelOutputError); ok {
			details = append(details, "specscene: "+strings.Join(me.Details, "; "))
		} else {
			details = append(details, "specscene: "+err.Error())
		}
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}

// ── SpecScene output ───────────────────────────────────────────────

// SpecSceneOutput is the structured scene breakdown embedded within
// ModelScriptOutputV1. Version tracks the specscene schema version
// (currently 1).
//
// JSON shape:
//
//	{
//	  "version": 1,
//	  "scenes": [
//	    { "id": "scene-1", "index": 0, "text": "...", "kind": "clip", "bindings": {...} }
//	  ]
//	}
type SpecSceneOutput struct {
	// Version is the specscene schema version. Currently 1.
	Version int `json:"version"`

	// Scenes is the ordered list of scenes. May be empty for pure
	// prose generation where no scene breakdown is expected.
	Scenes []SpecScene `json:"scenes"`
}

// Validate checks structural invariants on the specscene block.
//
// Rules:
//   - Version must be 1.
//   - Scene Indexes must be sequential (0..len-1).
//   - Scene IDs must be non-empty and unique.
//   - Every scene must pass its own Validate.
func (s *SpecSceneOutput) Validate() error {
	var details []string

	if s.Version != 1 {
		details = append(details,
			fmt.Sprintf("unsupported specscene version %d (expected 1)", s.Version))
	}

	seenIDs := make(map[string]int) // id → first index
	for i := range s.Scenes {
		scene := &s.Scenes[i]

		// Scene-level validation.
		if err := scene.Validate(); err != nil {
			if me, ok := err.(*ModelOutputError); ok {
				for _, d := range me.Details {
					details = append(details,
						fmt.Sprintf("scenes[%d]: %s", i, d))
				}
			} else {
				details = append(details,
					fmt.Sprintf("scenes[%d]: %s", i, err.Error()))
			}
		}

		// Index must match position.
		if scene.Index != i {
			details = append(details,
				fmt.Sprintf("scenes[%d]: index mismatch (expected %d, got %d)",
					i, i, scene.Index))
		}

		// Duplicate ID check.
		if scene.ID != "" {
			if prev, ok := seenIDs[scene.ID]; ok {
				details = append(details,
					fmt.Sprintf("scenes[%d]: duplicate scene id %q (first seen at index %d)",
						i, scene.ID, prev))
			} else {
				seenIDs[scene.ID] = i
			}
		}
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}

// ── SpecScene ──────────────────────────────────────────────────────

// SpecScene is a single scene within the structured breakdown.
// Every scene has a stable ID, sequential Index, narrative Text,
// a Kind tag, and optional Bindings to resolved assets.
//
// JSON shape:
//
//	{
//	  "id": "scene-1",
//	  "index": 0,
//	  "text": "Scene narration text...",
//	  "title": "Opening",
//	  "kind": "clip",
//	  "bindings": { "clip": {...} }
//	}
type SpecScene struct {
	// ID is a stable identifier within the result. Must be non-empty.
	ID        string `json:"id"`
	SegmentID string `json:"segment_id,omitempty"`

	// Index is the zero-based position in the scene array.
	Index int `json:"index"`

	// Text is the narrative text for this scene. Must be non-empty.
	Text string `json:"text"`

	// Title is an optional human-readable scene title.
	Title string `json:"title,omitempty"`

	// Metadata carries technical details that must not be spoken in
	// the voiceover text. It is optional and omitted when empty.
	Metadata *SceneMetadata `json:"metadata,omitempty"`

	// Kind tags the scene's primary visual treatment.
	Kind SceneKind `json:"kind"`

	// Bindings holds the resolved asset references for this scene.
	// Always present in the JSON output (as {} when no assets are
	// bound). Individual binding fields (clip, image, voiceover)
	// use omitempty and are absent when nil.
	Bindings SceneBindings `json:"bindings"`
}

// SceneMetadata carries technical scene data that should not be
// read as narration. It is separate from Text by contract.
type SceneMetadata struct {
	SourceURL string   `json:"source_url,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
	Raw       string   `json:"raw,omitempty"`
}

// Validate checks structural invariants on a single scene.
//
// Rules:
//   - ID must be non-empty.
//   - Text must be non-empty.
//   - Kind must be a valid SceneKind.
func (s *SpecScene) Validate() error {
	var details []string

	if strings.TrimSpace(s.ID) == "" {
		details = append(details, "id is required")
	}
	if strings.TrimSpace(s.Text) == "" {
		details = append(details, "text is required")
	}
	if !s.Kind.Valid() {
		details = append(details,
			fmt.Sprintf("unknown scene kind %q", s.Kind))
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}

// ── Scene bindings ─────────────────────────────────────────────────

// SceneBindings holds the resolved asset references for a scene.
// Each optional binding is omitted when no asset of that type
// is associated with the scene.
type SceneBindings struct {
	// Clip binds this scene to a selected YouTube clip.
	Clip *ClipBinding `json:"clip,omitempty"`

	// Image binds this scene to an AI-generated image.
	Image *ImageBinding `json:"image,omitempty"`

	// Voiceover binds this scene to a generated voiceover audio track.
	Voiceover *VoiceoverBinding `json:"voiceover,omitempty"`

	// Stock binds this scene to a semantically associated stock
	// footage asset, found by vector search. When no stock matches
	// the scene text, falls back to the Clip.DriveLink.
	Stock *StockBinding          `json:"stock,omitempty"`
	Media []ResolvedMediaBinding `json:"media,omitempty"`
}

type ResolvedMediaBinding struct {
	Slot                 string  `json:"slot"`
	AssetID              string  `json:"asset_id,omitempty"`
	BindingID            string  `json:"binding_id,omitempty"`
	Provider             string  `json:"provider,omitempty"`
	MediaType            string  `json:"media_type,omitempty"`
	DriveLink            string  `json:"drive_link,omitempty"`
	Score                float64 `json:"score,omitempty"`
	MaterializationState string  `json:"materialization_state,omitempty"`
	CacheStatus          string  `json:"cache_status,omitempty"`
}

// ── Clip binding ───────────────────────────────────────────────────

// ClipBinding anchors a scene to a selected YouTube clip. The LLM
// outputs the clip_id; the application layer enriches title and
// Drive link from the resolved clip evidence.
type ClipBinding struct {
	// ClipID is the canonical asset ID of the selected clip.
	ClipID string `json:"clip_id"`

	// ClipTitle is the human-readable clip title, enriched by the
	// application layer from resolved clip evidence.
	ClipTitle string `json:"clip_title,omitempty"`

	// DriveLink is the Google Drive URL, enriched by the application
	// layer from resolved clip evidence.
	DriveLink string `json:"drive_link,omitempty"`

	// StartMs is the optional clip start offset in milliseconds.
	// Together with EndMs it bounds the selected segment within
	// the underlying clip asset.
	StartMs int64 `json:"start_ms,omitempty"`

	// EndMs is the optional clip end offset in milliseconds.
	// Together with StartMs it bounds the selected segment within
	// the underlying clip asset.
	EndMs int64 `json:"end_ms,omitempty"`

	// DurationMs is the canonical binding-segment-duration
	// surface; "duration unknown" when zero. Populated by
	// the scene planner via scriptpkg.ClipDurationMs (PURE
	// canonical helper) with the canonical caller pattern's
	// scriptpkg.ClipDurationMsFromAssetID fallback. Whole-
	// clip duration is upstream binder's responsibility
	// (godlike/06 SSOT decomposition).
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// ── Image binding ──────────────────────────────────────────────────

// ImageBinding holds the metadata for an AI-generated scene image.
// The LLM produces the prompt; the application layer fills in the
// generated asset URL and local path.
type ImageBinding struct {
	// ImageID is the canonical asset ID of the generated image.
	ImageID string `json:"image_id,omitempty"`

	// Prompt is the image generation prompt produced by the LLM.
	Prompt string `json:"prompt,omitempty"`

	// URL is the publicly-accessible URL of the generated image,
	// set by the image postprocessor.
	URL string `json:"url,omitempty"`

	// LocalPath is the local filesystem path to the generated image,
	// set by the image postprocessor after download.
	LocalPath string `json:"local_path,omitempty"`

	// Status tracks the generation outcome: "pending", "generated",
	// "failed".
	Status string `json:"status,omitempty"`
}

// ── Voiceover binding ──────────────────────────────────────────────

// VoiceoverBinding holds the metadata for a generated voiceover
// audio track. The LLM does not produce this; it is created
// exclusively by the voiceover postprocessor.
type VoiceoverBinding struct {
	// Status tracks the generation outcome: "pending", "completed",
	// "failed".
	Status string `json:"status"`

	// Link is the publicly-accessible URL of the generated audio.
	Link string `json:"link,omitempty"`

	// LocalPath is the local filesystem path to the generated audio.
	LocalPath string `json:"local_path,omitempty"`

	// DurationMs is the audio duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// ── Stock binding ──────────────────────────────────────────────────

// StockBinding binds a scene to a semantically associated stock
// footage asset. Populated by the stock_association postprocessor
// which searches Qdrant per-scene and falls back to the clip
// drive link when no stock match is found.
type StockBinding struct {
	// AssetID is the canonical media_assets.id of the matched stock.
	AssetID string `json:"asset_id,omitempty"`

	// Name is the human-readable name of the stock asset.
	Name string `json:"name,omitempty"`

	// Source identifies the provider (artlist|youtube|stock).
	Source string `json:"source,omitempty"`

	// DriveLink is the Google Drive URL of the stock asset.
	DriveLink string `json:"drive_link,omitempty"`

	// Score is the cosine-similarity from the vector search.
	Score float64 `json:"score,omitempty"`

	// Fallback is true when the drive_link comes from the scene's
	// ClipBinding.DriveLink because no stock match was found.
	Fallback bool `json:"fallback,omitempty"`
}

// ── Model output errors ────────────────────────────────────────────

// ErrModelOutputMalformed is the sentinel for any model-output
// decode or validation failure (malformed JSON, missing fields,
// unsupported schema version).
var ErrModelOutputMalformed = fmt.Errorf("script: model output malformed")

// ModelOutputError carries the structured details behind
// ErrModelOutputMalformed.
type ModelOutputError struct {
	Details []string
}

func (e *ModelOutputError) Error() string {
	if e == nil || len(e.Details) == 0 {
		return ErrModelOutputMalformed.Error()
	}
	return fmt.Sprintf("%s: %s", ErrModelOutputMalformed.Error(), strings.Join(e.Details, "; "))
}

func (e *ModelOutputError) Unwrap() error { return ErrModelOutputMalformed }
