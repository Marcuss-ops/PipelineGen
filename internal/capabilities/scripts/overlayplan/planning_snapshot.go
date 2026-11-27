package overlayplan

import (
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// The clone helpers below isolate the documents the plan/freeze pass writes. They copy the document value (and the plan's item slice) so field
// assignment cannot reach the durable result; the remaining nested values are
// read-only for the duration of the branch.
func ClonePlanningBundle(in *capabilityoverlay.SemanticRenderBundleV1) *capabilityoverlay.SemanticRenderBundleV1 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func ClonePlanningPlan(in *capmodel.OverlayPlan) *capmodel.OverlayPlan {
	if in == nil {
		return nil
	}
	out := *in
	out.Items = append([]capmodel.OverlayItem(nil), in.Items...)
	return &out
}

func ClonePlanningIntake(in *capmodel.OverlayPlanIntake) *capmodel.OverlayPlanIntake {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func ClonePlanningBudget(in *capabilityoverlay.PhraseOverlayBudget) *capabilityoverlay.PhraseOverlayBudget {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
