// internal/application/scripts/scene/scene_planner_kinds.go —
// canonical intro/clip/outro position policy (assignKindsByPosition).
// Extracted from scene_planner.go; no behavior change.
package scene

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// assignKindsByPosition overwrites scene.Kind for >=3-scene
// bundles following the canonical Intro / Clip / Outro layout.
// Wave 1.1 promotes this from the binder-internal `for-loop` that
// ran AFTER the binding step; Wave 1.3 will move the assignment
// BEFORE the binding step so the binder can never overwrite the
// planner's kind decision (godlike/06 SSOT).
//
// godlike/06 SSOT: this method is the canonical intro/clip/outro
// policy owner. SceneSynthesizer.kindForPosition is the cheat
// sheet for the synthesizer path; the planner wins for the
// binder-driven path because the planner knows the full scene
// count + plan evidence at decision time.
//
// godlike/07 NO-FAKE-AVAILABILITY: intros and outros are written
// only when len(scenes) >= 3 AND plan.ClipEvidence.AcceptedClipIDs
// has at least 3 accepted clips. Short bundles stay as
// SceneClip because the "every requested clip is a narrative
// beat" intent wins over the "frame with intro/outro" heuristic.
func (p *ScenePlanner) assignKindsByPosition(
	scenes []scriptpkg.SpecScene,
	plan *scriptpkg.ResolvedGenerationPlan,
) {
	if plan == nil || plan.ClipEvidence == nil {
		return
	}
	clipCount := len(plan.ClipEvidence.AcceptedClipIDs)
	if clipCount < 3 || len(scenes) < clipCount {
		return
	}
	scenes[0].Kind = scriptpkg.SceneIntro
	scenes[clipCount-1].Kind = scriptpkg.SceneOutro
	for i := 1; i < clipCount-1; i++ {
		scenes[i].Kind = scriptpkg.SceneClip
	}
}
