// Package adapters — pipeline result types.
//
// PipelineResult is the aggregate result of the postprocessor pipeline.
package adapters

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PipelineResult aggregates the postprocessor outputs across the
// full Run sequence. PR 5: it's the typed merged view that
// generation_job.go writes to script/section rows via the
// canonical artifacts contract.
type PipelineResult struct {
	DocID                   string
	DocLink                 string
	DocumentRenderer        string `json:"document_renderer,omitempty"`
	DocumentSpecSceneSHA256 string `json:"document_specscene_sha256,omitempty"`
	DocumentSceneCount      int    `json:"document_scene_count,omitempty"`
	DocumentLanguage        string `json:"document_language,omitempty"`
	VisualPlans             []mediamemory.SceneVisualPlan
	VisualAssignments       []mediadomain.VisualAssignment `json:"visual_assignments,omitempty"`
	Entities                *scriptpkg.EntityResult
	VidRushSegments         []scriptpkg.VidRushSegmentResult
	VideoMetadata           []scriptpkg.VideoMetadata
	Voiceovers              []SceneVoiceover
	Scenes                  []SceneImage
	ScriptID                int64
	AlreadyPersisted        bool
	StageProgress           map[string]job.StageProgress `json:"stage_progress,omitempty"`
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
	// SpecSceneChanged is internal pipeline metadata used to force a
	// document refresh after durable location reconciliation.
	SpecSceneChanged bool `json:"-"`
}
