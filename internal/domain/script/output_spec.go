// Package script — output_spec.go defines the canonical ScriptSpec
// (HOW to generate) and OutputSpec (WHAT to produce) contracts.
//
// PR 8 (June 2026): introduces the canonical tri-state Toggle type
// for future-migration but keeps OutputSpec fields as bool for
// backward compatibility with preset_resolver,
// handler_legacy_adapters, generation_normalizer, and postprocessor
// wiring. The full bool→Toggle cutover is a follow-up PR once those
// call sites migrate; until then, Toggle.Resolve() is available
// for any caller that wants tri-state semantics, and Toggle.AsBool()
// converts to bool at the OutputSpec boundary.
//
// PR 7 (June 2026): unchanged from prior turn — GenerationEnvelopeResult
// unification consolidated with GenerationEnvelopeItem.
//
// No durable field uses interface{}, any, or map[string]any.
package script

// Toggle is the canonical tri-state for OutputSpec postprocessor
// flags. The precedence chain is:
//
//	ToggleDefault  — caller did not specify; defer to preset/config/safety
//	ToggleEnabled  — caller explicitly enabled this processor
//	ToggleDisabled — caller explicitly disabled this processor
//
// Resolve() algorithm:
//
//	if caller != ToggleDefault: caller
//	elif preset != ToggleDefault: preset
//	elif config != ToggleDefault: config
//	else: safety
type Toggle string

const (
	// ToggleDefault — no preference; downstream layers decide.
	ToggleDefault Toggle = "default"
	// ToggleEnabled — explicitly enabled.
	ToggleEnabled Toggle = "enabled"
	// ToggleDisabled — explicitly disabled.
	ToggleDisabled Toggle = "disabled"
)

// Resolve applies the precedence chain to a sequence of Toggles
// (caller, preset, config, safety) and returns the resolved value.
func (t Toggle) Resolve(caller, preset, config, safety Toggle) Toggle {
	if caller != ToggleDefault {
		return caller
	}
	if preset != ToggleDefault {
		return preset
	}
	if config != ToggleDefault {
		return config
	}
	return safety
}

// AsBool collapses the resolved toggle to a boolean. ToggleDisabled
// → false. ToggleEnabled or ToggleDefault → true.
func (t Toggle) AsBool() bool {
	return t != ToggleDisabled
}

// ── ScriptSpec ─────────────────────────────────────────────────────

// ScriptSpec controls the generation behaviour: sizing, style, and
// prompt versioning. Identity fields (Language, Tone, Model) live
// on GenerationItemV2; the normalizer merges them into the resolved
// plan.
type ScriptSpec struct {
	TargetWords         int      `json:"target_words,omitempty"`
	Duration            int      `json:"duration,omitempty"`
	MinWords            int      `json:"min_words,omitempty"`
	SegmentWords        int      `json:"segment_words,omitempty"`
	SegmentTopics       []string `json:"segment_topics,omitempty"`
	SentencesPerImage   int      `json:"sentences_per_image,omitempty"`
	ImagesPerScene      int      `json:"images_per_scene,omitempty"`
	Style               string   `json:"style,omitempty"`
	Guidelines          string   `json:"guidelines,omitempty"`
	TranscriptPolicy    string   `json:"transcript_policy,omitempty"`
	OrderingStrategy    string   `json:"ordering_strategy,omitempty"`
	PromptVersion       string   `json:"prompt_version,omitempty"`
	EditorPromptVersion string   `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string   `json:"qa_prompt_version,omitempty"`
	ForceRefresh        bool     `json:"force_refresh,omitempty"`
	UseMemory           bool     `json:"use_memory,omitempty"`
}

// ── OutputSpec ─────────────────────────────────────────────────────

// OutputSpec declares which post-generation artifacts to produce.
// PR 8 (June 2026): the postprocessor flags remain as bool
// (transitional). A future PR migrates to the tri-state Toggle
// type once all consumer sites (preset_resolver,
// handler_legacy_adapters, generation_normalizer, postprocessor
// wiring) are updated in lock-step. Until then, callers that want
// tri-state semantics can use Toggle.Resolve() internally and convert
// to bool via Toggle.AsBool() at the boundary.
type OutputSpec struct {
	// ── Postprocessors (bool — fully compatible with prior contracts) ──
	ExtractEntities  bool `json:"extract_entities,omitempty"`
	GenerateMetadata bool `json:"generate_metadata,omitempty"`

	// Deprecated: GenerateVoiceover is no longer an inline postprocessor.
	// Fase 2 Spina Dorsale (July 2026): voiceovers are now produced by
	// a separate voiceover.generate downstream job. This flag is kept
	// for backward compatibility; setting it has no effect on the
	// script.generate pipeline.
	GenerateVoiceover bool `json:"generate_voiceover,omitempty"`

	// Deprecated: GenerateSceneImages is no longer an inline postprocessor.
	// Fase 2 Spina Dorsale (July 2026): scene images are now produced by
	// a separate images.generate downstream job. This flag is kept
	// for backward compatibility; setting it has no effect on the
	// script.generate pipeline.
	GenerateSceneImages bool `json:"generate_scene_images,omitempty"`

	// Deprecated: GenerateDocument is no longer an inline postprocessor.
	// Fase 2 Spina Dorsale (July 2026): Google Doc creation is now
	// produced by a separate document.generate downstream job. This
	// flag is kept for backward compatibility; setting it has no
	// effect on the script.generate pipeline.
	GenerateDocument bool `json:"generate_document,omitempty"`

	// ── Persistence ──────────────────────────────────────────────────
	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`

	// ── Voiceover options ────────────────────────────────────────────
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// ── Document options ─────────────────────────────────────────────
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Formatting ──────────────────────────────────────────────────
	MaxChars  int    `json:"max_chars,omitempty"`
	OutputFmt string `json:"output_fmt,omitempty"`

	// ── Translations ────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`
}

// HasAnyPostprocessor returns true when at least one postprocessor
// flag is enabled.
//
// Fase 2 Spina Dorsale (July 2026): GenerateVoiceover, GenerateSceneImages,
// and GenerateDocument are no longer inline postprocessors — they are
// produced by separate downstream jobs. Only script-internal transformations
// (entities, metadata) are checked here.
func (o *OutputSpec) HasAnyPostprocessor() bool {
	return o.ExtractEntities ||
		o.GenerateMetadata
}
