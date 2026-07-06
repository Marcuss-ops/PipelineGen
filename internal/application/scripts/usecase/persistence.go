// Package usecase — persistence.go: result-building helpers.
//
// Extracted from generate_one_usecase_persist.go (July 2026).
// Owns: buildGenerationResult.
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
	// engineResult.Output.SpecScene when populated.
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
	// the post-processor walk BEFORE this function is called.
	if engineResult.ClipEvidence != nil {
		clipIDs := engineResult.ClipEvidence.AcceptedClipIDs
		if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
			clipIDs = clipIDs[:plan.NumClips]
		}
		sourceTrace.AcceptedClipIDs = append([]string(nil), clipIDs...)
	}

	// Merge postprocessor results into canonical Artifacts.
	if postResult != nil {
		result.Artifacts.Entities = postResult.Entities
		if postResult.Entities != nil {
			if raw, err := scriptdto.SerializeEntityResultRoundTrip(postResult.Entities); err == nil {
				result.Artifacts.EntitiesJSON = raw
			}
		}

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

		if postResult.DocLink != "" {
			result.Artifacts.Document = &scriptpkg.DocumentArtifact{
				DocLink: postResult.DocLink,
				DocID:   postResult.DocID,
				Status:  "completed",
			}
		}
	}

	if postResult != nil && len(postResult.Warnings) > 0 {
		result.Warnings = append(result.Warnings, postResult.Warnings...)
	}

	result.Source = sourceTrace
	return result
}
