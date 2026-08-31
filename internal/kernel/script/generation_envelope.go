// Package script — generation_envelope.go defines the canonical
// top-level request envelope for all script generation. It replaces
// the fragmented per-endpoint request types with a single contract.
//
//	GenerationEnvelopeV2 → [{GenerationItemV2 → SourceSpec + ScriptSpec + OutputSpec}]
//
// A single-item envelope maps to the unified /generate flow.
// A multi-item envelope maps to batch generation.
//
// No durable field uses any, any, or map[string]any.
package script

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// GenerationEnvelopeV2 is the canonical top-level request for all
// script generation. The worker unpacks the envelope, normalizes
// each item via the shared precedence chain, and executes them
// through the unified pipeline:
//
//	normalize → validate → resolve source → build plan → generate → postprocess → result
type GenerationEnvelopeV2 struct {
	// Version is the envelope schema version. Always 2 for V2.
	Version int `json:"version"`

	// Preset records the endpoint variant for default application:
	//   "custom"       — caller filled every flag explicitly
	//   "with_images"  — force scene_images+voiceover ON, entities+metadata OFF
	//   "batch"        — batch generation (multiple items)
	Preset Preset `json:"preset"`

	// CorrelationID is an optional tracing identifier propagated
	// through logs and job metadata.
	CorrelationID string `json:"correlation_id,omitempty"`

	// ForceRefresh bypasses the idempotency store and active-key
	// dedup for this envelope. When true, a brand-new script.generate
	// job is always enqueued, even if the same Idempotency-Key has
	// an active or completed record.
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// Items is the list of generation items. Must contain at least
	// one entry. For single-item generation, use one item. For
	// batch generation, use multiple items — each is independently
	// normalized, resolved, generated, and postprocessed.
	Items []GenerationItemV2 `json:"items"`
}

// GenerationItemV2 is a single generation request within an envelope.
// Each item declares its source, script parameters, output options,
// and identity independently. This independence allows a batch
// envelope to mix text-only, clip-based, catalog, and search items
// in a single request.
type GenerationItemV2 struct {
	// ID is an optional caller-assigned identifier for correlating
	// results. When non-empty, the matching GenerationResult
	// carries the same ID.
	ID string `json:"id,omitempty"`

	// ── Identity ──────────────────────────────────────────────────────
	Title string `json:"title,omitempty"`
	// Project is the explicit artifact-routing namespace. It is resolved at
	// ingress and propagated unchanged to every published artifact.
	Project   string `json:"project,omitempty"`
	Language  string `json:"language,omitempty"`
	Tone      string `json:"tone,omitempty"`
	Style     string `json:"style,omitempty"`
	Model     string `json:"model,omitempty"`
	ModelAuto bool   `json:"-"`

	// MediaMode explicitly selects the media contract for this item.
	// Mixed media is never inferred from the presence of references.
	MediaMode MediaMode `json:"media_mode,omitempty"`

	// ── Source ────────────────────────────────────────────────────────
	// Source declares where the generation input comes from.
	// Must have a valid Type and the corresponding fields populated.
	Source SourceSpec `json:"source"`

	// ── Script parameters ─────────────────────────────────────────────
	// ScriptParams controls HOW the script is generated (sizing,
	// prompt versioning). The field is named ScriptParams, not Script,
	// to avoid shadowing the package name (a Go footgun — any method
	// added to GenerationItemV2 wouldn't be able to reference
	// script.SomeType).
	//
	// All fields are optional; the normalizer fills in defaults.
	ScriptParams ScriptSpec `json:"script_params,omitempty"`

	// ── Output options ────────────────────────────────────────────────
	// Output declares WHAT post-generation artifacts to produce.
	// Every postprocessor is opt-in.
	Output OutputSpec `json:"output,omitempty"`

	// OverlayBackground optionally selects the full-canvas background used by
	// the Chronon overlay render. It is deliberately separate from audio
	// background_music: this is a visual pixel-layer choice.
	OverlayBackground *OverlayBackgroundSpec `json:"overlay_background,omitempty"`
	// OverlayStyle applies the same explicit visual overrides to generated
	// phrase/word/image overlays. It is transport-only; Chronon's preset
	// registry remains the source of defaults.
	OverlayStyle *OverlayStyleSpec `json:"overlay_style,omitempty"`

	// Audio configures the audio execution mode (audio.mode) plus the
	// editorial audio intent block (mix_policy, background_music,
	// sound_effects) for this generate item. Batch items may choose
	// independently.
	Audio AudioOutputConfig `json:"audio,omitempty"`

	// Docs explicitly requests publication of one Google Doc per language.
	// It is kept separate from Output so document creation is never inferred
	// from unrelated output options.
	Docs DocumentsSpec `json:"docs,omitempty"`

	// ── Media plan ────────────────────────────────────────────────────
	// MediaPlan declares which visual media should accompany the
	// generated script. It is separate from SourceSpec because it
	// describes media assets, not narrative content.
	MediaPlan media.MediaPlanSpec `json:"media_plan,omitempty"`

	// VideoMetadata contains caller-provided YouTube metadata.
	// When present, these values are used directly and the metadata
	// generator must not be called.
	VideoMetadata *VideoMetadata `json:"video_metadata,omitempty"`

	// Intro is an optional literal section prepended verbatim. It is NOT
	// sent to the LLM, NOT rewritten from source_text, and NOT merged
	// with clip transcripts. The caller supplies the exact narration
	// (Text) and a single clip_id that must exist in the media catalog.
	// The runner injects it as kind=intro before LLM-generated scenes.
	Intro *FixedSection `json:"intro,omitempty"`

	// Outro is an optional literal section appended verbatim. Same
	// literal contract as Intro — no LLM rewrite, exact Text is spoken
	// and the supplied clip is bound with kind=outro.
	Outro *FixedSection `json:"outro,omitempty"`
}

