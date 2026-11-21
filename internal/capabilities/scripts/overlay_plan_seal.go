// overlay_plan_seal.go — the plan-sealing boundary of overlay compilation:
// render-extent derivation (audio-plan authoritative + scene/item spans),
// provisional-key reset, OverlayPlan assembly and Validate.
//
// Split from overlay_plan_compile.go to respect max_lines_per_file_strict
// (same package, no behavior change).
//
// Entity-card → entity-image promotion no longer happens here: the v2 planner
// owns it (CHAIN-SEGMENT v2). PipelineGen ships `media.promotable_entity_assets`
// and adopts the plan the planner sealed, so no post-seal pass rewrites a
// segment's identity, text, motion or preset.
package scriptgeneration

import (
	"fmt"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// sealCompiledOverlayPlan adopts a plan produced by the v2 compiler. Rust owns
// selection, timing, map resolution and entity-card promotion; this boundary
// owns only the derived render identity and the master extent, so the document
// that reaches the bundle hash, audit, renderer and checkpoint is the same
// sealed document.
func sealCompiledOverlayPlan(plan *capmodel.OverlayPlan, result *GenerateResult, language Language, resolved []ResolvedScene, canvas OverlayCanvasSpec) (*capmodel.OverlayPlan, error) {
	if plan == nil {
		return nil, nil
	}
	plan.SchemaVersion = capmodel.SchemaVersionPlan
	plan.Language = string(language)
	return finalizeSealedOverlayPlan(*plan, result, language, resolved, canvas)
}

// finalizeSealedOverlayPlan derives the master extent, resets provisional render
// keys and stamps the one output identity before Validate computes the final
// fingerprint. It is the only place a plan becomes wire-eligible.
func finalizeSealedOverlayPlan(plan capmodel.OverlayPlan, result *GenerateResult, language Language, resolved []ResolvedScene, canvas OverlayCanvasSpec) (*capmodel.OverlayPlan, error) {
	// Explicit payload typography is projected after the planner's presentation
	// defaults: the run-level style styles every text lane, while the dedicated
	// overlay_style.bullet block styles bullet-point copy only and falls back to
	// the run-level choice field by field. Fields the payload left unset keep the
	// planner default.
	applyPayloadOverlayStyleToItems(plan.Items, canvas)
	plan.DurationMS = overlayPlanDurationMS(result, language, resolved, plan.Items)
	// Item transforms above (font/style projection and image composition) can
	// change renderer-owned inputs after an upstream planner has sealed its own
	// intermediate plan. This is the final plan sealing boundary, so discard
	// those provisional keys and let Validate recompute them from the actual
	// serialized render inputs.
	for i := range plan.Items {
		plan.Items[i].RenderKey = ""
	}
	plan.RecapCards = countRecapItems(plan.Items)
	plan.ChartCards = countChartItems(plan.Items)
	plan.Fingerprint = ""
	// The output identity is stamped in ONE place: the certification profile the
	// renderer will be held to, and the media contract derived from it. The
	// contract travels with the plan (never re-derived downstream) and the
	// compiled chronon output derives container/codec/pixel format from it, so
	// the sealed plan carries one statement about the artifact instead of two
	// that can disagree. The stamp runs BEFORE Validate because the plan
	// fingerprint is computed from its contents.
	plan.StampOutputProfile(canvas.OutputProfileID)
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}

// overlayPlanDurationMS derives the master render extent from the authoritative
// audio plan, with the scene and item tails as lower bounds.
func overlayPlanDurationMS(result *GenerateResult, language Language, resolved []ResolvedScene, items []capmodel.OverlayItem) int64 {
	if result == nil {
		return 0
	}
	// Preserve the scene extent as a defensive lower bound when a malformed or
	// legacy result has no final-audio reference.
	var durationUS int64
	if result.SourceLanguage == "" || result.SourceLanguage == language {
		if result.AudioPlan != nil {
			// A sealed plan exists before encoding. Its master extent, not
			// encoder packet padding, freezes identical serial/parallel plans.
			durationUS = result.AudioPlan.DurationUS
			if result.AudioPlan.MasterDurationUS > durationUS {
				durationUS = result.AudioPlan.MasterDurationUS
			}
		} else if result.FinalAudio != nil && result.FinalAudio.DurationMS > 0 {
			durationUS = result.FinalAudio.DurationMS * 1000
		}
	}
	for _, scene := range resolved {
		if scene.TimelineStartUS < 0 || scene.DurationUS <= 0 {
			continue
		}
		if endUS := scene.TimelineStartUS + scene.DurationUS; endUS > durationUS {
			durationUS = endUS
		}
	}
	// Date cards intentionally outlive their spoken word timing. Their render
	// plan canvas must include the full item interval; this affects video/overlay
	// duration only and does not pad or rewrite the canonical audio plan.
	for _, item := range items {
		if endUS := item.EndUSValue(); endUS > durationUS {
			durationUS = endUS
		}
	}
	if durationUS <= 0 {
		return 0
	}
	// DurationMS is a transport projection; round up so a sub-ms tail is
	// never lost when it crosses the PipelineGen → Chronon boundary.
	return (durationUS + 999) / 1000
}
