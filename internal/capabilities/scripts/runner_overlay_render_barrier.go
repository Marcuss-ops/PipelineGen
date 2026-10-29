// Package scriptgeneration — runner_overlay_render_barrier.go owns the P2
// overlay-language submit-barrier (TICKET-OVERLAY-QUEUE-OVERLAP).
//
// The dependency boundary this design respects: a localized OverlayPlan can
// only compile after EVERY TTS unit of the run is final, because the planner
// reads the run-wide CanonicalTimeline and EntityTimeline. A per-language
// "join TTS of language L" plan trigger is therefore structurally unreachable
// today — the plan branch (P1, runner_overlay_plan_branch.go) is already the
// earliest certified point. What was still serial after P1 is the RENDER
// SUBMISSION: today the overlay render fork only starts rendering when the
// master OnPlanReady hand-off fires, i.e. after the master canonical audio
// plan + scene persistence joined the branch projection. This barrier closes
// exactly that gap:
//
//	plan branch (P1) ──► barrier renders EVERY pending plan ──► master compile ──► OnPlanReady fork joins
//	                     while the master audio compile runs     still running   (instant: refs already there)
//
// Why it is safe:
//   - The barrier renders from branch.input — the SAME detached projection the
//     master join applies — so plan identity, submission shape and observability
//     records (same gates, same boundary metrics, same enqueuer contract) are
//     byte-identical to the serial path shaped by pendingOverlayPlans.
//   - The barrier NEVER writes the durable result on its own goroutine. It
//     stashes certified outcomes; the OnPlanReady render fork applies them onto
//     its own detached snapshot (single writer), and the fork's done-join
//     carries them into the durable result in deterministic plan order — the
//     exact same join the master's own outcomes take today.
//   - Exactly once is structural: the merge happens BEFORE the fork's
//     renderOverlayPlans captures its pending set (which reads the now-claimed
//     references), so a language the barrier certified is never re-submitted.
//     On a partial barrier failure only the CERTIFIED outcomes merge; the fork
//     re-renders every language the barrier left unclaimed, so a consumer that
//     ever SUCCEEDS owns exactly one submission per language, and the final
//     fail-closed surface is today's (run fails against AUDIO_COMPILE).
//   - Ready is not authority: any barrier error is logged and absorbed. The
//     run is failed by the fork's normal render error path, exactly as it
//     would fail without this file.
//   - Off by default (OVERLAY_RENDER_LANG_BARRIER), non-final jobs only: the
//     final-job publication-drainer and placement normalization keep their
//     OnPlanReady scheduling untouched.
package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// overlayLanguageBarrierEnabled reads the opt-in feature gate once per call,
// mirroring overlayPerSceneSplitEnabled's env contract.
func overlayLanguageBarrierEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OVERLAY_RENDER_LANG_BARRIER"))) {
	case "enabled", "on", "true", "1":
		return true
	default:
		return false
	}
}

// overlayLanguageBarrier renders the frozen plans from the P1 plan-branch
// outcome while the master audio compile is still running, and hands the
// certified outcomes to the OnPlanReady render fork at its join.
type overlayLanguageBarrier struct {
	r         *Runner
	runID     string
	req       GenerateRequest
	resumeIdx int
	jobID     string

	// ctx drives the barrier render fan-out; it is a child of the
	// audio-compile context so a master failure stops the barrier instead of
	// leaving submissions in flight behind a dead run.
	ctx      context.Context
	cancel   context.CancelFunc
	branch   *overlayPlanBranch
	finished chan struct{}

	outcomes []overlayRenderOutcome
	err      error
}

