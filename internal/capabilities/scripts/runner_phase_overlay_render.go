package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderqueue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

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
	language     contract.Language
	ref          contract.RenderReference
	boundaryWall time.Duration
	planItems    int
	// planDigest is the identity of the plan this render was produced from. It
	// is written by the caller that owns the plan, never by the worker.
	planDigest string
	// preflight carries the digest-bound image verification performed before
	// the first submission on the snapshot. The joined caller copies it onto
	// the durable result so the late handoff does not repeat the downloads.
	preflight *contract.FinalJobPreflight
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
func (r *Runner) runOverlayRenderPhase(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, resumeIdx int, state audioCompileState, result *contract.GenerateResult) bool {
	ctx = r.withMapRenderProgress(ctx, runID)
	outcomes, fanOutErr := r.renderOverlayPlans(ctx, req, resumeIdx, result)
	if fanOutErr != nil {
		r.failExecutionStepBestEffort(ctx, exec, state.Step, fanOutErr)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, fanOutErr)
		return false
	}
	r.applyOverlayRenderOutcomes(runID, result, outcomes)
	return true
}

// withMapRenderProgress projects the native dynamic-map subprocess lifecycle
// onto jobs.progress/current_detail. The map renderer reports its own PID and
// elapsed time; the runner only relays those owner-measured facts. The coarse
// job stage stays render because the canonical job vocabulary has no map stage;
// current_detail carries the map-specific phase.
func (r *Runner) withMapRenderProgress(ctx context.Context, runID string) context.Context {
	if r == nil || runID == "" {
		return ctx
	}
	// The callback is keyed by run, while jobs.progress is keyed by the
	// broker job bound to that run. Resolve it once; a run without a bound
	// job has no aggregate row to update.
	jobID := r.jobIDForRun(runID)
	if jobID == "" {
		return ctx
	}
	// A renderer can report its exit after the job context was cancelled. Keep
	// cancellation from hiding the terminal map event, but bound this best-
	// effort database write so shutdown cannot block on telemetry.
	baseCtx := context.WithoutCancel(ctx)
	return renderqueue.WithMapRenderProgress(ctx, func(progress renderqueue.MapRenderProgress) {
		detail := fmt.Sprintf("map_render %s pid=%d elapsed=%s",
			progress.Phase, progress.PID, progress.Elapsed.Round(time.Second))
		if progress.Result != "" {
			detail += " result=" + progress.Result
		}
		if progress.Phase == "exit" && progress.ExitCode >= 0 {
			detail += fmt.Sprintf(" exit_code=%d", progress.ExitCode)
		}
		if progress.Err != nil {
			detail += ": " + progress.Err.Error()
		}
		aggregate := 72
		if progress.Phase == "exit" {
			aggregate = 74
		}
		writeCtx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
		defer cancel()
		data := map[string]any{
			"phase":      "map_render",
			"map_phase":  progress.Phase,
			"pid":        progress.PID,
			"elapsed_ms": progress.Elapsed.Milliseconds(),
		}
		if progress.Result != "" {
			data["result"] = progress.Result
		}
		if progress.Phase == "exit" && progress.ExitCode >= 0 {
			data["exit_code"] = progress.ExitCode
		}
		r.writeJobProgress(writeCtx, jobID, job.StageRender, aggregate, detail, data)
	})
}

// renderOverlayPlanAtBoundary is the ONLY local overlay submission shape.
//
// PipelineGen renders independent per-item alpha clips. The whole-timeline
// canvas and the per-scene split were removed on 2026-11-12: a local run must
// never encode a full timeline or a scene-wide composition. If the wired
// enqueuer cannot render per item, the run fails closed instead of falling
// back to a timeline render.
func (r *Runner) renderOverlayPlanAtBoundary(ctx context.Context, req contract.GenerateRequest, plan *capmodel.OverlayPlan) (contract.RenderReference, error) {
	if plan == nil {
		return contract.RenderReference{}, nil
	}
	// Propagate the request's explicit GPU declaration to the overlay job and,
	// through the child-plan copies built by the enqueuer, to every per-item
	// render. Nil stays nil: a payload that declares nothing keeps the worker's
	// execution policy untouched (the historical behaviour). The field is
	// `json:"-"` on the plan, so the plan document — and its digest — never
	// changes because of it.
	plan.RequireGPU = req.Render.RequireGPU
	return r.overlayRenderEnqueuer.EnqueueChrononPlanItems(ctx, *plan)
}

// renderOverlayPlans computes against a private snapshot. It never writes the
// durable run or execution step; the joined caller owns failures and results.
// renderOverlayPlans is the serial entry point: it freezes the durable plan
// and renders it through the same typed core the overlap path uses.
func (r *Runner) renderOverlayPlans(ctx context.Context, req contract.GenerateRequest, resumeIdx int, result *contract.GenerateResult) ([]overlayRenderOutcome, error) {
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
	return r.renderOverlayInput(ctx, req, newOverlayRenderInput(result, req, resumeIdx))
}

