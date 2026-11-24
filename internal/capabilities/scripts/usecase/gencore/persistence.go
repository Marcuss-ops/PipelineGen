// Package usecase — persistence.go: result-building helpers.
//
// Extracted from generate_one_usecase_persist.go (July 2026).
// Owns: buildGenerationResult.
package gencore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
) *scriptpkg.GenerationResult {
	return buildGenerationResultWithCache(item, plan, engineResult, postResult, nil, context.Background())
}

type generationCacheDecision struct {
	Status string
	Hit    bool
}

func decideGenerationCache(status string) generationCacheDecision {
	return generationCacheDecision{Status: status, Hit: status == "exact_hit"}
}

func lookupGenerationCache(decision generationCacheDecision) bool {
	return decision.Hit
}

func buildGenerationResultWithCache(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
	cache scriptports.VidRushCachePort,
	ctx context.Context,
) *scriptpkg.GenerationResult {
	cacheDecision := decideGenerationCache(engineResult.CacheStatus)
	cacheHit := lookupGenerationCache(cacheDecision)
	specScene := resolveResponseSpecScene(item, plan, engineResult, postResult)
	result := buildGenerationResultCore(item, plan, engineResult, postResult, specScene, cacheHit)
	if postResult != nil {
		specScene = mergePostprocessorArtifacts(ctx, result, plan, postResult, cache, cacheHit)
		result.Output.SpecScene = specScene
		if len(postResult.Warnings) > 0 {
			result.Warnings = append(result.Warnings, postResult.Warnings...)
		}
	}
	// Keep the canonical trace assembled before postprocessor artifact merges.
	return projectGenerationResult(result, postResult, plan, cacheHit)
}

func containsGenerationWarning(warnings []string, candidate string) bool {
	for _, warning := range warnings {
		if warning == candidate {
			return true
		}
	}
	return false
}

// projectGenerationResult performs the final cache/result projection after
// all canonical fields and postprocessor artifacts have been assembled.
func projectGenerationResult(result *scriptpkg.GenerationResult, _ *adapters.PipelineResult, _ scriptpkg.ResolvedGenerationPlan, _ bool) *scriptpkg.GenerationResult {
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
	case "source_text_verbatim":
		return "SOURCE_TEXT_VERBATIM"
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
// internal/capabilities/scripts/dto/compat_types.go as the canonical
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
		// Deep-copy the per-language timing map so later response-surface
		// writes (buildGenerationResult voiceover write-back) never mutate
		// the pipeline's final binding map through a shared reference.
		if vo.Timing != nil {
			voCopy.Timing = make(map[string]scriptpkg.VoiceoverTimingBinding, len(vo.Timing))
			for language, timing := range vo.Timing {
				voCopy.Timing[language] = timing
			}
		}
		out.Scenes[i].Bindings.Voiceover = &voCopy
	}
	return out
}
