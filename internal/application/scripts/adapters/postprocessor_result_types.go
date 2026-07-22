// Package adapters — postprocessor_document.go: pipeline result types.
//
// Extracted from postprocessor_registry.go (July 2026).
// Owns: PipelineResult, PostProcessResult, ProcessInput, IsEmpty.
package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// PipelineResult aggregates the postprocessor outputs across the
// full Run sequence. PR 5: it's the typed merged view that
// generation_job.go writes to script/section rows via the
// canonical artifacts contract.
type PipelineResult struct {
	Entities         *scriptpkg.EntityResult
	VideoMetadata    []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	Scenes           []SceneImage
	ScriptID         int64
	AlreadyPersisted bool
	// StageDurations maps processor name → wall-clock milliseconds
	// consumed. Populated by Run() before merge. P1 #10 (June 2026).
	StageDurations map[string]int64 `json:"stage_durations,omitempty"`
	// SynthesizedScenes mirrors PostProcessResult.SynthesizedScenes
	// after mergePostProcessResult — the canonical pipeline-level
	// surface for processors that reconstructed scenes from prose.
	// FASE 3 (June 2026): added for the clip-bindings prose-fallback
	// heuristic. omitempty keeps the JSON envelope stable for
	// callers that did not opt into the heuristic.
	SynthesizedScenes []scriptpkg.SpecScene `json:"synthesized_scenes,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	// ArtlistClipSuggestions carries Artlist clip matches discovered
	// by the ClipSearchProcessor from the artlist_phrases extracted
	// by the upstream EntitiesProcessor. PR-CLIP-SEARCH-WIRING (July 2026).
	ArtlistClipSuggestions []ArtlistClipMatch `json:"artlist_clip_suggestions,omitempty"`
	// FinalSpecScene (Issue #1, June 2026) is the canonical
	// post-walk SpecScene surface consumed by buildGenerationResult.
	// Pre-fix: buildGenerationResult read from engineResult.Output
	// .SpecScene (the pre-walk view). The clip_bindings prose-fallback
	// synthesised scenes into PipelineResult.SynthesizedScenes, but
	// the synthesised bundle never reached GenerationResult.Output
	// .SpecScene — the JSON envelope went out with empty scenes even
	// when the heuristic engaged. Post-fix: mergePostProcessResult
	// writes SynthesizedScenes back into the registry-local
	// ProcessInput.SpecScene.Scenes (so document/persistence see
	// populated scenes during the same Run) AND captures the
	// post-walk envelope here. buildGenerationResult prefers
	// postResult.FinalSpecScene with the empty-aware fallback so
	// the normal-model-output path is unaffected. omitempty keeps
	// the JSON envelope stable for calls that did not exercise any
	// postprocessor.
	FinalSpecScene scriptpkg.SpecSceneOutput `json:"final_specscene,omitempty"`
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): the canonical
	// pipeline-level surface for the translated text + translated
	// SpecScene (Last-writer-wins from mergePostProcessResult).
	// omitempty so callers that did not opt into translation don't
	// see a serialisation diff. The buildGenerationResult consumer
	// in usecase/generate_one_usecase.go prefers these fields
	// (when populated) so the post-translation version is the
	// canonical wire-shape observed downstream.
	TranslatedText      string                    `json:"translated_text,omitempty"`
	TranslatedSpecScene scriptpkg.SpecSceneOutput `json:"translated_specscene,omitempty"`
	// Original* preserve the source-language surface when translation runs
	// before persistence, allowing the persistence processor to write both
	// canonical language rows.
	OriginalText      string                    `json:"original_text,omitempty"`
	OriginalSpecScene scriptpkg.SpecSceneOutput `json:"original_specscene,omitempty"`
	EffectiveLanguage string                    `json:"effective_language,omitempty"`
}

// PostProcessResult carries the output of a single processor.
type PostProcessResult struct {
	Entities         *scriptpkg.EntityResult
	Metadata         []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	SceneImages      []SceneImage
	ScriptID         int64
	AlreadyPersisted bool
	// Changed is set by mutative processors (e.g. ClipBindingsProcessor)
	// that modify input state but don't produce canonical output fields.
	// When true, IsEmpty() returns false even if all output fields
	// are zero. P1 #10 (June 2026).
	Changed bool `json:"changed,omitempty"`
	// DurationMs is the wall-clock time this processor consumed, set
	// by the registry's Run() method before merge. P1 #10 (June 2026).
	DurationMs int64 `json:"duration_ms,omitempty"`
	// SynthesizedScenes carries scene bundles constructed by an
	// individual processor when the canonical SpecScene pipeline
	// could not produce them. The clip-bindings prose-fallback
	// heuristic (FASE 3, June 2026) is the canonical emitter —
	// small local models (gemma2:2b / gemma4:e4b) commonly return
	// prose without SpecScene.scenes, so the binder synthesises N
	// scenes from input.Text and binds clips 1:1. Without this
	// field the binder would be flagged "returned empty output" by
	// the registry's IsEmpty check, even though meaningful work
	// happened. omitempty so existing emitters (entities /
	// metadata / voiceover / images / document / persistence) do
	// not see a serialisation diff.
	SynthesizedScenes []scriptpkg.SpecScene `json:"synthesized_scenes,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	// ArtlistClipSuggestions carries Artlist clip matches discovered
	// by the ClipSearchProcessor. PR-CLIP-SEARCH-WIRING (July 2026).
	ArtlistClipSuggestions []ArtlistClipMatch `json:"artlist_clip_suggestions,omitempty"`
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): the canonical
	// pipeline-level surface for translated text + translated
	// SpecScene. The TranslationProcessor populates both in
	// addition to the in-place mutation on ProcessInput (per the
	// MIX design; the in-place mutation handles downstream
	// document/persistence within the same Run, the explicit
	// fields handle cross-Run observability + buildGenerationResult
	// preference). omitempty so callers that did not request
	// translation don't see a serialisation diff.
	TranslatedText      string                    `json:"translated_text,omitempty"`
	TranslatedSpecScene scriptpkg.SpecSceneOutput `json:"translated_specscene,omitempty"`
	OriginalText        string                    `json:"original_text,omitempty"`
	OriginalSpecScene   scriptpkg.SpecSceneOutput `json:"original_specscene,omitempty"`
	EffectiveLanguage   string                    `json:"effective_language,omitempty"`
}

