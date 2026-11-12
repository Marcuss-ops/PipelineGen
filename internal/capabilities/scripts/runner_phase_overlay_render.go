package scriptgeneration

import (
	"context"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// Bounded outcome vocabulary of the overlay render boundary. These are the
// ONLY values the overlay_render_total label ever carries, so the series count
// stays fixed regardless of how the render failed.
const (
	renderOutcomeSuccess = "success"
	renderOutcomeFailure = "failure"
	// renderOutcomeCacheHit marks an attempt served from the content-addressed
	// overlay render cache: no GPU service happened, so it is neither a normal
	// success nor a failure and must be separable in analytics.
	renderOutcomeCacheHit = "cache_hit"
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
// The render boundary is unchanged: same gates, same enqueuer, same fail-closed
// outcome, same execution step on failure. Two things moved: the measurement
// boundary (out of audio_compile), and the per-language submission loop, which
// is now a bounded fan-out instead of one sequential Chronon round-trip per
// language (see runOverlayRenderPhase).

// overlayRenderOutcome is the result of ONE per-language render submission. It
// carries the language (plus the boundary wall and plan size it was measured
// with) so the caller can apply the certified reference and log it in
// deterministic PLAN ORDER after the fan-out joins. A worker goroutine never
// touches the durable result: only the caller writes result.OverlayRender and
// result.LocalizedOverlayRenders, so the run record can never be raced or
// reordered by render completion order.
type overlayRenderOutcome struct {
	language     Language
	ref          RenderReference
	boundaryWall time.Duration
	planItems    int
	// planDigest is the identity of the plan this render was produced from. It
	// is written by the caller that owns the plan, never by the worker.
	planDigest string
}

// runOverlayRenderPhase renders the frozen semantic OverlayPlan and records the
// certified reference on the run.
//
// The per-language submissions run through a BOUNDED fan-out
// (SetOverlayRenderConcurrency, default 2) instead of the former serial
// `for plan { enqueue; wait }` loop: N languages used to cost N sequential
// Chronon round-trips, so the stage wall time grew as T1 + T2 + ... + TN. The
// fan-out is a pipelining bound, not a GPU bound — RenderingGen's worker still
// owns `worker.gpu_lanes` and remains the only authority on concurrent GPU work.
//
// Determinism over concurrency: the awarded references are keyed by language,
// the worker only computes the reference, and the caller applies every outcome
// in plan order, so the durable result is byte-identical to the serial ordering
// regardless of which render finished first. A failure is fail-closed exactly as
// before (first error cancels the rest, the run fails against AUDIO_COMPILE, the
// failed stage keeps its pre-split attribution).
//
// It returns true without rendering when the request did not ask for a render,
// no render enqueuer is wired, the overlay plan was never projected, the result
// already carries a render, or a prior attempt was already resumed past the
// audio stage (the render used to be part of that stage's work, so a resumed run
// must not re-wait on it).
func (r *Runner) runOverlayRenderPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, state audioCompileState, result *GenerateResult) bool {
	outcomes, fanOutErr := r.renderOverlayPlans(ctx, req, resumeIdx, result)
	if fanOutErr != nil {
		r.failExecutionStepBestEffort(ctx, exec, state.Step, fanOutErr)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, fanOutErr)
		return false
	}
	r.applyOverlayRenderOutcomes(runID, result, outcomes)
	return true
}

