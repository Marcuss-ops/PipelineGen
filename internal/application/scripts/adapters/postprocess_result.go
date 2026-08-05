package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// PostProcessResult carries the output of a single processor.
type PostProcessResult struct {
	DocID             string
	DocLink           string
	VisualPlans       []mediamemory.SceneVisualPlan
	VisualAssignments []mediadomain.VisualAssignment `json:"visual_assignments,omitempty"`
	Entities          *scriptpkg.EntityResult
	VidRushSegments   []scriptpkg.VidRushSegmentResult
	Metadata          []scriptpkg.VideoMetadata
	Voiceovers        []SceneVoiceover
	SceneImages       []SceneImage
	ScriptID          int64
	AlreadyPersisted  bool
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
	TranslatedText      string                       `json:"translated_text,omitempty"`
	TranslatedSpecScene scriptpkg.SpecSceneOutput    `json:"translated_specscene,omitempty"`
	OriginalText        string                       `json:"original_text,omitempty"`
	OriginalSpecScene   scriptpkg.SpecSceneOutput    `json:"original_specscene,omitempty"`
	EffectiveLanguage   string                       `json:"effective_language,omitempty"`
	StageProgress       map[string]job.StageProgress `json:"stage_progress,omitempty"`
	UpdatedSpecScene    scriptpkg.SpecSceneOutput    `json:"updated_specscene,omitempty"`
	// SpecSceneChanged is internal pipeline metadata used to force a
	// document refresh after durable location reconciliation.
	SpecSceneChanged bool `json:"-"`
}

// IsEmpty reports whether the result carries no observable work.
func (r *PostProcessResult) IsEmpty() bool {
	if r == nil {
		return true
	}
	// A published document is observable output even when it does not
	// mutate the SpecScene or populate another artifact collection.
	if strings.TrimSpace(r.DocID) != "" || strings.TrimSpace(r.DocLink) != "" {
		return false
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
	if len(r.VisualAssignments) > 0 {
		return false
	}
	if len(r.VidRushSegments) > 0 {
		return false
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
	if len(r.UpdatedSpecScene.Scenes) > 0 {
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
