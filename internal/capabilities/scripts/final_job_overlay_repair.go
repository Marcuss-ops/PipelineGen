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
	"fmt"
	"sort"

	"go.uber.org/zap"
)

// OverlayRepairStage is the stage a repaired checkpoint must be resumed from.
// The overlay render belongs to StageCompilingAudio; resuming from anything
// later skips it entirely (that is the trap a manual delete walks into).
const OverlayRepairStage = StageCompilingAudio

// FinalJobOverlayCheckpointRepair is the audit record of one repair. It is
// deliberately explicit: an operator must be able to see WHICH identity broke
// and WHICH artifacts were archived, without diffing two JSON snapshots.
type FinalJobOverlayCheckpointRepair struct {
	// Repaired reports that an incompatible reference was archived and cleared.
	Repaired bool
	// Archived is the reference that was moved to OverlayRenderHistory.
	Archived *RenderReference
	// Reasons are the binding violations that justified the repair.
	Reasons []string
	// DroppedLocalized lists the target languages whose render was also
	// incompatible and therefore cleared.
	DroppedLocalized []string
	// ResumeStage is the stage the caller must replay so the cleared languages
	// are actually rendered again.
	ResumeStage Stage
}

// RepairFinalJobOverlayCheckpoint archives overlay renders that do not bind to
// the sealed plan and clears them so the next attempt re-renders.
//
// Posture:
//   - nothing is deleted; the incompatible reference moves to
//     OverlayRenderHistory with its artifacts still addressable;
//   - a reference that DOES bind is left exactly as it is (the repair is
//     idempotent, so running it twice is a no-op);
//   - localized renders are decided per language against their own plan, so a
//     valid translation render survives a source-language repair;
//   - every other field of the result (scenes, segments, FinalAudio, TTS and
//     the certified segment renders) is untouched by construction.
func RepairFinalJobOverlayCheckpoint(result *GenerateResult) FinalJobOverlayCheckpointRepair {
	report := FinalJobOverlayCheckpointRepair{ResumeStage: OverlayRepairStage}
	if result == nil {
		return report
	}

	// The source render and the localized renders are independent: a valid
	// source language must never hide an incompatible translation (and a
	// missing source render must not skip the localized check entirely).
	sourceIndex := -1
	if result.OverlayRender != nil {
		reason := ""
		if result.OverlayPlan == nil {
			reason = "source overlay render has no sealed plan"
		} else if err := ValidateOverlayPlanRenderBinding(result.OverlayPlan, result.OverlayRender); err != nil {
			reason = err.Error()
		}
		if reason != "" {
			report.Reasons = append(report.Reasons, reason)
			archived := *result.OverlayRender
			result.OverlayRender = nil
			sourceIndex = len(result.OverlayRenderHistory)
			result.OverlayRenderHistory = append(result.OverlayRenderHistory, archived)
		}
	}

	languages := make([]string, 0, len(result.LocalizedOverlayRenders))
	for language := range result.LocalizedOverlayRenders {
		languages = append(languages, string(language))
	}
	sort.Strings(languages)
	dropped := make([]string, 0, len(languages))
	for _, raw := range languages {
		language := Language(raw)
		ref := result.LocalizedOverlayRenders[language]
		plan := result.LocalizedOverlayPlans[language]
		reason := ""
		if plan == nil {
			// A render with no plan cannot be certified at all.
			reason = fmt.Sprintf("%s render has no sealed plan", language)
		} else if err := ValidateOverlayPlanRenderBinding(plan, &ref); err != nil {
			reason = fmt.Sprintf("%s render: %v", language, err)
		}
		if reason == "" {
			continue
		}
		report.Reasons = append(report.Reasons, reason)
		// Archive before deleting: the artifact must stay addressable for audit.
		result.OverlayRenderHistory = append(result.OverlayRenderHistory, ref)
		delete(result.LocalizedOverlayRenders, language)
		dropped = append(dropped, string(language))
	}

	if sourceIndex >= 0 {
		// Resolve the pointer AFTER every append: growing the history may have
		// moved the backing array.
		report.Archived = &result.OverlayRenderHistory[sourceIndex]
	}
	sort.Strings(report.Reasons)
	report.DroppedLocalized = dropped
	report.Repaired = sourceIndex >= 0 || len(dropped) > 0
	return report
}

// overlayResumeStageAfterRepair is the resume-stage decision. A run that
// resumes at or BEFORE the overlay stage will render again anyway, so its
// stage is kept; a run that failed later (Kaufmann failed at
// PUBLISHING_DOCUMENTS) must be moved back, because no phase after
// COMPILING_AUDIO ever re-enters the overlay render.
func overlayResumeStageAfterRepair(resumeStage Stage, report FinalJobOverlayCheckpointRepair) Stage {
	if !report.Repaired {
		return resumeStage
	}
	replay := StageIndex(report.ResumeStage)
	current := StageIndex(resumeStage)
	if current >= 0 && current <= replay {
		return resumeStage
	}
	return OverlayRepairStage
}

// repairOverlayCheckpointOnResume is the RESUME ENTRY POINT of the repair: the
// piece that turns the procedure from dead code into the recovery path a retry
// actually walks.
//
// It runs before the resume index is frozen. When it archives an incompatible
// reference it also persists the checkpoint, so a crash before the next
// checkpoint cannot resurrect the reference the repair just removed (the
// procedure is idempotent either way).
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
