// Package script — resolved_plan.go defines the ResolvedGenerationPlan,
// the canonical contract between normalization and engine execution.
// After normalization, validation, and source resolution, every
// GenerationItemV2 produces exactly one ResolvedGenerationPlan.
//
// No durable field uses interface{}, any, or map[string]any.
package script

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
	Title    string `json:"title"`
	Topic    string `json:"topic"`
	Language string `json:"language"`
	Tone     string `json:"tone"`
	Model    string `json:"model"`
	Mode     string `json:"mode"` // "text", "clip_to_script", "batch"

	// ── Source text ───────────────────────────────────────────────────
	// SourceText is the canonical resolved text fed to the engine.
	// For text sources it's the topic+source_text+guidelines assembly.
	// For clip/catalog/search sources it's the clip evidence text.
	SourceText string `json:"source_text"`

	// Guidelines are the writing style constraints.
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clip evidence ─────────────────────────────────────────────────
	// ClipEvidence is set when the source involved clips; nil for
	// pure text generation.
	ClipEvidence *ClipEvidence `json:"clip_evidence,omitempty"`

	// ── Sizing ────────────────────────────────────────────────────────
	TargetWords       int      `json:"target_words,omitempty"`
	Duration          int      `json:"duration,omitempty"`
	MinWords          int      `json:"min_words,omitempty"`
	NumClips          int      `json:"num_clips,omitempty"`
	SegmentWords      int      `json:"segment_words,omitempty"`
	SegmentTopics     []string `json:"segment_topics,omitempty"`
	SentencesPerImage int      `json:"sentences_per_image,omitempty"`
	ImagesPerScene    int      `json:"images_per_scene,omitempty"`

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
	DriveFolderID     string `json:"drive_folder_id,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`
	MaxChars          int    `json:"max_chars,omitempty"`
	OutputFmt         string `json:"output_fmt,omitempty"` // PR 9: "json" only; "prose" REJECTED by the PR 6 validator
	SaveToDB          bool   `json:"save_to_db,omitempty"`

	// ── Translations ──────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// ── Token budget ──────────────────────────────────────────────────
	// NumPredict is the LLM num_predict override (0 = use server default).
	NumPredict int `json:"num_predict,omitempty"`
	// Temperature is the LLM temperature override (0 = use server default).
	Temperature float64 `json:"temperature,omitempty"`
}

// HasClips returns true when the plan carries clip evidence (the
// source involved one or more clips).
func (p *ResolvedGenerationPlan) HasClips() bool {
	return p.ClipEvidence != nil && len(p.ClipEvidence.ClipIDs) > 0
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
