// overlay_plan_seal.go — the plan-sealing boundary of overlay compilation:
// render-extent derivation (audio-plan authoritative + scene/item spans),
// provisional-key reset, OverlayPlan assembly and Validate.
//
// Split from overlay_plan_compile.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// sealOverlayPlan derives the master render extent, resets provisional
// render keys after post-processing and assembles + validates the final
// OverlayPlan. Split from compileOverlayPlanWithMotionOffset (same behavior).
func sealOverlayPlan(result *GenerateResult, language Language, plannerPlan capabilityoverlay.OverlayPlan, resolved []ResolvedScene, items []capabilityoverlay.OverlayItem, canvas OverlayCanvasSpec, planID, videoID, projectID string) (*capabilityoverlay.OverlayPlan, error) {
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
	durationMS := int64(0)
	if durationUS > 0 {
		// DurationMS is a transport projection; round up so a sub-ms tail is
		// never lost when it crosses the PipelineGen → Chronon boundary.
		durationMS = (durationUS + 999) / 1000
	}
	// Item transforms above (chain coalescing, font/style projection and
	// image composition) can change renderer-owned inputs after an upstream
	// planner has sealed its own intermediate plan. This is the final plan
	// sealing boundary, so discard those provisional keys and let Validate
	// recompute them from the actual serialized render inputs.
	for i := range items {
		items[i].RenderKey = ""
	}

	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       videoID,
		ProjectID:     strings.TrimSpace(projectID),
		// Forward the explicit render seed so the worker resamples its own
		// "random" entity styles per render; empty keeps historical
		// plan-identity sampling and byte-identical wire plans.
		EntityVariationSeed:    entityVariationSeedForCanvas(canvas),
		ScriptName:             textutil.FirstNonEmpty(result.OutputName, result.Title, projectID),
		Language:               string(language),
		Width:                  canvas.Width,
		Height:                 canvas.Height,
		FPSNum:                 canvas.FPSNum,
		FPSDen:                 canvas.FPSDen,
		DurationMS:             durationMS,
		ForegroundScalePercent: canvas.ForegroundScalePercent,
		Background:             canvas.Background,
		AnimationPolicies:      scriptoverlay.ToOverlayAnimationPolicies(canvas.AnimationPolicies),
		// Overlays are composited over the master video, so they require an
		// alpha channel. The contract travels with the plan (never re-derived
		// downstream); the compiled chronon output derives container/codec/
		// pixel format from it.
		MediaContract: capabilityoverlay.ContractIDForCanvas(canvas.Width, canvas.Height, canvas.FPSNum, canvas.FPSDen, true),
		Items:         items,
		// Carry the planner's recap observability into the sealed plan:
		// plannerPlan itself is superseded here by post-processing (entity
		// overlays, budgets, chain fusion). RecapCards is recounted on the
		// final items so fused-away markers cannot inflate it.
		RecapPlacements: plannerPlan.RecapPlacements,
		RecapCards:      countRecapItems(items),
		ChartPlacements: plannerPlan.ChartPlacements,
		ChartCards:      countChartItems(items),
		TimedScenes:     plannerPlan.TimedScenes,
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}
