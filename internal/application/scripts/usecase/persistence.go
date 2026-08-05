// Package usecase — persistence.go: result-building helpers.
//
// Extracted from generate_one_usecase_persist.go (July 2026).
// Owns: buildGenerationResult.
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
	return buildGenerationResultWithCache(item, plan, engineResult, postResult, timings, nil, context.Background())
}

func buildGenerationResultWithCache(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
	timings scriptpkg.GenerationTimings,
	cache scriptports.VidRushCachePort,
	ctx context.Context,
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
	for i := range specScene.Scenes {
		if specScene.Scenes[i].Annotations != nil {
			specScene.Scenes[i].Annotations = adapters.RebaseSceneAnnotations(
				specScene.Scenes[i].Annotations,
				specScene.Scenes[i].Text,
			)
		}
	}
	specScene = sanitizeSpecSceneOutputForResponse(specScene)

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
		if len(postResult.VidRushSegments) > 0 {
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
			}
		}
		if strings.TrimSpace(postResult.DocID) != "" || strings.TrimSpace(postResult.DocLink) != "" {
			result.Artifacts.Document = &scriptpkg.DocumentArtifact{
				DocID: postResult.DocID, DocLink: postResult.DocLink,
			}
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
				}
			}
			for i := range result.Artifacts.Voiceovers {
				language := result.Artifacts.Voiceovers[i].Language
				if entry := voiceoverByLanguage[language]; entry != nil {
					result.Artifacts.Voiceovers[i] = *entry
				}
			}
		}

	}

	if postResult != nil && len(postResult.Warnings) > 0 {
		result.Warnings = append(result.Warnings, postResult.Warnings...)
	}

	result.Source = sourceTrace
	return result
}

// promoteCachedVidRushStates marks derived VidRush data as an exact cache hit
// when the canonical script cache returned the complete postprocessed result.
// BYPASSED is deliberately preserved: it records an explicit provider policy,
// not a cache miss.
func promoteCachedVidRushStates(segments []scriptpkg.VidRushSegmentResult) {
	for i := range segments {
		if segments[i].Cache.Extraction != "BYPASSED" {
			segments[i].Cache.Extraction = "HIT_EXACT"
		}
		if segments[i].Cache.Artlist != "BYPASSED" {
			segments[i].Cache.Artlist = "HIT_EXACT"
		}
		if segments[i].Cache.InternetImages != "BYPASSED" {
			segments[i].Cache.InternetImages = "HIT_EXACT"
		}
		if segments[i].Cache.Binding != "BYPASSED" {
			segments[i].Cache.Binding = "HIT_EXACT"
		}
	}
}

func scriptCacheStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "exact_hit":
		return "HIT_EXACT"
	case "generated", "":
		return "MISS"
	default:
		return strings.ToUpper(strings.TrimSpace(status))
	}
}

// ── Entity-result serializer (PR-LEGACY-CLEANUP-2026-07-10 Item 2) ──

// SerializeEntityResultRoundTrip preserves the typed entity result
// as JSON for legacy read-only compatibility. It never mutates the
// source of truth.
//
// godlike/06 SSOT: the helper used to live at
// internal/application/scripts/dto/compat_types.go as the canonical
// (export-named) SerializeEntityResultRoundTrip. After PR-LEGACY-
// CLEANUP-2026-07-10 Item 2 retired the entire dto/compat_types.go
// file + the PostProcessArtifact ` = any` alias, the helper moved
// here to preserve the export-named wire-shape contract.
//
// The single canonical caller in production code is
// buildGenerationResult in this file (call site at line ~92).
// The export name is preserved so that any future migration can
// reach the helper without changing the import path again; per the
// pre-Item-2 forensic rg, the prior dto-path had zero external
// callers, so the export is forward-looking contract stability,
// not backward-compat. If a future agent introduces a non-canonical
// caller, this goddoc must be updated to list the new caller.
func SerializeEntityResultRoundTrip(res *scriptpkg.EntityResult) (string, error) {
	if res == nil {
		return "", nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("serialize entity result: %w", err)
	}
	return string(raw), nil
}

// sanitizeSpecSceneOutputForResponse returns a deep copy of the
// canonical SpecSceneOutput with production-local voiceover paths
// stripped from the API-facing surface. The internal pipeline keeps
// LocalPath for downstream persistence and diagnostics; only the
// emitted result hides it.
func sanitizeSpecSceneOutputForResponse(in scriptpkg.SpecSceneOutput) scriptpkg.SpecSceneOutput {
	if len(in.Scenes) == 0 {
		return in
	}

	out := in
	out.Scenes = append([]scriptpkg.SpecScene(nil), in.Scenes...)
	for i := range out.Scenes {
		img := out.Scenes[i].Bindings.Image
		if img != nil {
			imgCopy := *img
			imgCopy.LocalPath = ""
			out.Scenes[i].Bindings.Image = &imgCopy
		}
		vo := out.Scenes[i].Bindings.Voiceover
		if vo == nil {
			continue
		}
		voCopy := *vo
		voCopy.LocalPath = ""
		out.Scenes[i].Bindings.Voiceover = &voCopy
	}
	return out
}
