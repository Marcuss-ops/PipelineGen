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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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

	// Detect prose fallback: the binder synthesised scenes because
	// the model emitted none.
	if postResult != nil && len(postResult.SynthesizedScenes) > 0 && len(engineResult.Output.SpecScene.Scenes) == 0 {
		fallbackUsed = true
		usedMode = "prose"
		warnings = append(warnings,
			"clip_native: prose fallback was used because the model did not emit scenes")
	}

	// Determine the final scene list after postprocessing.
	finalScenes := engineResult.Output.SpecScene.Scenes
	if postResult != nil && len(postResult.FinalSpecScene.Scenes) > 0 {
		finalScenes = postResult.FinalSpecScene.Scenes
	}

	clipIDs := effectiveClipIDs(plan)

	// 1 clip = 1 scene.
	if len(finalScenes) != len(clipIDs) {
		msg := fmt.Sprintf("clip_native: scene count (%d) does not match clip count (%d)",
			len(finalScenes), len(clipIDs))
		if policy == scriptpkg.FallbackPolicyAllowProse {
			warnings = append(warnings, msg)
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
			warnings = append(warnings, msg)
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

	// Strict mode must never use prose fallback.
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
	if fallbackUsed || len(warnings) > 0 {
		result.Status = "SUCCEEDED_WITH_WARNINGS"
		result.Warnings = append(result.Warnings, warnings...)
	} else {
		result.Status = "SUCCEEDED"
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
