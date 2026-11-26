package scriptgeneration

// final_job_split.go is the runner half of the split remote handoff. The base
// PREPARE runs as a sibling of the overlay render, so the Master can queue and
// prefetch the source job while the GPU is still composing overlays. The
// overlay FINALIZE stays on the final-job phase, after the render is certified.

import (
	"context"
	"errors"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"go.uber.org/zap"
)

// finalJobSplitOutcome is the early PREPARE result joined at the overlay render
// boundary. started/finished are the interval used to attribute the overlap.
type finalJobSplitOutcome struct {
	receipt  RemoteFinalJobResult
	err      error
	started  time.Time
	finished time.Time
}

// startFinalJobSplitPrepare launches the early base PREPARE. It returns nil
// whenever the serial handoff must stay authoritative: final job disabled,
// adapter without the split contract, no overlay sibling to overlap, a
// non-replayable receipt, or a missing result.
func (r *Runner) startFinalJobSplitPrepare(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) chan finalJobSplitOutcome {
	if r == nil || !req.FinalJob || result == nil || result.FinalAudio == nil {
		return nil
	}
	preparer, ok := r.finalJobSubmitter.(FinalJobSplitPreparer)
	if !ok || !preparer.FinalJobSplitEnabled() {
		return nil
	}
	// A PREPARED job already exists: FINALIZE is the only legal next step.
	// A terminal receipt is handled by the serial attempt rotation, and a
	// plain PREPARE_INTENT (no split marker) is the serial reconciliation path.
	if receipt := result.RemoteFinalJob; receipt != nil {
		if finalJobPreparedID(receipt) != "" || finalJobReceiptSucceeded(receipt) || finalJobReceiptTerminal(receipt) {
			return nil
		}
		if !(strings.EqualFold(strings.TrimSpace(receipt.Status), "PREPARE_INTENT") && receipt.Split) {
			return nil
		}
	}
	// The base handoff receives a typed immutable projection: only the facts
	// the PREPARE payload reads are copied, and the overlay-only fields stay
	// out of the detached document by construction.
	snapshot := newFinalJobSplitInput(runID, "", result).document()
	if result.RemoteFinalJob == nil {
		// Durable intent before the network: a process restart can replay the
		// SAME base plan (and therefore the same idempotency key) instead of
		// guessing whether a PREPARE ever left this host.
		intent := RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: 1, Split: true}
		result.RemoteFinalJob = &intent
		r.checkpoint(ctx, runID, result)
	}
	outcome := make(chan finalJobSplitOutcome, 1)
	started := time.Now()
	go func() {
		receipt, err := preparer.PrepareFinalJobSource(ctx, runID, req, snapshot)
		outcome <- finalJobSplitOutcome{receipt: receipt, err: err, started: started, finished: time.Now()}
	}()
	if r.log != nil {
		r.log.Info("final_job split PREPARE started alongside the overlay render",
			zap.String("run_id", runID),
			zap.Int("attempt", finalJobSplitAttempt(result)))
	}
	return outcome
}

// applyFinalJobSplitPrepare records the early PREPARE before the final-job
// phase inspects the result. A failure is never fatal here: no job exists, so
// the serial path can still build and submit the full handoff. A success is
// durable immediately, because the next attempt must FINALIZE this job instead
// of asking the Master for another render.
func (r *Runner) applyFinalJobSplitPrepare(ctx context.Context, runID string, result *GenerateResult, outcome finalJobSplitOutcome, overlayFinishedAt time.Time) {
	if result == nil {
		return
	}
	if outcome.err != nil {
		if r.log != nil {
			r.log.Warn("final_job split PREPARE failed; falling back to the serial handoff",
				zap.String("run_id", runID),
				zap.String("remote_job_id", outcome.receipt.JobID),
				zap.Error(outcome.err))
		}
		if strings.TrimSpace(outcome.receipt.JobID) == "" {
			if errors.Is(outcome.err, ErrFinalJobSplitPrepareNotSent) {
				// The request never reached the Master: discard the split intent
				// so the serial handoff owns this attempt without replaying it.
				if receipt := result.RemoteFinalJob; receipt != nil && receipt.Split && strings.EqualFold(strings.TrimSpace(receipt.Status), "PREPARE_INTENT") {
					result.RemoteFinalJob = nil
					r.checkpoint(ctx, runID, result)
				}
				return
			}
			// The response was lost after a possible send. Keep the durable
			// intent: submitFinalJob replays the SAME base plan/key before it
			// decides to fall back, so the Master can reconcile instead of
			// starting a second render.
			if r.log != nil {
				r.log.Warn("final_job split PREPARE outcome is ambiguous; keeping the base intent for same-key replay",
					zap.String("run_id", runID), zap.Error(outcome.err))
			}
			return
		}
	}
	receipt := outcome.receipt
	if strings.TrimSpace(receipt.JobID) == "" {
		return
	}
	receipt.Split = true
	if receipt.Attempt < 1 {
		receipt.Attempt = finalJobSplitAttempt(result)
	}
	overlap := time.Duration(0)
	if outcome.finished.After(outcome.started) {
		end := outcome.finished
		if !overlayFinishedAt.IsZero() && overlayFinishedAt.Before(end) {
			end = overlayFinishedAt
		}
		if end.After(outcome.started) {
			overlap = end.Sub(outcome.started)
		}
	}
	if receipt.Timing == nil {
		receipt.Timing = &RemoteFinalJobTiming{}
	}
	receipt.Timing.SplitPrepareMS = outcome.finished.Sub(outcome.started).Milliseconds()
	receipt.Timing.SplitOverlapMS = overlap.Milliseconds()
	result.RemoteFinalJob = &receipt
	recordFinalJobReport(ctx, &receipt)
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("final_job split PREPARE accepted",
			zap.String("run_id", runID),
			zap.String("remote_job_id", receipt.JobID),
			zap.String("remote_status", receipt.Status),
			zap.Int64("split_prepare_ms", receipt.Timing.SplitPrepareMS),
			zap.Int64("split_overlap_ms", receipt.Timing.SplitOverlapMS))
	}
}

