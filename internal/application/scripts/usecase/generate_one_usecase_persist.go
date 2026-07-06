package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Persist-phase helpers ─────────────────────────────────────────────

// buildGenerationResult constructs a GenerationResult from the
// engine and postprocessor outputs. PR 13: populates ONLY the
// canonical nested fields (Output, Source, Cache, Artifacts).
// The deprecated flat fields were removed in PR 13.
func buildGenerationResult(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
	timings scriptpkg.GenerationTimings,
) *scriptpkg.GenerationResult {
	cacheHit := engineResult.CacheStatus == "exact_hit"

	// PR 5: ScriptID is sourced from postResult.ScriptID (set by
	// PersistenceProcessor), NOT from engineResult.ScriptID (which
	// no longer exists post-PR 5). When the persistence processor
	// is not in the plan's Postprocessors list, ScriptID is zero.
	scriptIDFromPostprocess := int64(0)
	if postResult != nil {
		scriptIDFromPostprocess = postResult.ScriptID
	}

	// Issue #1 (June 2026): prefer postResult.FinalSpecScene over
	// engineResult.Output.SpecScene when populated. The
	// clip-bindings prose-fallback heuristic (FASE 3) can
	// synthesise scenes from prose when the model returns no
	// SpecScene. Pre-fix: buildGenerationResult always read the
	// pre-walk engineResult.Output.SpecScene, so the canonical
	// GenerationResult carried an empty SpecScene even when the
	// registry's PipelineResult.SynthesizedScenes held the
	// synthesised bundle — the JSON response, document body,
	// persistence row, image prompts, and voiceover plan all saw
	// empty scenes. Post-fix: registry.Run captures the post-walk
	// envelope in PipelineResult.FinalSpecScene; below selects it
	// when non-empty. The empty-aware guard keeps the
	// normal-model-output path unaffected (when the engine emits
	// scenes AND the heuristic does NOT engage, postResult
	// .FinalSpecScene mirrors input.SpecScene == engineResult
	// .Output.SpecScene, so the swap is a no-op).
	specScene := engineResult.Output.SpecScene
	if postResult != nil && len(postResult.FinalSpecScene.Scenes) > 0 {
		specScene = postResult.FinalSpecScene
	}

	result := &scriptpkg.GenerationResult{
		ItemID:   item.ID,
		ScriptID: scriptIDFromPostprocess,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		Output: scriptpkg.ScriptOutput{
			Text:      engineResult.Output.Text,
			WordCount: engineResult.WordCount,
			SpecScene: specScene,
		},
		Cache: scriptpkg.CacheResult{
			Status: engineResult.CacheStatus,
			Hit:    cacheHit,
		},
		Timings: timings,
	}

	// Populate Source trace.
	var sourceTrace scriptpkg.SourceTrace

	// PR 7 (June 2026): the model-emitted SpecScene goes through
	// the post-processor walk BEFORE this function is called. The
	// walkway runs ClipBindingsProcessor (when "clip_bindings" is
	// in plan.Postprocessors, which buildPostprocessorList always
	// inserts) which assigns `scene.Bindings.Clip = &ClipBinding{
	// ClipID: canonical, DriveLink: canonical_url }` UNCONDITIONALLY
	// for every scene. The slice header of
	// `result.Output.SpecScene.Scenes` is shared with the caller's
	// `engineResult.Output.SpecScene.Scenes` and `procInput.SpecScene
	// .Scenes`, so the mutations propagate to:
	//   1. DocumentProcessor when it builds the Google Doc HTML
	//      body (consumes the post-walk SpecScene).
	//   2. buildGenerationResult's `result.Output.SpecScene.Scenes`
	//      (consumed by the JSON response writer downstream).
	// Both paths now read the SAME final binding set; the pre-PR-7
	// duplicate loop that did "fill empty only" against a different
	// source-of-truth (engineResult.ClipEvidence) is REMOVED.
	//
	// PR 1 (June 2026): preserve the model-emitted SpecScene verbatim
	// for kind/text/id — the postprocessor walk never mutates those
	// fields. The binder only touches `scene.Bindings.Clip`.
	if engineResult.ClipEvidence != nil {
		// Issue #2 (June 2026): ClipEvidence.ClipIDs renamed to
		// AcceptedClipIDs (transcript-usable set). The SourceTrace
		// field already called this AcceptedClipIDs (per legacy
		// contract) so the assignment is semantically a 1:1
		// pass-through — the SourceTrace field description is
		// unchanged and now matches the ClipEvidence source by
		// name.
		clipIDs := engineResult.ClipEvidence.AcceptedClipIDs
		if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
			clipIDs = clipIDs[:plan.NumClips]
		}
		sourceTrace.AcceptedClipIDs = append([]string(nil), clipIDs...)
	}

	// Merge postprocessor results into canonical Artifacts.
	if postResult != nil {
		// Entities (PR 3, June 2026): copy the typed
		// *scriptpkg.EntityResult from postResult to
		// result.Artifacts.Entities (canonical V1). The
		// read-only EntitiesJSON artefact is derived by
		// JSON-marshalling the typed result at the boundary
		// (NEW producers MUST populate Entities directly;
		// consumers MUST read fields rather than parsing the
		// raw JSON). Persists only for downstream consumers
		// that have not yet migrated to the typed shape.
		result.Artifacts.Entities = postResult.Entities
		if postResult.Entities != nil {
			if raw, err := scriptdto.SerializeEntityResultRoundTrip(postResult.Entities); err == nil {
				result.Artifacts.EntitiesJSON = raw
			}
		}

		// Metadata.
		if len(postResult.VideoMetadata) > 0 {
			meta := make([]scriptpkg.VideoMetadata, len(postResult.VideoMetadata))
			for i, m := range postResult.VideoMetadata {
				meta[i] = scriptpkg.VideoMetadata{
					Language:    m.Language,
					Title:       m.Title,
					Description: m.Description,
					Tags:        m.Tags,
				}
			}
			result.Artifacts.Metadata = meta
		}

		// Scene images — enrich SpecScene bindings.
		if len(postResult.Scenes) > 0 {
			for _, s := range postResult.Scenes {
				if s.Index < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[s.Index]
					if sc.Bindings.Image == nil {
						sc.Bindings.Image = &scriptpkg.ImageBinding{}
					}
					sc.Bindings.Image.URL = s.URL
					sc.Bindings.Image.Status = "generated"
				}
			}
		}

		// Voiceovers — enrich SpecScene bindings.
		if len(postResult.Voiceovers) > 0 {
			for _, v := range postResult.Voiceovers {
				if v.SceneIndex < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[v.SceneIndex]
					if sc.Bindings.Voiceover == nil {
						sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
					}
					sc.Bindings.Voiceover.Status = v.Status
					sc.Bindings.Voiceover.Link = v.Link
					sc.Bindings.Voiceover.LocalPath = v.LocalPath
				}
			}
		}

		// Document.
		if postResult.DocLink != "" {
			result.Artifacts.Document = &scriptpkg.DocumentArtifact{
				DocLink: postResult.DocLink,
				DocID:   postResult.DocID,
				Status:  "completed",
			}
		}
	}

	// PR 2 (June 2026): propagate per-postprocessor warnings (best-effort
	// failures + missing-registered-at-runtime observations) into the
	// canonical GenerationResult.Warnings. GenerationResult.Warnings is
	// already serialised downstream by generation_job.go + response.go.
	if postResult != nil && len(postResult.Warnings) > 0 {
		result.Warnings = append(result.Warnings, postResult.Warnings...)
	}

	result.Source = sourceTrace

	return result
}

// Deprecated: error types and helpers from the legacy GenerationSpec
// bridge were removed in PR 3; processors now consume the typed
// EntityExtractor / MetadataGenerator ports directly.
