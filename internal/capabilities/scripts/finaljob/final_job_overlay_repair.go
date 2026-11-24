package finaljob

import (
	"fmt"
	"sort"
)

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

// OverlayResumeStageAfterRepair is the resume-stage decision. A run that
// resumes at or BEFORE the overlay stage will render again anyway, so its
// stage is kept; a run that failed later (Kaufmann failed at
// PUBLISHING_DOCUMENTS) must be moved back, because no phase after
// COMPILING_AUDIO ever re-enters the overlay render.
func OverlayResumeStageAfterRepair(resumeStage Stage, report FinalJobOverlayCheckpointRepair) Stage {
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
