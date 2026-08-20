package script

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

// OutputSpec declares which post-generation artifacts to produce.
// ExtractEntities and GenerateMetadata are Toggle tri-state values.
// Caller-explicit ToggleDisabled survives the applySafetyDefaults +
// ApplyPreset chain. SaveToDB is a bool persistence flag.
type OutputSpec struct {
	// VideoRender requests reconstruction of every resolved clip with the
	// selected subtitle/watermark layers. It is opt-in and is carried through
	// script.generate into the localized render fan-out.
	Render VideoRenderSpec `json:"render,omitempty"`
	// Direct blocks are accepted as a concise compatibility form:
	// output.watermark / output.subtitles.
	Watermark *VideoWatermarkSpec `json:"watermark,omitempty"`
	Subtitles *VideoSubtitlesSpec `json:"subtitles,omitempty"`
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

	// MixPolicy is the editorial mix decision applied when compiling the
	// audio plan: "VOICEOVER_ONLY" or "VOICEOVER_DUCKED_CLIP" (the wire
	// spelling "voiceover_with_ducked_clip" normalizes to the latter).
	// Empty means no policy (legacy full-volume overlap).
	MixPolicy audio.AudioMixPolicy `json:"mix_policy,omitempty"`

	// BackgroundMusic is the ordered list of BGM layer intents. Each entry
	// references an asset by asset_id only — filesystem paths are never
	// accepted at the wire boundary. Entries may cover disjoint windows of
	// the timeline (start_ms/end); the compiler resolves each window into
	// fully determined timeline events.
	BackgroundMusic []BackgroundMusicIntent `json:"background_music,omitempty"`

	// SoundEffects is the list of SFX intents, placed either at absolute
	// timeline offsets (at_ms) or relative to a scene (scene_id + anchor +
	// offset_ms). Each entry references an asset by asset_id only.
	SoundEffects []SoundEffectIntent `json:"sound_effects,omitempty"`
}

// HasAnyPostprocessor returns true when at least one active postprocessor
// flag is non-disabled (ToggleEnabled or ToggleDefault resolve to true;
// ToggleDisabled resolves to false). SaveToDB is intentionally out of scope.
func (o *OutputSpec) HasAnyPostprocessor() bool {
	return o.ExtractEntities.AsBool() ||
		o.GenerateMetadata.AsBool() ||
		o.GenerateSceneImages.AsBool()
}

// VideoRenderSpec is the opt-in video reconstruction contract carried by
// POST /api/script/generate. It is deliberately independent from the
// narration contract: generation decides the scenes, while the localized
// render fan-out materializes each selected clip.
type VideoRenderSpec struct {
	Enabled    bool                `json:"enabled,omitempty"`
	Watermark  *VideoWatermarkSpec `json:"watermark,omitempty"`
	Subtitles  *VideoSubtitlesSpec `json:"subtitles,omitempty"`
	OutputDir  string              `json:"output_dir,omitempty"`
	RequireGPU bool                `json:"require_gpu,omitempty"`
}

type VideoWatermarkSpec struct {
	Enabled  bool    `json:"enabled,omitempty"`
	Text     string  `json:"text,omitempty"`
	AssetID  string  `json:"asset_id,omitempty"`
	Position string  `json:"position,omitempty"`
	Opacity  float64 `json:"opacity,omitempty"`
	MarginPX int     `json:"margin_px,omitempty"`
}

type VideoSubtitlesSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"`
	StyleID string `json:"style_id,omitempty"`
}

// Normalize preserves the caller's explicit choices and enables the video
// path whenever either requested overlay is enabled. Empty values receive the
// same safe defaults as clip.render.
func (r *VideoRenderSpec) Normalize() {
	if r == nil {
		return
	}
	if r.Watermark != nil && r.Watermark.Enabled {
		r.Enabled = true
		if r.Watermark.Position == "" {
			r.Watermark.Position = "top_right"
		}
		if r.Watermark.Opacity == 0 {
			r.Watermark.Opacity = 1
		}
	}
	if r.Subtitles != nil && r.Subtitles.Enabled {
		r.Enabled = true
		if r.Subtitles.Mode == "" {
			r.Subtitles.Mode = "burn"
		}
	}
}