// startOverlayRenderBarrier spawns the barrier render goroutine for a run on
// the OnPlanReady overlap path whose plan branch already started. It returns
// nil — and starts nothing — for every configuration where the master fork
// must keep today's semantics: overlap path not active, final jobs, no render
// requested, no enqueuer, stage already resumed past, or the gate off.
func (r *Runner) startOverlayRenderBarrier(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, branch *overlayPlanBranch) *overlayLanguageBarrier {
	if branch == nil || !overlayLanguageBarrierEnabled() || req.FinalJob {
		return nil
	}
	if r.overlayRenderEnqueuer == nil || (!req.Render.Enabled && !req.Render.OverlaysOnly) {
		return nil
	}
	if stageSkipped(resumeIdx, StageCompilingAudio) {
		return nil
	}
	barrier := &overlayLanguageBarrier{
		r:         r,
		runID:     runID,
		req:       req,
		resumeIdx: resumeIdx,
		jobID:     exec.JobID,
		branch:    branch,
		finished:  make(chan struct{}),
	}
	barrier.ctx, barrier.cancel = context.WithCancel(ctx)
	go barrier.run()
	return barrier
}

// run waits for the detached plan projection, renders every pending plan and
// stashes the outcome. It never writes the durable result: the OnPlanReady
// render fork is the single writer (via applyToSnapshot).
func (b *overlayLanguageBarrier) run() {
	defer func() {
		b.cancel()
		close(b.finished)
	}()
	outcome := b.branch.outcome()
	if outcome.err != nil {
		// The branch's own join fails the run through the owning audio-compile
		// step; the barrier only records why it did not accelerate it.
		b.err = outcome.err
		b.r.log.Warn("overlay language barrier skipped: plan branch failed",
			zap.String("run_id", b.runID), zap.Error(b.err))
		return
	}
	// Broker job identity: the same assertion the OnPlanReady fork performs on
	// its snapshot (runner_phase_overlay_plan.go). It is asserted on a PRIVATE
	// deep copy, never on the detached plans: from branch delivery on those
	// shared objects must stay read-only — the master's OnPlanReady hand-off
	// marshals them concurrently, and a same-value write there would be a data
	// race even though it could not corrupt the value.
	cloned, cloneErr := snapshotGenerateResult(outcome.input)
	if cloneErr != nil {
		b.err = fmt.Errorf("overlay language barrier clone: %w", cloneErr)
		b.r.log.Warn("overlay language barrier skipped: snapshot clone failed",
			zap.String("run_id", b.runID), zap.Error(b.err))
		return
	}
	setOverlayDriveJobID(cloned, b.jobID)
	submitted, err := b.r.renderOverlayPlans(b.ctx, b.req, b.resumeIdx, cloned)
	for _, item := range submitted {
		// Merge only CERTIFIED references: an empty JobID from a failed
		// language leaves the reference unset so the range fork renders it.
		if strings.TrimSpace(item.ref.JobID) == "" {
			continue
		}
		b.outcomes = append(b.outcomes, item)
	}
	if err != nil {
		b.err = err
	}
	b.r.log.Info("overlay language barrier submitted plans",
		zap.String("run_id", b.runID),
		zap.Int("submitted", len(b.outcomes)),
		zap.Bool("complete", err == nil))
}

// applyToSnapshot merges the barrier's certified outcomes onto the detached
// render snapshot on the CALLER's goroutine — the OnPlanReady render fork — and
// returns them so the fork carries them into its durable-result join (the only
// path that ever applies render outcomes to the result). It blocks until the
// barrier has finished its submission work, so the fork's pendingOverlayPlans
// (which runs right after) reads a claimed set and never re-submits a language
// the barrier was composing. A barrier error is never a run failure from here:
// the fork's render path keeps the fail-closed surface it had before this file
// existed.
func (b *overlayLanguageBarrier) applyToSnapshot(snapshot *GenerateResult) []overlayRenderOutcome {
	if b == nil || snapshot == nil {
		return nil
	}
	select {
	case <-b.finished:
	case <-b.ctx.Done():
		<-b.finished
	}
	if len(b.outcomes) > 0 {
		b.r.applyOverlayRenderOutcomes(b.runID, snapshot, b.outcomes)
	}
	if b.err != nil {
		b.r.log.Warn("overlay language barrier incomplete; the render fork re-renders the unclaimed languages",
			zap.String("run_id", b.runID), zap.Error(b.err))
	}
	return b.outcomes
}