func finalJobSplitAttempt(result *GenerateResult) int {
	if result == nil || result.RemoteFinalJob == nil || result.RemoteFinalJob.Attempt < 1 {
		return 1
	}
	return result.RemoteFinalJob.Attempt
}

// FinalJobSplitInput is the typed immutable input of the early split
// PREPARE (DEMOL-07). The base handoff never reads the overlay projection: it
// needs the base timeline, its certified audio and the base scene projections.
// Copying exactly those facts replaces the former full-result JSON clone and
// mirrors the payload package's own finalJobInputFor projection.

// FinalJobSplitInput is the immutable input of the split PREPARE goroutine.
type FinalJobSplitInput struct {
	RunID string
	JobID string

	Scenes            []contract.Scene
	CanonicalTimeline *capabilityaudio.CanonicalTimeline
	FinalAudio        *contract.FinalAudioReference

	OverlayPlan         *capmodel.OverlayPlan
	OverlayAudit        *contract.OverlayAudit
	OverlayRender       *contract.RenderReference
	LocalizedRenders    []contract.LocalizedRenderResult
	RemoteFinalJob      *contract.RemoteFinalJobResult
	FinalJobPreflight   *contract.FinalJobPreflight
	PhraseOverlayBudget *capabilityoverlay.PhraseOverlayBudget
}

// newFinalJobSplitInput copies the base-handoff facts on the phase goroutine.
func newFinalJobSplitInput(runID, jobID string, result *contract.GenerateResult) FinalJobSplitInput {
	input := FinalJobSplitInput{RunID: runID, JobID: jobID}
	if result == nil {
		return input
	}
	input.Scenes = result.Scenes
	input.CanonicalTimeline = result.CanonicalTimeline
	input.FinalAudio = result.FinalAudio
	input.OverlayPlan = result.OverlayPlan
	input.OverlayAudit = result.OverlayAudit
	input.OverlayRender = result.OverlayRender
	input.LocalizedRenders = result.LocalizedRenders
	input.RemoteFinalJob = result.RemoteFinalJob
	input.FinalJobPreflight = result.FinalJobPreflight
	input.PhraseOverlayBudget = result.PhraseOverlayBudget
	return input
}

// document returns the branch-private base projection. It matches the
// finaljob package's own input projection field for field, so the payload the
// early PREPARE builds is byte-identical to the serial handoff's.
func (in FinalJobSplitInput) document() *contract.GenerateResult {
	return &contract.GenerateResult{
		AudioOutput: contract.AudioOutput{
			CanonicalTimeline: in.CanonicalTimeline,
			FinalAudio:        in.FinalAudio,
		},
		Scenes:              in.Scenes,
		OverlayPlan:         in.OverlayPlan,
		OverlayAudit:        in.OverlayAudit,
		OverlayRender:       in.OverlayRender,
		LocalizedRenders:    in.LocalizedRenders,
		RemoteFinalJob:      in.RemoteFinalJob,
		FinalJobPreflight:   in.FinalJobPreflight,
		PhraseOverlayBudget: in.PhraseOverlayBudget,
	}
}
