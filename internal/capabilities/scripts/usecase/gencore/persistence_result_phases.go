package gencore

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func resolveResponseSpecScene(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
) scriptpkg.SpecSceneOutput {
	// Issue #1 (June 2026): prefer postResult.FinalSpecScene over
	// engineResult.Output.SpecScene when populated.
	specScene := engineResult.Output.SpecScene
	if postResult != nil && len(postResult.FinalSpecScene.Scenes) > 0 {
		specScene = postResult.FinalSpecScene
	}
	// The final response is the last contract boundary. Reproject resolved
	// identity images from the materialized segment candidates here as well:
	// a later annotation/synthesis postprocessor must not erase an image that
	// has already passed download, Drive, persistence, and index validation.
	if postResult != nil && len(postResult.VidRushSegments) > 0 {
		specScene = adapters.ProjectEntityImageBindings(
			specScene,
			postResult.VidRushSegments,
			plan.MediaPlan.Extraction.EntityImages,
		)
	}
	for i := range specScene.Scenes {
		if specScene.Scenes[i].Annotations != nil {
			specScene.Scenes[i].Annotations = adapters.RebaseSceneAnnotations(
				specScene.Scenes[i].Annotations,
				specScene.Scenes[i].Text,
			)
		}
	}
	specScene = sanitizeSpecSceneOutputForResponse(specScene)
	// Preserve the caller's render contract at the final response boundary.
	// Post-processors may replace the scene list, but must not drop the
	// requested watermark/subtitle materialization settings.
	specScene.Render = item.Output.Render
	specScene.Render.Normalize()
	return specScene
}

func buildGenerationResultCore(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
	specScene scriptpkg.SpecSceneOutput,
	cacheHit bool,
) *scriptpkg.GenerationResult {
	// PR 5: ScriptID is sourced from postResult.ScriptID (set by
	// PersistenceProcessor), NOT from engineResult.ScriptID (which
	// no longer exists post-PR 5). When the persistence processor
	// is not in the plan's Postprocessors list, ScriptID is zero.
	scriptIDFromPostprocess := int64(0)
	if postResult != nil {
		scriptIDFromPostprocess = postResult.ScriptID
	}

	// PR-TRANSLATION-PIPELINE-2026-07-09: prefer translated text over
	// the engine's original output when the TranslationProcessor
	// succeeded. Without this, the API response always shows the
	// original (e.g. English) text even when translation to Italian
	// was requested and completed.
	outputText := engineResult.Output.Text
	if postResult != nil && strings.TrimSpace(postResult.TranslatedText) != "" {
		outputText = postResult.TranslatedText
	}

	result := &scriptpkg.GenerationResult{
		StageProgress: func() map[string]job.StageProgress {
			if postResult == nil {
				return nil
			}
			return postResult.StageProgress
		}(),
		ItemID:   item.ID,
		ScriptID: scriptIDFromPostprocess,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		Script: scriptpkg.ScriptSummary{
			Text:      outputText,
			WordCount: engineResult.WordCount,
		},
		// Sprint 1.3 (godlike/08): Status is the canonical per-item
		// outcome enum. It is NOT set here — the orchestrator's
		// classify phase (ClassifyGenerationStatus in status_classifier.go)
		// is the SOLE writer in the success path. Leaving Status empty
		// here keeps the build-phase a pure data-construction step and
		// lets the classifier apply the canonical
		// build → enforce → quality → warnings → classify → emit order.
		Output: scriptpkg.ScriptOutput{
			Text:      outputText,
			WordCount: engineResult.WordCount,
			SpecScene: specScene,
		},
		Cache: scriptpkg.CacheResult{
			Status: engineResult.CacheStatus,
			Hit:    cacheHit,
			Script: scriptCacheStatus(engineResult.CacheStatus),
		},
	}

	// Source trace is assembled by the dedicated projection helper so this
	// function remains focused on the response and artifact assembly.
	result.Source = buildGenerationSourceTrace(plan, engineResult)
	return result
}

