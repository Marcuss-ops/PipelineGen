// Package usecase — clip_native_enforce.go enforces the strict
// clip-native contract for source.type=clips generations.
//
// Rules:
//   - In strict mode (fallback_policy is empty or "strict") the
//     pipeline fails if the model does not produce exactly one scene
//     per accepted clip and bind every accepted clip to a scene.
//   - In explicit fallback mode (fallback_policy="allow_prose") the
//     pipeline succeeds with warnings and reports the fallback in
//     GenerationResult.ModeInfo.
package usecase

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// enforceClipNativeContract mutates the generation result for
// clip-native sources. It returns a typed error when the strict
// contract is violated. For allow_prose it sets
// result.Status="SUCCEEDED_WITH_WARNINGS" and populates ModeInfo.
func enforceClipNativeContract(
	result *scriptpkg.GenerationResult,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
) error {
	if result == nil || plan.SourceKind != string(scriptpkg.SourceClips) {
		return nil
	}

	policy := plan.FallbackPolicy
	if policy == "" {
		policy = scriptpkg.FallbackPolicyStrict
	}

	requestedMode := "clip_native"
	usedMode := "clip_native"
	fallbackUsed := false
	var warnings []string

	// Determine the final scene list after postprocessing.
	finalScenes := engineResult.Output.SpecScene.Scenes
	if postResult != nil && len(postResult.FinalSpecScene.Scenes) > 0 {
		finalScenes = postResult.FinalSpecScene.Scenes
	}

	clipIDs := effectiveClipIDs(plan)

	// P0 (July 2026): for source.type == clips, scenes must be built
	// directly from clip evidence. If no scenes were produced, the
	// clip-native plan is unavailable.
	if len(finalScenes) == 0 && plan.SourceKind == string(scriptpkg.SourceClips) {
		return &scriptpkg.ClipNativePlanningError{
			Code:    "CLIP_NATIVE_PLAN_UNAVAILABLE",
			ItemID:  item.ID,
			Policy:  policy,
			Reason:  "no scenes could be built from clip evidence",
			Details: []string{"clip_native: no scenes produced and no valid clip evidence available"},
		}
	}

	// 1 clip = 1 scene.
	if len(finalScenes) != len(clipIDs) {
		msg := fmt.Sprintf("clip_native: scene count (%d) does not match clip count (%d)",
			len(finalScenes), len(clipIDs))
		if policy == scriptpkg.FallbackPolicyAllowProse {
			warnings = append(warnings, "[CLIP_NATIVE_PLAN_UNAVAILABLE] "+msg)
		} else {
			return &scriptpkg.ClipNativePlanningError{
				Code:    "CLIP_NATIVE_PLANNING_FAILED",
				ItemID:  item.ID,
				Policy:  policy,
				Reason:  "scene-clip count mismatch",
				Details: []string{msg},
			}
		}
	}

	// Every accepted clip must be bound to a scene.
	bound := make(map[string]struct{}, len(finalScenes))
	for _, s := range finalScenes {
		if s.Bindings.Clip != nil && s.Bindings.Clip.ClipID != "" {
			bound[s.Bindings.Clip.ClipID] = struct{}{}
		}
	}
	var missing []string
	for _, id := range clipIDs {
		if _, ok := bound[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		msg := fmt.Sprintf("clip_native: accepted clips not bound to scenes: %s",
			strings.Join(missing, ", "))
		if policy == scriptpkg.FallbackPolicyAllowProse {
			warnings = append(warnings, "[CLIP_NATIVE_PLAN_UNAVAILABLE] "+msg)
		} else {
			return &scriptpkg.ClipNativePlanningError{
				Code:    "CLIP_NATIVE_PLANNING_FAILED",
				ItemID:  item.ID,
				Policy:  policy,
				Reason:  "accepted clips not bound",
				Details: []string{msg},
			}
		}
	}

	// P0.G KNOWN GAP fix (July 2026, surfaced by
	// TestFallbackPolicy_P0G_AllowProse_SucceedsWithWarnings):
	// when fallback_policy="allow_prose" AND at least one
	// branchable check (scene-count mismatch, unbound clips)
	// fired a warning, the canonical clip-native mode is being
	// degraded to prose fallback. Surface this in ModeInfo so
	// diagnostics dashboards can distinguish a fully-conformant
	// clip-native run from a degraded allow_prose run.
	//
	// godlike/06 SSOT: this is the SOLE mutation site for the
	// (usedMode, fallbackUsed) pair under the allow_prose policy.
	// A future refactor that adds another branchable check MUST
	// either fire a warning AND let this single mutation gate the
	// mode flip, OR extend this gate with the new check's name.
	// The pre-fix code declared usedMode="clip_native" and
	// fallbackUsed=false at function entry and never mutated
	// them — the test verified expected values "prose"/true
	// red, surfacing this gap.
	if policy == scriptpkg.FallbackPolicyAllowProse && len(warnings) > 0 {
		usedMode = "prose"
		fallbackUsed = true
	}

	// Strict mode must never use prose fallback. The guard fires
	// only when a previous mutation set fallbackUsed=true under a
	// non-allow_prose policy; today only this branchable block
	// can set that flag (the document processor + other paths
	// that consume ModeInfo do not write it). Pre-Sprint-1.3 this
	// guard appeared twice in the same file (literal duplicate);
	// the duplicate has been removed.
	if fallbackUsed && policy != scriptpkg.FallbackPolicyAllowProse {
		return &scriptpkg.ClipNativePlanningError{
			Code:    "CLIP_NATIVE_PLANNING_FAILED",
			ItemID:  item.ID,
			Policy:  policy,
			Reason:  "prose fallback not allowed",
			Details: []string{"clip_native: prose fallback was used but fallback_policy is strict"},
		}
	}

	result.ModeInfo = &scriptpkg.GenerationModeInfo{
		RequestedMode: requestedMode,
		UsedMode:      usedMode,
		FallbackUsed:  fallbackUsed,
	}
	// Sprint 1.3 (godlike/08): this function NO LONGER writes
	// result.Status. The orchestrator's classify phase
	// (ClassifyGenerationStatus in status_classifier.go) is the
	// SOLE writer of result.Status. Warnings carry the same
	// information — when the central classify runs with
	// qualitySkipped=false and len(Warnings)>0 it produces
	// ItemStatusSucceededWithWarnings, matching the pre-Sprint-1.3
	// behaviour this block used to encode.
	if len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings...)
	}
	return nil
}

// effectiveClipIDs returns the accepted clip IDs capped by NumClips,
// matching the binder's logic so the enforcement check does not drift.
func effectiveClipIDs(plan scriptpkg.ResolvedGenerationPlan) []string {
	if plan.ClipEvidence == nil {
		return nil
	}
	clipIDs := plan.ClipEvidence.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}
	return clipIDs
}

// provisionalModeInfo returns the best-effort mode info available
// before postprocessing. It is used to pre-fill the provenance block
// so result.Provenance always carries requested/used mode even when
// the document processor does not run. The document processor may
// overwrite these fields with the final post-walk values.
func provisionalModeInfo(plan scriptpkg.ResolvedGenerationPlan, engineResult *EngineResult) *scriptpkg.GenerationModeInfo {
	if plan.SourceKind != string(scriptpkg.SourceClips) {
		return nil
	}
	usedMode := "clip_native"
	fallbackUsed := false
	if len(engineResult.Output.SpecScene.Scenes) == 0 {
		usedMode = "prose"
		fallbackUsed = true
	}
	return &scriptpkg.GenerationModeInfo{
		RequestedMode: "clip_native",
		UsedMode:      usedMode,
		FallbackUsed:  fallbackUsed,
	}
}