// OverlayBackgroundSpec is the script.generate payload shape for an
// optional visual overlay background. Image/video backgrounds identify one
// content-addressed asset; color backgrounds use RGBA components in [0,1].
type OverlayBackgroundSpec struct {
	Kind      string            `json:"kind"`
	Color     []float64         `json:"color,omitempty"`
	AssetID   string            `json:"asset_id,omitempty"`
	URL       string            `json:"url,omitempty"`
	SHA256    string            `json:"sha256,omitempty"`
	MediaType string            `json:"media_type,omitempty"`
	Fit       string            `json:"fit,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	Loop      bool              `json:"loop,omitempty"`
	Style     *OverlayStyleSpec `json:"style,omitempty"`
}

// OverlayStyleSpec is the canonical script.generate visual override block.
// Every field is optional so existing payloads keep their exact behaviour.
type OverlayStyleSpec struct {
	Shadow       *OverlayShadowSpec     `json:"shadow,omitempty"`
	Color        []float64              `json:"color,omitempty"`
	Size         *OverlaySizeSpec       `json:"size,omitempty"`
	TransitionIn *OverlayTransitionSpec `json:"transition_in,omitempty"`
}

type OverlayShadowSpec struct {
	Enabled bool      `json:"enabled,omitempty"`
	Color   string    `json:"color,omitempty"`
	Opacity *float64  `json:"opacity,omitempty"`
	Blur    *float64  `json:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty"`
}

type OverlaySizeSpec struct {
	Width    *int     `json:"width,omitempty"`
	Height   *int     `json:"height,omitempty"`
	FontSize *float64 `json:"font_size,omitempty"`
}

type OverlayTransitionSpec struct {
	Preset         string `json:"preset"`
	DurationFrames int    `json:"duration_frames,omitempty"`
}

// DocumentsSpec is the transport-level document publication configuration.
type DocumentsSpec struct {
	Enabled   bool     `json:"enabled"`
	Languages []string `json:"languages,omitempty"`
	FolderID  string   `json:"folder_id,omitempty"`
}

// FixedSection is a literal intro/outro section that bypasses the LLM.
// The caller provides the exact spoken Text and the clip binding; the
// pipeline injects it verbatim as the first (intro) or last (outro)
// SpecScene with Kind=intro/outro. Text is never rewritten from
// source_text or clip transcripts. Allowed only with source.type=clips
// (or catalog/search/curate) where clip bindings exist.
type FixedSection struct {
	// Text is the exact narration spoken for this section. Must pass
	// ValidateSpeakableText (no URLs, no source markers).
	Text string `json:"text"`
	// ClipIDs is the clip binding for this section. Exactly one clip is
	// required; multiple clips are rejected to keep timeline ownership
	// unambiguous. Use the media catalog ID (e.g. yt_xxx_..._v1).
	ClipIDs []string `json:"clip_ids,omitempty"`
	// Title is an optional human-readable title for the Docs scene.
	Title string `json:"title,omitempty"`
}

// NormalizedClipIDs returns trimmed, non-empty clip IDs for this section.
func (f *FixedSection) NormalizedClipIDs() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.ClipIDs))
	for _, id := range f.ClipIDs {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// CloneFixedSection returns a deep copy of a FixedSection.
func CloneFixedSection(in *FixedSection) *FixedSection {
	if in == nil {
		return nil
	}
	out := *in
	if in.ClipIDs != nil {
		out.ClipIDs = append([]string(nil), in.ClipIDs...)
	}
	return &out
}
