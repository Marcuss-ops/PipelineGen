package videocreate

import (
	"context"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Progress projection (the §20 contract) ────────────────────────────
//
// Every stage reports its band so the remote Calendar can render a
// continuous bar without knowing anything about the internals:
//
//	SCRIPTING       5-20    MEDIA_SEARCH    20-30
//	MEDIA_ACQUIRE   30-45   VOICEOVER       45-55 (incl. audio master
//	and overlay plan)       RENDERING       55-85
//	ASSEMBLING      85-92   AUDIO_FINALIZE  92-96
//	VERIFYING       96-99   FINALIZING      99-100
//
// The band table lives with the step ladder (model.go::WorkflowSteps),
// so a step can never be added without its progress band.

// reportProgress emits (percent, message) through the run's progress
// channel. It is nil-safe: a test run without broker tools still
// exercises every stage.
func (r *Run) reportProgress(percent int, message string) {
	if r.Progress == nil {
		return
	}
	r.Progress(percent, message)
}

// ── The four transitions: the ONE emission point ──────────────────────
//
// Each of these fires BOTH surfaces for a step transition:
//
//   - the transient progress bar (reportProgress → the broker/Calendar),
//   - the DURABLE per-stage projection (stage_status.go →
//     job.JobStageStatusStore, read back by GET /api/jobs/{id}/stages).
//
// coordinator.go::runStep calls exactly these four, so every step the
// workflow executes writes its canonical stage row without the step
// implementations knowing anything about the store. The durable write is
// fail-soft (see stage_status.go::report) and nil-safe: with no store wired
// the run behaves exactly as before.

// stageStarted reports the step's band-start with the canonical
// operator-facing message and marks the canonical stage `running`.
func (r *Run) stageStarted(ctx context.Context, spec StepSpec) {
	r.reportProgress(spec.BandStart, string(spec.Stage)+": "+spec.Title)
	r.stageStatus.report(ctx, spec, job.StageRunning, spec.BandStart, spec.Title)
}

// stageSucceeded reports the step's band-end and marks the canonical stage
// `completed`.
func (r *Run) stageSucceeded(ctx context.Context, spec StepSpec) {
	r.reportProgress(spec.BandEnd, string(spec.Stage)+": "+spec.Title+": completed")
	r.stageStatus.report(ctx, spec, job.StageCompleted, spec.BandEnd, spec.Title+": completed")
}

// stageSkipped reports an optional step that the payload made
// unnecessary. It still moves the bar (a silent gap reads as a hang) and
// marks the canonical stage `skipped` — the workflow did not perform that
// work, and the table says so instead of claiming a completion.
func (r *Run) stageSkipped(ctx context.Context, spec StepSpec) {
	r.reportProgress(spec.BandEnd, string(spec.Stage)+": "+spec.Title+": skipped")
	r.stageStatus.report(ctx, spec, job.StageSkipped, spec.BandEnd, spec.Title+": optional step skipped by payload")
}

// stageFailed reports a step failure before the job turns FAILED and marks
// the canonical stage `failed` with the reason an operator needs.
func (r *Run) stageFailed(ctx context.Context, spec StepSpec, reason string) {
	r.reportProgress(spec.BandStart, string(spec.Stage)+": "+spec.Title+": FAILED: "+reason)
	r.stageStatus.report(ctx, spec, job.StageFailed, spec.BandStart, spec.Title+": "+reason)
}

// stageAlreadyCompleted re-emits the durable row for a step a PREVIOUS attempt
// already finished (coordinator.go's resume branch).
//
// Without it a resumed job's stage table would only ever contain the stages
// executed after the restart — the exact moment an operator most wants the
// whole table. It deliberately does NOT touch the transient progress bar: the
// bar is re-published once at resume (CurrentStageProgress), and replaying the
// whole ladder through it would make the bar stutter backwards.
func (r *Run) stageAlreadyCompleted(ctx context.Context, spec StepSpec) {
	r.stageStatus.report(ctx, spec, job.StageCompleted, spec.BandEnd, spec.Title+": completed")
}

// ProgressBand returns the (start, end) band of a step key (used by
// tests and by the remote progress projection).
func ProgressBand(stepKey string) (int, int, bool) {
	spec, ok := StepByKey(stepKey)
	if !ok {
		return 0, 0, false
	}
	return spec.BandStart, spec.BandEnd, true
}

// CurrentStageProgress maps a current stage to the band mid-point so a
// resumed job can re-publish a truthful bar immediately after restart.
func CurrentStageProgress(state WorkflowState) int {
	for _, spec := range WorkflowSteps {
		rec := state.Stages[spec.StepKey]
		if rec == nil || (rec.Status != StageSucceeded && rec.Status != StageSkipped) {
			return spec.BandStart
		}
	}
	return 100
}