// renderOverlayPlanAtBoundary is the ONLY place that chooses the submission
// shape for one language plan (TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29).
//
// The per-scene split (one composition per scene window, N independent GPU
// round-trips instead of one whole-timeline pass) is opt-in via
// OVERLAY_RENDER_PER_SCENE and now applies to final jobs too: the worker's
// final manifest carries artifacts[] and the V2 plan binds one asset per
// distinct byte-set, so several certified overlay artifacts are a supported
// shape (the wire becomes pipelinegen.overlay-manifest-set.v1, one manifest per
// batch). Single-scene plans never split (degenerate case of the same shape),
// and enqueuers without the Split capability keep the single path — including
// every test mock, so flag OFF and capability-missing both reproduce the
// single-timeline behaviour byte for byte.
//
// Fail-closed fallback: a SPLIT ERROR falls back to the single-timeline
// submission, so a flag-ON run can only ever end up with more GPU work, never
// a different failure surface than the certified path. A split that RENDERS but
// whose batch set cannot be certified is a different class: it is a defective
// artifact, not a shape the plan cannot express, and re-rendering the whole
// timeline to paper over it is exactly the double render r63 and r69 paid for.
// The plan-level preflight (finalJobSplitPreflight) admits only shapes whose
// batches can certify, and a post-render certificate failure then fails closed
// with the batch named instead of buying a second GPU pass.
func (r *Runner) renderOverlayPlanAtBoundary(ctx context.Context, req GenerateRequest, plan *capmodel.OverlayPlan) (RenderReference, error) {
	if plan == nil {
		return RenderReference{}, nil
	}
	if req.Render.OverlaysOnly {
		if renderer, ok := r.overlayRenderEnqueuer.(OverlayPlanItemEnqueuer); ok {
			return renderer.EnqueueChrononPlanItems(ctx, *plan)
		}
		return RenderReference{}, fmt.Errorf("overlay-only render requires per-item alpha renderer")
	}
	if overlayPerSceneSplitEnabled() && planSpansMultipleScenes(*plan) {
		if splitter, ok := r.overlayRenderEnqueuer.(OverlayPlanSplitEnqueuer); ok {
			if preflightErr := finalJobSplitPreflight(req.FinalJob, *plan); preflightErr != nil {
				// No split submission at all: the batch set could never be
				// certified, so rendering it would only be thrown away.
				r.log.Warn("overlay split skipped before any render: the batch set cannot be certified",
					zap.String("plan_id", plan.PlanID),
					zap.Error(preflightErr))
			} else {
				ref, splitErr := splitter.EnqueueChrononPlanSplit(ctx, *plan)
				if splitErr != nil {
					r.log.Warn("overlay per-scene split failed; falling back to the single-timeline submission",
						zap.String("plan_id", plan.PlanID),
						zap.Error(splitErr))
				} else if certErr := certifiableFinalJobOverlayBatches(req.FinalJob, ref, plan.FPSNum, plan.FPSDen); certErr != nil {
					// The plan passed the preflight, so the artifacts are wrong.
					return RenderReference{}, fmt.Errorf("overlay split rendered a batch set the final-job handoff cannot consume (refusing a second render): %w", certErr)
				} else {
					return ref, nil
				}
			}
		}
	}
	return r.overlayRenderEnqueuer.EnqueueChrononPlan(ctx, *plan)
}