// renderOverlayInput is the render fan-out over a typed immutable input. It
// never reads or writes the durable result: the plans, the scene clip windows
// and the preflight marker were copied on the owner goroutine.
func (r *Runner) renderOverlayInput(ctx context.Context, req contract.GenerateRequest, input overlayRenderInput) ([]overlayRenderOutcome, error) {
	if len(input.Plans) == 0 || r.overlayRenderEnqueuer == nil {
		return nil, nil
	}
	if req.FinalJob && req.MediaMode == scriptpkg.MediaModeClipOnly {
		if err := validateClipImageBackgroundCoverage(input.Plans); err != nil {
			return nil, err
		}
	}
	// Fail BEFORE paying for the first GPU render. A final job whose promised
	// images are not verifiable can never ship them: the late handoff would
	// reject it after the overlay stage had already charged the full render.
	// The gate is digest-bound, so the late fail-closed checks still run when
	// the plan changes but do not repeat the downloads when it does not.
	document := input.document()
	if req.FinalJob {
		if err := finalJobPreRenderPreflight(ctx, req, document); err != nil {
			return nil, err
		}
	}
	// The render phase deliberately performs NO plan normalization: the
	// final-job placement policy already ran on the durable plan (see the freeze
	// above), and the output identity (certification profile + derived media
	// contract) is stamped once at the plan seal (OverlayPlan.StampOutputProfile).
	// Restating either here is how the renderer and the checkpoint diverged.
	outcomes, err := concurrent.Map(ctx, input.Plans, r.overlayRenderWorkers(),
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
	if err != nil {
		return nil, err
	}
	// The preflight ran on the render snapshot. Attach its marker to the
	// outcomes so applyOverlayRenderOutcomes can publish it on the durable
	// result: the late handoff then trusts the same verified plan instead of
	// downloading its images a second time.
	if document.FinalJobPreflight != nil {
		for i := range outcomes {
			outcomes[i].preflight = document.FinalJobPreflight
		}
	}
	return outcomes, nil
}

// validateClipImageBackgroundCoverage prevents a clip-only final job from
// producing opaque image artifacts over a renderer default canvas when its
// source clip could not be resolved. It runs before any remote render is paid.
func validateClipImageBackgroundCoverage(plans []languageOverlayPlan) error {
	for _, localized := range plans {
		for _, item := range localized.plan.Items {
			if !capabilityoverlay.IsAssetImageOverlayItem(item) {
				continue
			}
			background := localized.plan.SceneBackgrounds[strings.TrimSpace(item.SceneID)]
			if background == nil || background.Kind != "video" || len(background.AssetRefs) != 1 ||
				strings.TrimSpace(background.AssetRefs[0].AssetID) == "" || strings.TrimSpace(background.AssetRefs[0].SHA256) == "" {
				return fmt.Errorf("clip-only final_job image overlay %q has no verified source-clip background for scene %q", item.ID, item.SceneID)
			}
		}
	}
	return nil
}

func (r *Runner) applyOverlayRenderOutcomes(runID string, result *contract.GenerateResult, outcomes []overlayRenderOutcome) {
	// Apply in PLAN ORDER on the caller goroutine. The result is keyed by
	// language, so this is deterministic by construction rather than by
	// accident of which render returned first.
	sourceLanguage := result.OverlayPlanLanguage()
	for _, outcome := range outcomes {
		if outcome.preflight != nil {
			result.FinalJobPreflight = outcome.preflight
		}
		if outcome.language == sourceLanguage {
			ref := outcome.ref
			ref.PlanDigest = outcome.planDigest
			result.OverlayRender = &ref
		} else {
			if result.LocalizedOverlayRenders == nil {
				result.LocalizedOverlayRenders = make(map[contract.Language]contract.RenderReference)
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
			zap.Int("cache_hits", outcome.ref.CacheHits),
			zap.Int("rendered_items", outcome.ref.RenderedItems),
		)
	}
}

// pendingOverlayPlans filters the deterministic dispatch list down to the plans
// whose language has no persisted render yet, preserving the order from
// overlayPlansToRender. It is the ONCE-ONLY resume/idempotency gate of the
// render phase: a completed reference already on the result is reused instead
// of re-submitting duplicate GPU work.
func pendingOverlayPlans(result *contract.GenerateResult, req contract.GenerateRequest) []languageOverlayPlan {
	plans := overlayPlansToRender(result, req)
	pending := make([]languageOverlayPlan, 0, len(plans))
	sourceLanguage := result.OverlayPlanLanguage()
	for _, item := range plans {
		if item.plan != nil {
			// Every overlay render is an INTERMEDIATE: the local join and the
			// final-job handoff both consume the certified object-store locator
			// (artifact.URL / StorageKey), never a Drive identity. Publishing it
			// to Drive would copy bytes that already have a reader elsewhere,
			// so the non-final path must not do it either. The final video is
			// unaffected: the join fetches the same artifact.
			item.plan.RequireObjectStoreBeforeReturn = true
		}
		// A script may deliberately disable every semantic overlay while still
		// using the final-job lane for clip assets and audio. Such a plan has no
		// local RenderingGen work: dispatching it creates a doomed empty render
		// job and prevents the remote final compositor from receiving the clips.
		if item.plan == nil || len(overlayRenderableCandidates(*item.plan)) == 0 {
			continue
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
	language contract.Language
	plan     *capmodel.OverlayPlan
}

// overlayPlansToRender makes dispatch order deterministic: source language
// first, then requested targets in caller order, followed by any persisted
// target plan missing from a resumed request.
func overlayPlansToRender(result *contract.GenerateResult, req contract.GenerateRequest) []languageOverlayPlan {
	if result == nil {
		return nil
	}
	var plans []languageOverlayPlan
	seen := map[contract.Language]struct{}{}
	if result.OverlayPlan != nil {
		language := result.OverlayPlanLanguage()
		plans = append(plans, languageOverlayPlan{language: language, plan: result.OverlayPlan})
		seen[language] = struct{}{}
	}
	for _, language := range req.Languages {
		if language == "" || language == result.OverlayPlanLanguage() {
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
		language := contract.Language(raw)
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
func recordChrononArtifactMetrics(ref contract.RenderReference) {
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
func renderedArtifacts(ref contract.RenderReference) []*contract.RenderArtifact {
	if len(ref.Batches) > 0 || len(ref.Items) > 0 {
		out := make([]*contract.RenderArtifact, 0, len(ref.Batches)+len(ref.Items))
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
		return []*contract.RenderArtifact{ref.Artifact}
	}
	return nil
}

// overlayRenderInput is the typed immutable input of the overlay render
// fan-out (DEMOL-07). The render goroutine used to receive a JSON clone of the
// whole durable result only to reach the frozen plans, the scene clip windows
// and the image-preflight marker. Those facts are copied here on the caller
// goroutine; the render branch then works on its own minimal document and
// never touches the durable result.

// overlayRenderInput is the render-side projection of a run: the detached
// plans in deterministic dispatch order plus the read-only facts the
// final-job image gate needs.
type overlayRenderInput struct {
	SourceLanguage contract.Language
	Plans          []languageOverlayPlan
	Scenes         []contract.Scene
	OverlayAudit   *contract.OverlayAudit
	Preflight      *contract.FinalJobPreflight
}

// newOverlayRenderInput copies the render facts out of the durable result.
// Callers must invoke it on the result's owner goroutine.
func newOverlayRenderInput(result *contract.GenerateResult, req contract.GenerateRequest, resumeIdx int) overlayRenderInput {
	input := overlayRenderInput{}
	if result == nil || (!req.Render.Enabled && !req.Render.OverlaysOnly) || stageSkipped(resumeIdx, StageCompilingAudio) {
		return input
	}
	input.SourceLanguage = result.OverlayPlanLanguage()
	input.Scenes = result.Scenes
	input.OverlayAudit = result.OverlayAudit
	input.Preflight = result.FinalJobPreflight
	sceneBackgrounds := scriptclips.SceneOverlayBackgrounds(req, result)
	for _, item := range pendingOverlayPlans(result, req) {
		// The renderer and the preflight may normalize their plan in place
		// (RequireGPU, RequireObjectStoreBeforeReturn); the durable plan must
		// stay the exact document the checkpoint names.
		plan := cloneOverlayPlan(item.plan)
		plan.SceneBackgrounds = sceneBackgrounds
		input.Plans = append(input.Plans, languageOverlayPlan{
			language: item.language,
			plan:     plan,
		})
	}
	return input
}

// document builds the branch-private result the image-preflight gate reads.
// It carries only the frozen plans and their audit surfaces.
func (in overlayRenderInput) document() *contract.GenerateResult {
	document := &contract.GenerateResult{
		Scenes:            in.Scenes,
		OverlayAudit:      in.OverlayAudit,
		FinalJobPreflight: in.Preflight,
	}
	for _, item := range in.Plans {
		if item.language == in.SourceLanguage {
			document.OverlayPlan = item.plan
			continue
		}
		if document.LocalizedOverlayPlans == nil {
			document.LocalizedOverlayPlans = make(map[contract.Language]*capmodel.OverlayPlan, len(in.Plans))
		}
		document.LocalizedOverlayPlans[item.language] = item.plan
	}
	return document
}
