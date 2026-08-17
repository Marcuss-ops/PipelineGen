// Package script — resolved_plan.go defines the ResolvedGenerationPlan,
// the canonical contract between normalization and engine execution.
// After normalization, validation, and source resolution, every
// GenerationItemV2 produces exactly one ResolvedGenerationPlan.
//
// No durable field uses any, any, or map[string]any.
package script

import (
	"slices"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// ResolvedGenerationPlan is the fully resolved, validated plan that
// the engine consumes. It carries:
//   - identity fields (title, language, tone, model, mode)
//   - the canonical source text to feed to the LLM
//   - sizing constraints (target words, min words, duration)
//   - the resolved clip evidence (when the source involved clips)
//   - the list of postprocessors to run
//   - output options (drive folder, formatting)
//
// Every field has been normalized through the precedence chain:
//
//	caller explicit > preset > configuration > safety default
type ResolvedGenerationPlan struct {
	// ID echoes GenerationItemV2.ID for result correlation.
	ID string `json:"id,omitempty"`

	// ── Identity ──────────────────────────────────────────────────────
	Title     string    `json:"title"`
	Project   string    `json:"project,omitempty"`
	Topic     string    `json:"topic"`
	Language  string    `json:"language"`
	Tone      string    `json:"tone"`
	Model     string    `json:"model"`
	Mode      string    `json:"mode"` // "text", "clip_to_script", "batch"
	MediaMode MediaMode `json:"media_mode,omitempty"`
	AudioMode string    `json:"audio_mode,omitempty"`
	// Timing is the canonical voiceover timing policy resolved for this
	// item (copied from the caller's audio.timing). nil means the pipeline
	// applies the canonical defaults (best_effort / word / [json]) —
	// timing capture is never implicitly mandatory.
	Timing *audio.TimingRequest `json:"voiceover_timing,omitempty"`

	// ── Source text ───────────────────────────────────────────────────
	// SourceText is the canonical resolved text fed to the engine.
	// For text sources it's the topic+source_text+guidelines assembly.
	// For clip/catalog/search sources it's the clip evidence text.
	SourceText       string                `json:"source_text"`
	ResearchSources  []SourceReference     `json:"research_sources,omitempty"`
	ResearchEvidence *ResearchEvidencePack `json:"research_evidence,omitempty"`

	// Guidelines are the writing style constraints.
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clip evidence ─────────────────────────────────────────────────
	// ClipEvidence is set when the source involved clips; nil for
	// pure text generation.
	ClipEvidence *ClipEvidence `json:"clip_evidence,omitempty"`
	// SearchResults preserves the resolver's retrieval trace through the
	// plan/engine boundary for the canonical GenerationResult source trace.
	SearchResults []SearchResultItem `json:"search_results,omitempty"`

	// ── Sizing ────────────────────────────────────────────────────────
	TargetWords       int             `json:"target_words,omitempty"`
	SingleScene       bool            `json:"single_scene,omitempty"`
	Duration          int             `json:"duration,omitempty"`
	MinWords          int             `json:"min_words,omitempty"`
	NumClips          int             `json:"num_clips,omitempty"`
	IntroClipIDs      []string        `json:"intro_clip_ids,omitempty"`
	SegmentWords      int             `json:"segment_words,omitempty"`
	SegmentTopics     []string        `json:"segment_topics,omitempty"`
	Segments          []ScriptSegment `json:"segments,omitempty"`
	SentencesPerImage int             `json:"sentences_per_image,omitempty"`
	ImagesPerScene    int             `json:"images_per_scene,omitempty"`

	// ── Style ─────────────────────────────────────────────────────────
	Style string `json:"style,omitempty"`

	// ── Prompt (PR 2: split into typed roles) ─────────────────────────
	// The legacy single `Prompt string` field was removed because it
	// conflated three distinct concepts: model-input editorial prompt,
	// source-content fingerprint, and memory-gate cache key. That
	// created anti-patterns in callers ("plan.Prompt = resolved.Fingerprint"
	// wrote a hex hash into a model input; "Guidelines: sourceFingerprint"
	// wrote the same hash as editorial style). The roles are now strict
	// and explicitly typed:

	// RenderedPrompt is the editorial instructions sent to the LLM.
	// Contains style/sizing/source-text assembly but NEVER a fingerprint
	// hash. The engine reads this field alone when constructing the
	// ollama request. Use plan.RenderedPrompt as the only model input.
	RenderedPrompt string `json:"rendered_prompt,omitempty"`

	// SourceFingerprint identifies the resolved source aggregates
	// (clip set, resolved source text, resolved catalog digest).
	// Captures what the model writes but not the editorial prompt.
	// Used as a cache-key input but never sent to the model.
	SourceFingerprint string `json:"source_fingerprint,omitempty"`

	// CacheKey is the canonical memory-gate cache key, computed by
	// BuildCacheKey(plan). Hashes source fingerprint + language +
	// tone + style + model + sizing + prompt version + prompt
	// profile + source kind + real guidelines. Excludes output flags
	// (document/image/voiceover/entities/metadata/Drive folder),
	// OutputFmt, Languages, and ForceRefresh. The engine feeds this
	// to the memory gate; never sent to the model.
	CacheKey string `json:"cache_key,omitempty"`

	// PromptProfile records the prompt-engine profile/version used
	// to build RenderedPrompt (e.g. "default-v1"). Useful for A/B
	// testing variants without invalidating the cache per language
	// change.
	PromptProfile string `json:"prompt_profile,omitempty"`

	// SourceKind mirrors the source type at time of plan build
	// ("text", "clip", "catalog", "search"). Cache-key input —
	// same content from different sources should hit a single cache
	// row only when the model output is structurally identical.
	SourceKind string `json:"source_kind,omitempty"`

	// GroundingPolicy is the clip-grounding policy used when the
	// source involved clips. It is part of the generation fingerprint
	// so policy changes invalidate cached results.
	GroundingPolicy string `json:"grounding_policy,omitempty"`

	// FallbackPolicy is the clip-fallback policy used when the
	// source involved clips. It controls whether the pipeline is
	// allowed to fall back to prose when clip-native planning cannot
	// produce scenes.
	FallbackPolicy string `json:"fallback_policy,omitempty"`

	// ── Prompt versioning ─────────────────────────────────────────────
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`

	// ── Memory gate ───────────────────────────────────────────────────
	UseMemory    bool `json:"use_memory,omitempty"`
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// ── Postprocessors ────────────────────────────────────────────────
	// Postprocessors lists the postprocessors to run, in execution
	// order. Derived from OutputSpec flags after normalization.
	// Valid values: "entities", "metadata", "voiceover", "images",
	// "document", "persistence".
	Postprocessors []string `json:"postprocessors,omitempty"`

	// ── Output ────────────────────────────────────────────────────────
	DriveFolderID     string              `json:"drive_folder_id,omitempty"`
	DocsEnabled       bool                `json:"docs_enabled,omitempty"`
	DocsLanguages     []string            `json:"docs_languages,omitempty"`
	DocsFolderID      string              `json:"docs_folder_id,omitempty"`
	VoiceoverEnabled  Toggle              `json:"voiceover_enabled,omitempty"`
	VoiceoverGroup    string              `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string              `json:"voiceover_folder_id,omitempty"`
	MaxChars          int                 `json:"max_chars,omitempty"`
	OutputFmt         string              `json:"output_fmt,omitempty"` // PR 9: "json" only; "prose" REJECTED by the PR 6 validator
	SaveToDB          bool                `json:"save_to_db,omitempty"`
	StockEnabled      Toggle              `json:"stock_enabled,omitempty"`
	StockBindings     []StockBindingInput `json:"stock_bindings,omitempty"`

	// ── Translations ──────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// PR-TRANSLATE-SCRIPT-SPEC PR-5+PR-6 (2026-07-09): the canonical
	// opt-in trigger for the TranslationProcessor, copied from
	// OutputSpec.TranslateTo in BuildPlan so the postprocessor reads
	// a single source (the plan, NOT the request envelope). Empty
	// string = no translation requested (built-in prompt-side
	// collateral — the processor falls back to plan.Languages[0]
	// when this is empty AND the operator wants translation via the
	// legacy Languages[] field). godlike/06 SSOT: this is the SOLE
	// field the translator reads; processor_translation.go NEVER
	// reaches into the request envelope.
	TranslateTo string `json:"translate_to,omitempty"`

	// ── Token budget ──────────────────────────────────────────────────
	// NumPredict is the LLM num_predict override (0 = use server default).
	NumPredict int `json:"num_predict,omitempty"`
	// Temperature is the LLM temperature override (0 = use server default).
	Temperature float64 `json:"temperature,omitempty"`

	// ── Media plan ─────────────────────────────────────────────────────
	// MediaPlan carries the resolved visual-media plan for this item.
	// It is copied from GenerationItemV2 and made available to the
	// visual_planning postprocessor.
	MediaPlan media.MediaPlanSpec `json:"media_plan,omitempty"`

	// VideoMetadata is caller-provided YouTube metadata.
	// It is output metadata and must never be sent to the script LLM.
	VideoMetadata *VideoMetadata `json:"video_metadata,omitempty"`
}

// HasClips returns true when the plan carries clip evidence (the
// source involved one or more clips).
//
// Issue #2 (June 2026): the field read here is
// ClipEvidence.AcceptedClipIDs (renamed from ClipIDs). The
// semantic stays the same — any transcript-usable resolved clip
// counts. Use HasRenderableClips (added if needed) for the
// DriveLink-bearing subset.
func (p *ResolvedGenerationPlan) HasClips() bool {
	return p.ClipEvidence != nil && len(p.ClipEvidence.AcceptedClipIDs) > 0
}

// HasPostprocessor returns true when the named postprocessor is in
// the execution list.
func (p *ResolvedGenerationPlan) HasPostprocessor(name string) bool {
	for _, pp := range p.Postprocessors {
		if pp == name {
			return true
		}
	}
	return false
}

// NewResolvedGenerationPlan is the canonical defensive-copy
// constructor for ResolvedGenerationPlan. Every slice/map field on
// the returned instance is freshly-allocated so post-construction
// mutation of the input's slices/maps cannot reach the constructed
// instance.
//
// Scope of cloning — the slice fields callers have historically
// mutated after BuildPlan returned:
//
//   - SegmentTopics, Segments (text-segment metadata)
//   - Languages (translation target list)
//   - Postprocessors (postprocessor execution order)
//
// The embedded *ClipEvidence is re-cloned via NewClipEvidence so
// downstream code holding the plan's plan.ClipEvidence pointer
// cannot mutate the source ResolvedSource's clip evidence maps.
// The remaining scalar fields (strings, ints, bools, the
// *SearchResultItem-equivalent scalars) are copied by value as
// part of the struct copy and are inherently safe.
//
// godlike/06 SSOT (no mutation helper): the constructor is the
// SOLE canonical path that returns a snapshot-safe plan. After
// NewResolvedGenerationPlan returns, the plan's slices/maps are
// logically immutable from the construction-side contract; the
// caller MUST NOT introduce public mutex methods that mutate the
// maps (the same `godlike/06 no-mutation-helper` discipline that
// applies to NewClipEvidence).
//
// godlike/07 NO-FAKE-AVAILABILITY: scalar fields are read-only
// from the consumer's surface — no `WithClipEvidenceClipID(...)`
// companion. Re-cloning into a fresh instance is the canonical
// mutation path; the constructor's defensive-copy semantics
// means a single logical "edit" round trips through
// NewResolvedGenerationPlan.
//
// Nil-pointer contract: if input.ClipEvidence is nil the
// constructor leaves the embedded ClipEvidence nil. The
// downstream type assertion `plan.ClipEvidence != nil` is the
// canonical nil-guard at the consumer.
func NewResolvedGenerationPlan(p ResolvedGenerationPlan) *ResolvedGenerationPlan {
	p.SegmentTopics = slices.Clone(p.SegmentTopics)
	p.Segments = slices.Clone(p.Segments)
	p.Languages = slices.Clone(p.Languages)
	p.Postprocessors = slices.Clone(p.Postprocessors)
	if p.ClipEvidence != nil {
		p.ClipEvidence = NewClipEvidence(*p.ClipEvidence)
	}
	p.ResearchEvidence = p.ResearchEvidence.Clone()
	if p.Timing != nil {
		t := *p.Timing
		t.Formats = slices.Clone(p.Timing.Formats)
		p.Timing = &t
	}
	p.VideoMetadata = CloneVideoMetadata(p.VideoMetadata)
	return &p
}