// renderOverlayPlans computes against a private snapshot. It never writes the
// durable run or execution step; the joined caller owns failures and results.
func (r *Runner) renderOverlayPlans(ctx context.Context, req GenerateRequest, resumeIdx int, result *GenerateResult) ([]overlayRenderOutcome, error) {
	if result == nil || (!req.Render.Enabled && !req.Render.OverlaysOnly) || r.overlayRenderEnqueuer == nil || stageSkipped(resumeIdx, StageCompilingAudio) {
		return nil, nil
	}
	// Freeze ONCE, on the durable result, before any plan reaches the renderer.
	// This is intervention A: the plan the renderer executes, the plan the
	// checkpoint persists and the plan the manifest is derived from are the same
	// document, so "normalize on the snapshot" can no longer exist. Idempotent, so
	// a resumed run that already froze its plan re-derives an identical one.
	if err := FreezeFinalJobOverlayPlans(result, req.FinalJob); err != nil {
		return nil, err
	}
	pending := pendingOverlayPlans(result, req)
	if len(pending) == 0 {
		return nil, nil
	}
	// The render phase deliberately performs NO plan normalization: the
	// final-job placement policy already ran on the durable plan (see the freeze
	// above), and the output identity (certification profile + derived media
	// contract) is stamped once at the plan seal (OverlayPlan.StampOutputProfile).
	// Restating either here is how the renderer and the checkpoint diverged.
	return concurrent.Map(ctx, pending, r.overlayRenderWorkers(),
		func(renderCtx context.Context, _ int, item languageOverlayPlan) (overlayRenderOutcome, error) {
			// The plan size and boundary wall time are measured HERE, at the only
			// place that performs the blocking hand-off, so no consumer has to
			// re-time a wait it does not own.
			observability.OverlayRenderItems.Observe(float64(len(item.plan.Items)))
			// Capture the identity BEFORE the hand-off. The plan travels by value
			// but its Items slice is shared, so an enqueuer that normalizes its own
			// copy in place would otherwise change the document this digest names
			// — that is how a render of plan A came to be stamped with the digest
			// of a mutated plan A' and then looked unbound on the next pass.
			planDigest := OverlayPlanRenderDigest(item.plan)
			startedAt := time.Now()
			ref, renderErr := r.renderOverlayPlanAtBoundary(renderCtx, req, item.plan)
			boundaryWall := time.Since(startedAt)
			observability.OverlayRenderDurationSeconds.Observe(boundaryWall.Seconds())
			if renderErr != nil {
				observability.OverlayRenderTotal.WithLabelValues(renderOutcomeFailure).Inc()
				return overlayRenderOutcome{language: item.language}, fmt.Errorf("overlay render for %s failed: %w", item.language, renderErr)
			}
			observability.OverlayRenderTotal.WithLabelValues(renderOutcomeSuccess).Inc()
			recordChrononArtifactMetrics(ref)
			return overlayRenderOutcome{
				language:     item.language,
				ref:          ref,
				boundaryWall: boundaryWall,
				planItems:    len(item.plan.Items),
				// Stamp the identity of the plan the renderer just executed. The
				// worker cannot know it, and inferring it later is what allowed a
				// retry to adopt a render produced by an older plan.
				planDigest: planDigest,
			}, nil
		})
}

func (r *Runner) applyOverlayRenderOutcomes(runID string, result *GenerateResult, outcomes []overlayRenderOutcome) {
	// Apply in PLAN ORDER on the caller goroutine. The result is keyed by
	// language, so this is deterministic by construction rather than by
	// accident of which render returned first.
	sourceLanguage := result.overlayPlanLanguage()
	for _, outcome := range outcomes {
		if outcome.language == sourceLanguage {
			ref := outcome.ref
			ref.PlanDigest = outcome.planDigest
			result.OverlayRender = &ref
		} else {
			if result.LocalizedOverlayRenders == nil {
				result.LocalizedOverlayRenders = make(map[Language]RenderReference)
			}
			localized := outcome.ref
			localized.PlanDigest = outcome.planDigest
			result.LocalizedOverlayRenders[outcome.language] = localized
		}
		r.log.Info("overlay render complete",
			zap.String("run_id", runID),
			zap.String("language", string(outcome.language)),
			zap.String("render_job_id", outcome.ref.JobID),
			zap.String("status", outcome.ref.Status),
			zap.Duration("boundary_wall", outcome.boundaryWall),
			zap.Int("plan_items", outcome.planItems),
		)
	}
}

