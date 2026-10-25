// overlay_plan_intake.go — admission telemetry for one compiled overlay plan.
//
// The plan compiler is a chain of producers (planner phrases/images/maps, the
// entity resolver, light leaks) followed by admission filters (budgets, group
// caps, per-scene cap, composition). A payload can request far more overlays
// than the sealed plan contains, and until now the run result only carried the
// final item count. This tracker records, per stage, how many items the stage
// removed and which stage it was, so an operator can distinguish "the script
// never supplied the candidates" from "a budget admitted only N of them".
package scriptgeneration

import capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"

// overlayPlanIntakeTracker accumulates the per-stage admission loss. It is
// result telemetry only: it never reads or writes plan content.
type overlayPlanIntakeTracker struct {
	intake capabilityoverlay.OverlayPlanIntake
}

func newOverlayPlanIntakeTracker(scenes, timedScenes int, budget capabilityoverlay.OverlayIntakeBudget) *overlayPlanIntakeTracker {
	untimed := scenes - timedScenes
	if untimed < 0 {
		untimed = 0
	}
	return &overlayPlanIntakeTracker{intake: capabilityoverlay.OverlayPlanIntake{
		Scenes: scenes, TimedScenes: timedScenes, UntimedScenes: untimed, Budget: budget,
	}}
}

// addCandidates records items a producer contributed before any filter ran.
func (t *overlayPlanIntakeTracker) addCandidates(count int) {
	if t == nil || count <= 0 {
		return
	}
	t.intake.Candidates += count
}

// stage attributes a drop to the admission filter that caused it.
func (t *overlayPlanIntakeTracker) stage(reason string, before, after int) {
	if t == nil {
		return
	}
	t.intake.RecordStage(reason, before, after)
}

// finish seals the planner-side counters and returns the completed report.
func (t *overlayPlanIntakeTracker) finish(items []capabilityoverlay.OverlayItem) *capabilityoverlay.OverlayPlanIntake {
	if t == nil {
		return nil
	}
	t.intake.Finish(items)
	report := t.intake
	return &report
}
