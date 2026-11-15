package scriptgeneration

import "context"

// FinalJobPreflight is the durable proof that the image contract of ONE
// sealed overlay plan was verified before any overlay render was submitted.
//
// It is keyed by the plan's render digest, not by the run: the digest covers
// the canvas, timing, item order, content and asset addresses, so a repaired
// or re-frozen plan changes the digest and re-arms the gate. A marker without
// a digest never covers a plan — an unidentifiable plan cannot be skipped.
type FinalJobPreflight struct {
	PlanDigest string `json:"plan_digest,omitempty"`
	Passed     bool   `json:"passed"`
}

// finalJobPreflightCoversPlan reports whether the durable marker certifies the
// CURRENT sealed plan. An empty digest is never a match.
func finalJobPreflightCoversPlan(result *GenerateResult) bool {
	if result == nil || result.OverlayPlan == nil || result.FinalJobPreflight == nil || !result.FinalJobPreflight.Passed {
		return false
	}
	digest := OverlayPlanRenderDigest(result.OverlayPlan)
	return digest != "" && result.FinalJobPreflight.PlanDigest == digest
}

// finalJobImagePreflightOnce verifies the image contract at most once per
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
func finalJobImagePreflightOnce(ctx context.Context, imageRequested bool, result *GenerateResult) error {
	if result == nil || result.OverlayPlan == nil || !imageRequested {
		return nil
	}
	if finalJobPreflightCoversPlan(result) {
		return nil
	}
	clipScenes := make(map[string]struct{})
	for sceneID := range finalJobIntermediateClipWindows(result) {
		clipScenes[sceneID] = struct{}{}
	}
	if err := finalJobImagePreflightContext(ctx, result.OverlayPlan.Items, clipScenes, true); err != nil {
		// A failed verification leaves no marker: a retry must re-check the
		// bytes instead of inheriting an unproven pass.
		return err
	}
	result.FinalJobPreflight = &FinalJobPreflight{
		PlanDigest: OverlayPlanRenderDigest(result.OverlayPlan),
		Passed:     true,
	}
	return nil
}

// finalJobPreRenderPreflight is the gate at the overlay render boundary: the
// cheap entity-image coverage check first (no network), then the content
// verification. It runs only for a final job that asked for images, and only
// when at least one render is about to be submitted.
func finalJobPreRenderPreflight(ctx context.Context, req GenerateRequest, result *GenerateResult) error {
	if result == nil || result.OverlayPlan == nil || !finalJobImageRequested(req) {
		return nil
	}
	if err := finalJobEntityImageGate(result.OverlayAudit, true); err != nil {
		return err
	}
	return finalJobImagePreflightOnce(ctx, true, result)
}
