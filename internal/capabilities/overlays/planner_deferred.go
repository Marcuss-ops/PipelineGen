// planner_deferred.go — candidate-plan boundary for the single-call Rust
// compiler. BuildPlanCandidates returns every valid item without Go-side
// admission; FinalizeDeferredPlan applies only the post-admission rendering
// steps after Rust has selected the final set.
package overlays

import (
	"context"
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// DeferredPlan carries the normalized planner input and the precomputed
// deterministic seeds. Its fields are populated by BuildPlanCandidates and
// consumed by FinalizeDeferredPlan after the caller has applied the worker's
// selection and timing edits.
type DeferredPlan struct {
	Plan  capmodel.OverlayPlan
	Input PlanInput

	motionSeed          string
	imageSeed           string
	entityStyleSelector string
	imageMotionOffset   int
}

// BuildPlanCandidates builds the complete, validated candidate set. Map
// trimming, overlap degradation and run-level admission are deliberately
// deferred: the single-call compiler must see every candidate before it
// chooses the final plan. The caller must not treat the returned items as
// render-ready until FinalizeDeferredPlan has run.
func BuildPlanCandidates(input PlanInput, config PlannerConfig) (DeferredPlan, error) {
	config.DeferAdmission = true
	var deferred DeferredPlan
	plan, err := buildPlan(context.Background(), input, config, nil, &deferred)
	if err != nil {
		return DeferredPlan{}, fmt.Errorf("overlay planner candidates: %w", err)
	}
	deferred.Plan = plan
	return deferred, nil
}

// FinalizeDeferredPlan applies the post-admission rendering steps to the
// caller-selected candidate set: recap/chart observability, deterministic
// motion assignment, phrase entrance clamping, date/image collision handling
// and final validation. It never re-runs admission.
func FinalizeDeferredPlan(deferred DeferredPlan) (capmodel.OverlayPlan, error) {
	imageMotionOffset := deferred.imageMotionOffset
	entityImageMotionForPlan := func(ordinal, limit int) string {
		pool := generatedEntityImageMotionCandidates
		if limit > 0 {
			pool = limitedMotionPool(pool, limit)
		}
		return rotateMotionAtOffset(imageMotionOffset, ordinal, pool)
	}
	return finalizeAdmittedPlan(&deferred.Plan, deferred.Input, deferred.motionSeed, deferred.imageSeed,
		deferred.entityStyleSelector, entityImageMotionForPlan)
}

// finalizeAdmittedPlan contains the rendering steps that must run only after
// the final admission decision. It is shared by the legacy BuildPlan path and
// the deferred single-call path so both produce the same layout contract.
func finalizeAdmittedPlan(plan *capmodel.OverlayPlan, input PlanInput, motionSeed, imageSeed, entityStyleSelector string, entityImageMotionForPlan func(ordinal, limit int) string) (capmodel.OverlayPlan, error) {
	if plan == nil {
		return capmodel.OverlayPlan{}, fmt.Errorf("overlay planner: nil plan")
	}
	// Recap/chart observability is counted after every gate that can drop a
	// card, while each surviving card remains separately counted.
	plan.RecapCards = 0
	plan.ChartCards = 0
	for i := range plan.Items {
		if v, ok := plan.Items[i].Params["recap"]; ok && v == true {
			plan.RecapCards++
		}
		if v, ok := plan.Items[i].Params["chart_card"]; ok && v == true {
			plan.ChartCards++
		}
	}
	assignPlannerItemMotions(plan, input, motionSeed, imageSeed, entityStyleSelector, entityImageMotionForPlan)
	// Renderer catalog defaults may use a 4-second entrance, longer than the
	// certified spoken window of a short phrase. Clamp every phrase family to
	// one third of its own window so the selected treatment settles in time and
	// remains readable through the rest of its MP4.
	if input.FPSNum > 0 && input.FPSDen > 0 {
		for i := range plan.Items {
			item := &plan.Items[i]
			if item.Kind != "text_phrase" {
				continue
			}
			durationUS := item.DurationUS
			if durationUS <= 0 && item.EndMs > item.StartMs {
				durationUS = (item.EndMs - item.StartMs) * 1_000
			}
			if durationUS <= 0 {
				continue
			}
			if item.MotionParams == nil {
				item.MotionParams = make(map[string]any)
			}
			item.MotionParams["enter_frames"] = int(PhraseEntranceFrames(durationUS, input.FPSNum, input.FPSDen))
			item.MotionParams["exit_frames"] = int(PhraseExitFrames(durationUS, input.FPSNum, input.FPSDen))
		}
	}
	input.MotionSeed = motionSeed
	plan.Items = PruneOrphanChartBars(plan.Items)
	// Move images aside for overlapping date cards after admission. Animation
	// consistency was already applied above; re-running it here would rotate
	// duplicate motions and exceed the per-family style cap.
	MoveImagesAsideForOverlappingDateCards(plan.Items, input.Width)
	plan.Items = PruneOrphanChartBars(plan.Items)
	if strings.TrimSpace(plan.PlanID) == "" || strings.TrimSpace(plan.VideoID) == "" {
		return capmodel.OverlayPlan{}, fmt.Errorf("overlay planner: plan_id and video_id are required")
	}
	if err := plan.Validate(); err != nil {
		return capmodel.OverlayPlan{}, err
	}
	return *plan, nil
}
