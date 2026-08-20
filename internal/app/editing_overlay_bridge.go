package app

// editing_overlay_bridge.go wires the Gate 7 lineage hop between the two
// surfaces that must agree on which overlay a final video composites:
//
//	scriptgeneration.EditingOverlaySpan  (the run's editing timeline)
//	          │  render_job_id / plan_fingerprint / render_key /
//	          │  source_video_asset_id  (identical identity, never re-derived)
//	          ▼
//	cliprender.OverlayRefSpec           (the clip.render wire contract)
//
// The editing timeline span is the certified projection of the frozen
// OverlayPlan + the completed overlay.render queue job. The clip.render
// request must carry exactly that lineage — a second derivation would let
// the final video claim an overlay it never actually composited. This
// adapter is the single projection point: identical fields, fail-closed.

import (
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// overlayRefSpecFromEditingSpan projects one editing timeline overlay span
// onto the clip.render overlay lineage contract, preserving the identity
// verbatim (render_job_id, plan_fingerprint, render_key,
// source_video_asset_id) AND the declared compositing window
// (start_us/end_us from the span). It is fail-closed: a span whose render
// has not completed (any lineage field empty) or whose window is invalid
// yields nil — the clip.render request then declares no overlay rather than
// a half-proven or untimed composition, mirroring OverlayRefSpec's
// all-or-nothing validation gate.
func overlayRefSpecFromEditingSpan(span scriptgeneration.EditingOverlaySpan) *cliprender.OverlayRefSpec {
	if strings.TrimSpace(span.RenderJobID) == "" ||
		strings.TrimSpace(span.PlanFingerprint) == "" ||
		strings.TrimSpace(span.RenderKey) == "" ||
		strings.TrimSpace(span.SourceVideoAssetID) == "" {
		return nil
	}
	if span.StartUS < 0 || span.EndUS <= span.StartUS {
		return nil
	}
	return &cliprender.OverlayRefSpec{
		RenderJobID:        span.RenderJobID,
		PlanFingerprint:    span.PlanFingerprint,
		RenderKey:          span.RenderKey,
		SourceVideoAssetID: span.SourceVideoAssetID,
		StartUS:            span.StartUS,
		EndUS:              span.EndUS,
	}
}