// mergePostprocessorArtifacts merges the canonical postprocessor output into
// the result and returns the authoritative final spec scene.
func mergePostprocessorArtifacts(
	ctx context.Context,
	result *scriptpkg.GenerationResult,
	plan scriptpkg.ResolvedGenerationPlan,
	postResult *adapters.PipelineResult,
	cache scriptports.VidRushCachePort,
	cacheHit bool,
) scriptpkg.SpecSceneOutput {
	specScene := result.Output.SpecScene
	if len(postResult.VidRushSegments) > 0 {
		postResult.VidRushSegments = adapters.DeduplicateVidRushSegments(postResult.VidRushSegments)
		if plan.MediaPlan.Materialization.Mode == mediadomain.MaterializationMetadataOnly {
			result.Segments = append([]scriptpkg.VidRushSegmentResult(nil), postResult.VidRushSegments...)
		} else {
			result.Segments = adapters.FinalizeVidRushBindingsWithCache(ctx, postResult.VidRushSegments, plan.MediaPlan.ForceRefreshBindings, cache)
		}
		if cacheHit {
			promoteCachedVidRushStates(result.Segments)
		}
		result.Cache.Segments = make(map[string]scriptpkg.SegmentCacheState, len(result.Segments))
		for _, seg := range result.Segments {
			if strings.TrimSpace(seg.SegmentID) == "" {
				continue
			}
			result.Cache.Segments[seg.SegmentID] = seg.Cache
			for _, warning := range seg.Warnings {
				if !containsGenerationWarning(result.Warnings, warning) {
					result.Warnings = append(result.Warnings, warning)
				}
			}
		}
		// Reproject from the exact response segments after final binding
		// finalization. This is the authoritative candidate surface and
		// cannot be stale or be replaced by an intermediate postprocessor.
		specScene = adapters.ProjectEntityImageBindings(
			specScene,
			result.Segments,
			plan.MediaPlan.Extraction.EntityImages,
		)
		result.Output.SpecScene = sanitizeSpecSceneOutputForResponse(specScene)
	}
	result.Artifacts.Entities = postResult.Entities
	if postResult.Entities != nil {
		if raw, err := SerializeEntityResultRoundTrip(postResult.Entities); err == nil {
			result.Artifacts.EntitiesJSON = raw
		}
	}

	if len(postResult.VideoMetadata) > 0 {
		meta := make([]scriptpkg.VideoMetadata, len(postResult.VideoMetadata))
		for i, m := range postResult.VideoMetadata {
			meta[i] = scriptpkg.VideoMetadata{
				Language:          m.Language,
				Title:             m.Title,
				Description:       m.Description,
				Tags:              m.Tags,
				TranslationStatus: m.TranslationStatus,
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
				// fail-closed image-binding (godlike/07): promote to ImageStatusGenerated only when SceneImageDriveLink is non-empty; wave-coherent rule with postprocessor_composite_merge.go
				driveLink := adapters.SceneImageDriveLink(s)
				if strings.TrimSpace(driveLink) != "" {
					sc.Bindings.Image.URL = driveLink
					sc.Bindings.Image.Status = string(scriptpkg.ImageStatusGenerated)
				} else {
					sc.Bindings.Image.URL = ""
					sc.Bindings.Image.Status = string(scriptpkg.ImageStatusFailed)
				}
			}
		}
	}

	if len(postResult.Voiceovers) > 0 {
		voiceoverByLanguage := make(map[string]*scriptpkg.VoiceoverLanguageArtifact)
		for _, v := range postResult.Voiceovers {
			language := strings.TrimSpace(v.Language)
			if language == "" {
				language = plan.Language
			}
			entry := voiceoverByLanguage[language]
			if entry == nil {
				entry = &scriptpkg.VoiceoverLanguageArtifact{Language: language, Status: v.Status}
				voiceoverByLanguage[language] = entry
				result.Artifacts.Voiceovers = append(result.Artifacts.Voiceovers, *entry)
			}
			if strings.TrimSpace(v.Link) != "" {
				entry.DriveLinks = append(entry.DriveLinks, v.Link)
			}
			if v.Status == "failed" {
				entry.Status = v.Status
			}
			if v.SceneIndex < len(result.Output.SpecScene.Scenes) {
				sc := &result.Output.SpecScene.Scenes[v.SceneIndex]
				if sc.Bindings.Voiceover == nil {
					sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
				}
				sc.Bindings.Voiceover.Status = v.Status
				sc.Bindings.Voiceover.Link = v.Link
				// Timing write-back on the response surface. The sanitized
				// copy deep-copies the Timing map, so this mutation never
				// aliases the pipeline's final binding map.
				if v.Timing != nil {
					if sc.Bindings.Voiceover.Timing == nil {
						sc.Bindings.Voiceover.Timing = make(map[string]scriptpkg.VoiceoverTimingBinding)
					}
					sc.Bindings.Voiceover.Timing[language] = *v.Timing
				}
			}
		}
		for i := range result.Artifacts.Voiceovers {
			language := result.Artifacts.Voiceovers[i].Language
			if entry := voiceoverByLanguage[language]; entry != nil {
				result.Artifacts.Voiceovers[i] = *entry
			}
		}
	}
	return specScene
}
