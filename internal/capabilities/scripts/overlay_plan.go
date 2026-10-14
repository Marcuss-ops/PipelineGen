// Package scriptgeneration — overlay_plan.go owns the derivation of the
// semantic OverlayPlan from a COMPLETED REAL result. It is the production
// counterpart of the fixture-driven planner/resolver tests: it feeds the
// certified timing surfaces (phrase timings, entity timeline, word timing)
// of an actual run into the overlay planner and resolver, so every candidate
// carries real timestamps — never estimates. The final production budget
// keeps the certified run-level ceiling of grounded phrases (overridable per
// request through max_phrase_overlays) and twenty materialized images per run.
//
// Ownership split (single owner per surface):
//
//	Phrase and image candidates → overlays.BuildPlan, from grounded scene
//	annotations and certified word timing.
//	Entity-bound image candidates → entities.ResolveEntityOverlayPlan, from
//	the certified EntityTimeline.
//	Other candidate types are discarded by the final editorial image+phrase budget.
//
// Every template terminates in one of the four canonical primitives
// (Text / Image / Video / Shape). The returned plan is the SEMANTIC
// renderinggen.overlay-plan.v1 document; RenderingGen lowers it to
// chronon.render-plan.v2 — PipelineGen never emits a concrete Chronon plan.
package scriptgeneration

import (
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// CompileOverlayPlan derives the full semantic OverlayPlan for a completed
// real result. It returns nil (no-op) when the result carries no derivable
// overlay surface (no annotations, no word timing, no entity timeline) and
// an error when a scene that DID carry timing/annotations cannot be
// projected — fail-closed like the phrase and entity timeline projections.
//
// Timestamps come exclusively from certified surfaces:
//
//   - Phrase and keyword candidates are located against the scene's real word
//     timing (LocatePhraseTimings). A phrase the voiceover did not speak is
//     skipped — never timestamped. The run-level editorial contract admits
//     phrase overlays and images only.
//   - Image candidates use their certified occurrence windows and require a
//     materialized asset before they can enter the final plan.
//
// The returned plan is sealed (render keys + fingerprint) and ready to
// enqueue through QueueRenderEnqueuer.EnqueueChrononPlan.
func CompileOverlayPlan(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, nil, nil, perSceneImages...)
}

// CompileOverlayPlanWithPlates is CompileOverlayPlan with the run's certified
// basemap plate resolver wired: grounded place annotations covered by a plate
// become map overlay items. A nil resolver behaves exactly like
// CompileOverlayPlan (no maps — the fail-closed default deployment).
func CompileOverlayPlanWithPlates(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, resolver capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, nil, resolver, perSceneImages...)
}
