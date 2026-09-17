package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// Bounded outcome vocabulary of the overlay render boundary. These are the
// ONLY values the overlay_render_total label ever carries, so the series count
// stays fixed regardless of how the render failed.
const (
	renderOutcomeSuccess = "success"
	renderOutcomeFailure = "failure"
	// unknownChrononBackend labels an artifact whose worker reported no backend,
	// so a backend label is never the empty string.
	unknownChrononBackend = "unknown"
)

// runner_phase_overlay_render.go owns the BLOCKING overlay render boundary.
//
// Why it is its own phase rather than a block inside the audio compile phase:
//
// The render submits the Chronon plan and then WAITS for the remote GPU — the
// single longest wait in a script run. It used to be sequenced inside
// runAudioCompilePhase, so the audio stage's wall time included a render it does
// not own, the render never appeared on the critical path, and the breakdown
// reported `audio_compile` as the bottleneck with a dominant operation borrowed
// from another subsystem. The kernel attributes a nested stage to its enclosing
// stage, so attributing the render correctly requires it to be a SIBLING of the
// audio stage — which is what this file provides.
//
// The render itself is unchanged: same gates, same enqueuer, same fail-closed
// outcome, same execution step on failure. Only the measurement boundary moved.

// runOverlayRenderPhase renders the frozen semantic OverlayPlan and records the
// certified reference on the run.
//
// It returns true without rendering when the request did not ask for a render,
// no render enqueuer is wired, the overlay plan was never projected, the result
// already carries a render, or a prior attempt was already resumed past the
// audio stage (the render used to be part of that stage's work, so a resumed run
// must not re-wait on it).
func (r *Runner) runOverlayRenderPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, state audioCompileState, result *GenerateResult) bool {
	if result == nil || result.OverlayPlan == nil {
		return true
	}
	if !req.Render.Enabled || r.overlayRenderEnqueuer == nil {
		return true
	}
	if result.OverlayRender != nil {
		return true
	}
	if stageSkipped(resumeIdx, StageCompilingAudio) {
		return true
	}

	// The plan size and the boundary wall time are measured HERE, at the only
	// place that performs the blocking hand-off, so no consumer has to re-time
	// a wait it does not own.
	observability.OverlayRenderItems.Observe(float64(len(result.OverlayPlan.Items)))
	startedAt := time.Now()
	ref, renderErr := r.overlayRenderEnqueuer.EnqueueChrononPlan(ctx, *result.OverlayPlan)
	boundarySeconds := time.Since(startedAt).Seconds()
	observability.OverlayRenderDurationSeconds.Observe(boundarySeconds)
	if renderErr != nil {
		observability.OverlayRenderTotal.WithLabelValues(renderOutcomeFailure).Inc()
		cause := fmt.Errorf("overlay render failed: %w", renderErr)
		// The AUDIO_COMPILE step stays open across the render precisely so a
		// render failure is still reported against the work that produced the
		// plan (its pre-split behaviour).
		r.failExecutionStep(ctx, exec, state.Step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	observability.OverlayRenderTotal.WithLabelValues(renderOutcomeSuccess).Inc()
	recordChrononArtifactMetrics(ref)
	result.OverlayRender = &ref
	r.log.Info("overlay render complete",
		zap.String("run_id", runID),
		zap.String("render_job_id", ref.JobID),
		zap.String("status", ref.Status),
		zap.Duration("boundary_wall", time.Since(startedAt)),
		zap.Int("plan_items", len(result.OverlayPlan.Items)),
	)
	return true
}

// recordChrononArtifactMetrics projects the CERTIFIED artifact's owner-measured
// Chronon telemetry into Prometheus: the render phase (chronon_render_seconds),
// the encode phase (chronon_encode_seconds) and the frames actually produced
// (chronon_frames_rendered_total). These are the GPU lane's own numbers — this
// function never re-times a phase the worker already measured.
//
// Which artifact: when production renders each semantic item as its own short
// video, Items carries one artifact per item while Artifact duplicates the
// FIRST item for the legacy document/publication consumers — so observing both
// would double-count it. Items wins; Artifact is the single-render fallback.
//
// A phase the worker did not report is NOT observed. A zero would silently pull
// the histogram down and hide a missing measurement, which is exactly the
// regression this telemetry exists to expose.
func recordChrononArtifactMetrics(ref RenderReference) {
	for _, artifact := range renderedArtifacts(ref) {
		backend := strings.TrimSpace(artifact.Backend)
		if backend == "" {
			backend = unknownChrononBackend
		}
		if artifact.RenderMS > 0 {
			observability.ChrononRenderSeconds.WithLabelValues(backend).Observe(float64(artifact.RenderMS) / 1000)
		}
		if artifact.EncodeMS > 0 {
			observability.ChrononEncodeSeconds.WithLabelValues(backend).Observe(float64(artifact.EncodeMS) / 1000)
		}
		if artifact.FrameCount > 0 {
			observability.ChrononFramesRenderedTotal.WithLabelValues(backend).Add(float64(artifact.FrameCount))
		}
	}
}

// renderedArtifacts returns the artifacts to certify for ONE render reference,
// preferring the per-item lineage over the duplicated first-item Artifact.
func renderedArtifacts(ref RenderReference) []*RenderArtifact {
	if len(ref.Items) > 0 {
		out := make([]*RenderArtifact, 0, len(ref.Items))
		for i := range ref.Items {
			if ref.Items[i].Artifact != nil {
				out = append(out, ref.Items[i].Artifact)
			}
		}
		return out
	}
	if ref.Artifact != nil {
		return []*RenderArtifact{ref.Artifact}
	}
	return nil
}