// IsEmpty reports whether the result carries no observable work.
func (r *PostProcessResult) IsEmpty() bool {
	if r == nil {
		return true
	}
	// P1 #10 (June 2026): Changed flag lets mutative processors
	// (e.g. ClipBindingsProcessor) signal "I did real work" without
	// populating canonical output fields. Prevents false "empty
	// output" warnings.
	if r.Changed {
		return false
	}
	if r.Entities != nil {
		if len(r.Entities.Persons) > 0 || len(r.Entities.Places) > 0 || len(r.Entities.Concepts) > 0 {
			return false
		}
	}
	if len(r.Metadata) > 0 {
		return false
	}
	if len(r.Voiceovers) > 0 {
		return false
	}
	if len(r.SceneImages) > 0 {
		return false
	}
	if r.ScriptID > 0 || r.AlreadyPersisted {
		return false
	}
	// FASE 3 (June 2026): SynthesizedScenes counts as observable
	// work. Without this, the clip_bindings prose-fallback heuristic
	// is functionally complete but the registry still complains
	// "returned empty output" — choking the job on a false-positive.
	if len(r.SynthesizedScenes) > 0 {
		return false
	}
	// PR-CLIP-SEARCH-WIRING (July 2026): clip search results count
	// as observable work.
	if len(r.ArtlistClipSuggestions) > 0 {
		return false
	}
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): translated text +
	// translated SpecScene count as observable work. The MIX design
	// keeps the in-place mutation on ProcessInput.SpecScene (so
	// downstream document/persistence see the translated bundle
	// within the same Run) AND surfaces the explicit fields here
	// for cross-Run observability. Without this branch, the registry
	// would flag the TranslationProcessor "returned empty output"
	// when only in-place mutation occurred (a false-positive bug
	// surfaced in the pre-PR-5 audit).
	if strings.TrimSpace(r.TranslatedText) != "" {
		return false
	}
	if len(r.TranslatedSpecScene.Scenes) > 0 {
		return false
	}
	return true
}

// ProcessInput is the typed envelope passed to every postprocessor.
type ProcessInput struct {
	Text              string
	WordCount         int
	SpecScene         scriptpkg.SpecSceneOutput
	OriginalText      string
	OriginalSpecScene scriptpkg.SpecSceneOutput
	ModelUsed         string
	CacheStatus       string
	SourceTrace       *scriptpkg.ClipEvidence
	PriorArtifacts    map[string]PostProcessResult
	EffectiveLanguage string

	// Entities carries the entity-extraction result, populated by
	// mergePostProcessResult when the entities processor produces
	// output. Threaded through to BuildGenerationDocumentHTML so
	// the Google Doc renders the <h2>Entities</h2> section when
	// non-empty. Nil until the entities processor runs.
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026).
	Entities *scriptpkg.EntityResult

	// Metadata carries the video-metadata result, populated by
	// mergePostProcessResult when the metadata processor produces
	// output. Threaded through to BuildGenerationDocumentHTML so
	// the Google Doc renders the <h2>Video Metadata</h2> section
	// when non-empty. Nil until the metadata processor runs.
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026).
	Metadata []scriptpkg.VideoMetadata

	// Provenance carries the provisional generation provenance block.
	// The document processor fills DocID/DocLink after creating or
	// updating the Google Doc and embeds the complete block into the
	// document HTML. Populated by GenerateOneUseCase before Run.
	// PR-PROVENANCE (July 2026).
	Provenance *scriptpkg.GenerationProvenance
}