// pendingOverlayPlans filters the deterministic dispatch list down to the plans
// whose language has no persisted render yet, preserving the order from
// overlayPlansToRender. It is the ONCE-ONLY resume/idempotency gate of the
// render phase: a completed reference already on the result is reused instead
// of re-submitting duplicate GPU work.
func pendingOverlayPlans(result *GenerateResult, req GenerateRequest) []languageOverlayPlan {
	plans := overlayPlansToRender(result, req)
	pending := make([]languageOverlayPlan, 0, len(plans))
	sourceLanguage := result.overlayPlanLanguage()
	for _, item := range plans {
		if req.FinalJob && item.plan != nil {
			item.plan.RequireObjectStoreBeforeReturn = true
		}
		// Reuse requires PROOF that the persisted render belongs to this plan
		// version (intervention B). A different digest means different pixels:
		// the plan is re-rendered instead of adopting the older artifact. A
		// digest-less legacy checkpoint is reused only when its content still
		// certifies the current plan.
		if item.language == sourceLanguage && result.OverlayRender != nil {
			if overlayRenderBindsPlan(item.plan, result.OverlayRender) {
				continue
			}
		}
		if item.language != sourceLanguage {
			if ref, done := result.LocalizedOverlayRenders[item.language]; done {
				if overlayRenderBindsPlan(item.plan, &ref) {
					continue
				}
			}
		}
		pending = append(pending, item)
	}
	return pending
}

type languageOverlayPlan struct {
	language Language
	plan     *capmodel.OverlayPlan
}

func (r *GenerateResult) overlayPlanLanguage() Language {
	if r == nil || r.OverlayPlan == nil {
		return ""
	}
	return Language(strings.TrimSpace(r.OverlayPlan.Language))
}

// overlayPlansToRender makes dispatch order deterministic: source language
// first, then requested targets in caller order, followed by any persisted
// target plan missing from a resumed request.
func overlayPlansToRender(result *GenerateResult, req GenerateRequest) []languageOverlayPlan {
	if result == nil {
		return nil
	}
	var plans []languageOverlayPlan
	seen := map[Language]struct{}{}
	if result.OverlayPlan != nil {
		language := result.overlayPlanLanguage()
		plans = append(plans, languageOverlayPlan{language: language, plan: result.OverlayPlan})
		seen[language] = struct{}{}
	}
	for _, language := range req.Languages {
		if language == "" || language == result.overlayPlanLanguage() {
			continue
		}
		if plan := result.LocalizedOverlayPlans[language]; plan != nil {
			plans = append(plans, languageOverlayPlan{language: language, plan: plan})
			seen[language] = struct{}{}
		}
	}
	remaining := make([]string, 0, len(result.LocalizedOverlayPlans))
	for language := range result.LocalizedOverlayPlans {
		if _, ok := seen[language]; !ok && result.LocalizedOverlayPlans[language] != nil {
			remaining = append(remaining, string(language))
		}
	}
	sort.Strings(remaining)
	for _, raw := range remaining {
		language := Language(raw)
		plans = append(plans, languageOverlayPlan{language: language, plan: result.LocalizedOverlayPlans[language]})
	}
	return plans
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
	if len(ref.Batches) > 0 || len(ref.Items) > 0 {
		out := make([]*RenderArtifact, 0, len(ref.Batches)+len(ref.Items))
		seen := make(map[string]struct{}, len(ref.Batches)+len(ref.Items))
		for i := range ref.Batches {
			artifact := ref.Batches[i].Artifact
			if artifact == nil {
				continue
			}
			identity := strings.TrimSpace(artifact.SHA256)
			if identity != "" {
				if _, exists := seen[identity]; exists {
					continue
				}
				seen[identity] = struct{}{}
			}
			out = append(out, artifact)
		}
		for i := range ref.Items {
			item := ref.Items[i]
			if item.Artifact == nil || item.BatchID != "" {
				continue
			}
			identity := strings.TrimSpace(item.Artifact.SHA256)
			if identity != "" {
				if _, exists := seen[identity]; exists {
					continue
				}
				seen[identity] = struct{}{}
			}
			out = append(out, item.Artifact)
		}
		return out
	}
	if ref.Artifact != nil {
		return []*RenderArtifact{ref.Artifact}
	}
	return nil
}
