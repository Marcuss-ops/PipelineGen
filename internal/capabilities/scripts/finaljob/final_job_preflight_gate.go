package finaljob

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// FinalJobPreflightCoversPlan reports whether the durable marker certifies the
// CURRENT sealed plan. An empty digest is never a match.
func FinalJobPreflightCoversPlan(result *contract.GenerateResult) bool {
	if result == nil || result.OverlayPlan == nil || result.FinalJobPreflight == nil || !result.FinalJobPreflight.Passed {
		return false
	}
	digest := OverlayPlanRenderDigest(result.OverlayPlan)
	return digest != "" && result.FinalJobPreflight.PlanDigest == digest
}

// FinalJobImagePreflightOnce verifies the image contract at most once per
// sealed plan. The first caller pays the network verification; every later
// caller (the final-job payload build and the overlay handoff) trusts the
// marker only while the plan digest is unchanged.
//
// Why this exists: the same verification used to run after the overlay render,
// so a job whose promised images were invalid paid for the full GPU render
// before learning it would never ship them. Moving the gate to the render
// boundary turns that class from "wasted render" into "fast failure", and the
// digest-bound marker keeps the late fail-closed check without paying the
// downloads twice on the happy path.
func FinalJobImagePreflightOnce(ctx context.Context, imageRequested bool, result *contract.GenerateResult) error {
	if result == nil || result.OverlayPlan == nil || !imageRequested {
		return nil
	}
	if FinalJobPreflightCoversPlan(result) {
		return nil
	}
	clipScenes := make(map[string]struct{})
	for SceneID := range FinalJobIntermediateClipWindows(result) {
		clipScenes[SceneID] = struct{}{}
	}
	if err := finalJobImagePreflightContext(ctx, result.OverlayPlan.Items, clipScenes, true); err != nil {
		// A failed verification leaves no marker: a retry must re-check the
		// bytes instead of inheriting an unproven pass.
		return err
	}
	result.FinalJobPreflight = &contract.FinalJobPreflight{
		PlanDigest: OverlayPlanRenderDigest(result.OverlayPlan),
		Passed:     true,
	}
	return nil
}

// FinalJobPreRenderPreflight is the gate at the overlay render boundary: the
// cheap entity-image coverage check first (no network), then the content
// verification. It runs only for a final job that asked for images, and only
// when at least one render is about to be submitted.
func FinalJobPreRenderPreflight(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult) error {
	if result == nil || result.OverlayPlan == nil || !FinalJobImageRequested(req) {
		return nil
	}
	if err := FinalJobEntityImageGate(result.OverlayAudit, true); err != nil {
		return err
	}
	return FinalJobImagePreflightOnce(ctx, true, result)
}
