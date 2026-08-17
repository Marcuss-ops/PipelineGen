package script

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

// OutputSpec declares which post-generation artifacts to produce.
// ExtractEntities and GenerateMetadata are Toggle tri-state values.
// Caller-explicit ToggleDisabled survives the applySafetyDefaults +
// ApplyPreset chain. SaveToDB is a bool persistence flag.
type OutputSpec struct {
	// Audio is the explicit audio execution mode. Empty preserves the
	// legacy voiceover behavior and is resolved once at the capability edge.
	Audio AudioOutputConfig `json:"audio,omitempty"`
	// ── Postprocessors (Toggle tri-state) ──────────────────────────
	//
	// ExtractEntities is an ACTIVE inline postprocessor
	// (ProcessorEntities) registered conditionally on
	// DefaultPolicyFor("entities") == ProcessorRequired. Caller
	// explicit ToggleDisabled is preserved through the resolution
	// chain.
	ExtractEntities Toggle `json:"extract_entities,omitempty"`

	// GenerateMetadata is an ACTIVE inline postprocessor
	// (ProcessorMetadata). See ExtractEntities comment for
	// Toggle semantics.
	GenerateMetadata Toggle `json:"generate_metadata,omitempty"`
	// GenerateSceneImages enables the canonical per-scene AI image
	// postprocessor. It is opt-in; omitted and disabled both leave the
	// image processor out of the plan.
	GenerateSceneImages Toggle `json:"generate_scene_images,omitempty"`

	StockEnabled  Toggle              `json:"stock_enabled,omitempty"`
	StockBindings []StockBindingInput `json:"stock_bindings,omitempty"`

	// ── Persistence (bool — out of PR-3 scope per action plan) ──
	SaveToDB bool `json:"save_to_db,omitempty"`
	// GenerateTimeline requests the canonical timeline metadata artifact
	// (scene durations, video segments) WITHOUT binary render
	// materialization. It only needs transcripts and Drive references;
	// no local media is staged and no render job is enqueued.
	GenerateTimeline bool `json:"generate_timeline,omitempty"`

	// ── Voiceover options ────────────────────────────────────────────
	// VoiceoverEnabled is the canonical capability toggle. Routing fields
	// below select the destination only; they are not consulted as an
	// implicit enable switch after normalization.
	VoiceoverEnabled  Toggle `json:"voiceover_enabled,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// ── Document options ─────────────────────────────────────────────
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Formatting ──────────────────────────────────────────────────
	MaxChars  int    `json:"max_chars,omitempty"`
	OutputFmt string `json:"output_fmt,omitempty"`

	// ── Translations ────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// PR-TRANSLATE-SCRIPT-SPEC PR-5+PR-6 (2026-07-09): the canonical
	// opt-in trigger for the TranslationProcessor. When non-empty,
	// buildPostprocessorList appends ProcessorTranslation between
	// metadata and clip_bindings in the EXECUTION order so the
	// translated SpecScene is visible to the downstream clip binder
	// (localised Drive links + clip titles). Empty string is the
	// "no translation requested" sentinel — caller-omission is
	// distinguishable from caller-explicit-empty because callers
	// that want "explicit no translation" pass TranslateTo="". The
	// resolution chain (caller > preset > config > safety) applies
	// unchanged from PR-3.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: a caller that supplies
	// TranslateTo="en" (the script's primary language) intentionally
	// bypasses translation (translator would no-op into ErrTranslationEqualToSource)
	// — this is the canonical explicit opt-in for "I already wrote in
	// the target language, don't waste LLM tokens". The processor
	// surfaces the no-op soft-warning + the bounded-reason metric so
	// operator dashboards can distinguish "translator idle" from
	// "translator absent".
	//
	// godlike/06 SSOT (one-canonical-owner-per-fact): TranslateTo lives
	// ONLY here on OutputSpec. BuildPlan copies it onto the
	// canonical ResolvedGenerationPlan so the postprocessor reads
	// a single source (the plan); no duplicate expression in
	// ResolvedGenerationPlan or in processor_translation.go.
	TranslateTo string `json:"translate_to,omitempty"`
}

type AudioOutputConfig struct {
	Mode string `json:"mode,omitempty"`
	// Timing is the canonical voiceover timing policy nested inside the
	// existing audio config (wire key "timing"). nil means the pipeline
	// applies the canonical defaults (best_effort / word / [json]) —
	// timing capture is never implicitly mandatory.
	Timing *audio.TimingRequest `json:"timing,omitempty"`
}

// HasAnyPostprocessor returns true when at least one active postprocessor
// flag is non-disabled (ToggleEnabled or ToggleDefault resolve to true;
// ToggleDisabled resolves to false). SaveToDB is intentionally out of scope.
func (o *OutputSpec) HasAnyPostprocessor() bool {
	return o.ExtractEntities.AsBool() ||
		o.GenerateMetadata.AsBool() ||
		o.GenerateSceneImages.AsBool()
}
