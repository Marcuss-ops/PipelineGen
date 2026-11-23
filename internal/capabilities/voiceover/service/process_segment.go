// Package voiceover — per-segment pipeline orchestration
// (PR-VO-USECASE-PROCESS-DRY; decomposed by BON-08).
//
// ProcessSegmentUseCase is the SINGLE canonical per-item voiceover
// pipeline runner. Both the batch path and the per-item path delegate
// here, so the dedupe gate, TTS, timing bundle, publish and finalize
// semantics cannot diverge between callers.
//
// Execute is deliberately thin: it sequences the testable phase owners.
//
//	process_segment_prepare.go          Stage 0 validation + result envelope
//	process_segment_cache.go            cross-run cache probe + reuse decision
//	process_segment_synthesis.go        Stage 1 TTS + Stage 2 audio post
//	process_segment_publish.go          Stage 3 metadata + upload + timing bundle
//	process_segment_publish_failure.go  Stage 3 wrapper + failure classification
//	process_segment_finalize.go         Stage 4 BeginTx + dedupe/finalize + commit
//	process_segment_async.go            async publish hand-off and worker body
//	process_segment_orphan.go           orphan-cleanup outbox tx
//
// godlike/06 SSOT: ProcessSegmentUseCase owns the per-item pipeline;
// the finalizer owns the dedupe gate and the persistence sequence
// sequenced by process_segment_finalize.go.
//
// godlike/07 honest-limitation: this extraction does NOT touch the
// bounded parallel fan-out in the batch caller (the *Executor field).
// Fan-out decides how to schedule N tasks; this use case executes ONE.
package voiceover

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

func (u *ProcessSegmentUseCase) Execute(ctx context.Context, cmd *ProcessSegmentCommand) (*VoiceoverItemResult, error) {
	out, err := u.prepareSegment(cmd)
	if err != nil {
		return out, err
	}
	log := u.deps.Logger
	timingPolicy := timingPolicyFor(cmd)

	// Cross-run cache: a verified hit skips TTS, upload and finalize.
	if cached, hit := u.lookupCachedResult(ctx, cmd, timingPolicy, log); hit {
		observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
		return cached, nil
	}

	// Stage 1: TTSProvider.Synthesize.
	ttsOut, err := u.synthesizeStage(ctx, cmd, timingPolicy, log)
	if err != nil {
		return out, failSegment(out, cmd, StageTTS, true, FailureTTS, err)
	}
	applySynthesisOutput(out, ttsOut)

	// Stage 2: optional AudioPostProcessor (silence removal).
	post, err := u.postProcessStage(ctx, cmd, ttsOut, log)
	if err != nil {
		return out, failSegment(out, cmd, StageAudioPost, false, FailureAudioPost, err)
	}
	if report := applyAudioPostOutput(out, ttsOut, post); report != nil {
		out.SilenceCleanup = report
		silenceCleanupEvent(log, cmd, report)
	}

	if !hasLocalPayload(out) {
		return out, failSegment(out, cmd, StageTTS, false, FailureNoLocalPayload,
			fmt.Errorf("TTSProvider + AudioPostProcessor produced no local path"))
	}

	// Async publish is only an audio-only delivery optimisation; timing
	// stays synchronous because the script runner consumes the artifact.
	if shouldPublishAsync(u.deps.Cache.AsyncPublish, u.deps.Publisher, timingPolicy) {
		u.startAsyncPublish(ctx, cmd, out, ttsOut, post, log)
		return out, nil
	}

	// Stage 3: publish (metadata + idempotency key + upload + timing).
	pub, err := u.publishAndClassify(ctx, cmd, out, &ttsOut, post, log)
	if err != nil {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, err
	}

	// Stage 4: BeginTx + dedupe/finalize + commit.
	if _, err := u.finalizeStage(ctx, cmd, out, pub, ttsOut, post, log); err != nil {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, err
	}

	out.Status = StatusCompleted
	setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
	observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
	return out, nil
}
