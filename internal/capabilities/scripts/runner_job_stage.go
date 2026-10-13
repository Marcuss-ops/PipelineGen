package scriptgeneration

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// jobStageState tracks which job-level stage is currently open for a durable
// run so the next transition can close it.
type jobStageState struct {
	jobID string
	open  job.StageName
}

// SetJobStageSink wires the durable job stage-row store. Optional: nil keeps
// the run ledger as the only progress record.
func (r *Runner) SetJobStageSink(sink job.JobStageStatusStore) {
	if r != nil {
		r.jobStageSink = sink
	}
}

// bindJobStage associates a durable run with the job whose stage rows it feeds.
func (r *Runner) bindJobStage(runID, jobID string) {
	if r == nil || r.jobStageSink == nil || runID == "" || jobID == "" {
		return
	}
	r.jobStages.Store(runID, &jobStageState{jobID: jobID})
}

// closeJobStage closes the job stage still open when the execution returns.
// The run's terminal status decides the outcome: COMPLETED closes it as
// completed, anything else as failed. Best-effort, like every relay write.
func (r *Runner) closeJobStage(ctx context.Context, runID string) {
	if r == nil || r.jobStageSink == nil {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state := raw.(*jobStageState)
	if state.open == "" {
		return
	}
	status := job.StageFailed
	if run, err := r.repo.Get(ctx, runID); err == nil && run != nil && run.Status == RunStatusCompleted {
		status = job.StageCompleted
	}
	r.writeJobStage(ctx, state.jobID, state.open, status, time.Now().UTC())
	state.open = ""
}

// unbindJobStage removes the association once the execution has returned.
func (r *Runner) unbindJobStage(runID string) {
	if r == nil {
		return
	}
	r.jobStages.Delete(runID)
}

// jobStageForRunStage maps a run-ledger stage onto the job stage vocabulary.
// Stages without a job-level equivalent return false and are not relayed.
func jobStageForRunStage(stage Stage) (job.StageName, bool) {
	switch stage {
	case StageNormalizing, StagePreflight, StageGeneratingSceneText:
		return job.StageScript, true
	case StageTranslatingScenes:
		return job.StageTranslation, true
	case StageGeneratingVoiceovers:
		return job.StageVoiceover, true
	}
	return "", false
}

// relayJobStage writes the job stage row for a run-ledger transition. It is
// best-effort: a failed row write is logged and never fails the run.
func (r *Runner) relayJobStage(ctx context.Context, runID string, stage Stage) {
	if r == nil || r.jobStageSink == nil {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state := raw.(*jobStageState)
	closing := stage == StageCoreReady || stage == StageCompleted
	next, opens := jobStageForRunStage(stage)
	if !opens && !closing {
		return
	}
	now := time.Now().UTC()
	if state.open != "" && (closing || (opens && next != state.open)) {
		r.writeJobStage(ctx, state.jobID, state.open, job.StageCompleted, now)
		state.open = ""
	}
	if opens {
		r.writeJobStage(ctx, state.jobID, next, job.StageRunning, now)
		state.open = next
	}
}

func (r *Runner) writeJobStage(ctx context.Context, jobID string, stage job.StageName, status job.StageStatus, at time.Time) {
	rec := job.JobStageStatus{JobID: jobID, Stage: stage, Status: status, UpdatedAt: at}
	if err := r.jobStageSink.UpsertJobStageStatus(ctx, rec); err != nil && r.log != nil {
		r.log.Warn("job stage status write failed",
			zap.String("job_id", jobID),
			zap.String("stage", string(stage)),
			zap.Error(err))
	}
}
