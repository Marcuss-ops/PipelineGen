package scriptgeneration

// final_job_overlay_repair.go owns the RECOVERY of a checkpoint whose overlay
// render no longer binds to its sealed plan (the Kaufmann failed result: the
// plan listed 28 items, the render reference 27 with IDs the plan did not have).
//
// Why a procedure and not a database edit: clearing `OverlayRender` by hand is
// not sufficient. A run resumed from a later stage never re-enters
// StageCompilingAudio, so the overlay phase stays skipped and the job proceeds
// to the final handoff with no render at all. The repair therefore does two
// things — archive the incompatible reference and tell the caller which stage
// must be replayed — while leaving every still-valid artifact (scripts, TTS,
// certified final audio, segment renders) untouched.

import (
	"context"

	"go.uber.org/zap"
)

// OverlayRepairStage is the stage a repaired checkpoint must be resumed from.
// The overlay render belongs to StageCompilingAudio; resuming from anything
// later skips it entirely (that is the trap a manual delete walks into).
func (r *Runner) repairOverlayCheckpointOnResume(ctx context.Context, runID string, resumeStage Stage, result *GenerateResult) (Stage, bool) {
	if resumeStage == StageCompleted {
		return resumeStage, false
	}
	report := RepairFinalJobOverlayCheckpoint(result)
	next := overlayResumeStageAfterRepair(resumeStage, report)
	if !report.Repaired {
		return next, false
	}
	if r != nil && r.repo != nil {
		// Persist the cleared reference FIRST, then move the durable stage. If
		// the process dies between the two writes, the next resume finds no
		// reference and keeps the old stage, which would skip the overlay phase
		// forever; if the stage write happened first and the checkpoint failed,
		// the same trap would be opened with the OLD render. This order makes
		// the only crash window a harmless re-render.
		if err := r.checkpoint(ctx, runID, result); err != nil {
			r.log.Error("overlay repair checkpoint failed; continuing with the replay stage in memory",
				zap.String("run_id", runID), zap.Error(err))
		}
		if err := r.updateStage(ctx, runID, RunStatusRunning, next); err != nil {
			r.log.Error("overlay repair stage update failed; the in-memory resume stage still replays the render",
				zap.String("run_id", runID), zap.String("resume_stage", string(next)), zap.Error(err))
		}
	}
	if r != nil && r.log != nil {
		r.log.Warn("archived an overlay render that no longer binds to the sealed plan",
			zap.String("run_id", runID),
			zap.String("resume_stage", string(next)),
			zap.Strings("reasons", report.Reasons),
			zap.Strings("dropped_localized", report.DroppedLocalized))
	}
	return next, true
}
