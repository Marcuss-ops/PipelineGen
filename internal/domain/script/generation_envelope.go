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

import "github.com/Marcuss-ops/PipelineGen/internal/domain/media"

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
	Project  string `json:"project,omitempty"`
	Language string `json:"language,omitempty"`
	Tone     string `json:"tone,omitempty"`
	Style    string `json:"style,omitempty"`
	Model    string `json:"model,omitempty"`

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

	// Audio explicitly selects the audio execution mode for this generate
	// item. Batch items may choose independently.
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
}

// DocumentsSpec is the transport-level document publication configuration.
type DocumentsSpec struct {
	Enabled   bool     `json:"enabled"`
	Languages []string `json:"languages,omitempty"`
	FolderID  string   `json:"folder_id,omitempty"`
}
