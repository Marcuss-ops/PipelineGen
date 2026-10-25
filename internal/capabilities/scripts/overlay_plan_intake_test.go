// Package scriptgeneration — overlay_plan_intake_test.go certifies the intake
// telemetry that explains a compiled plan against the requested budgets. The
// 100-overlay golden case compiles far fewer items than it requests; this
// report is the evidence that separates "the script supplied no candidates"
// from "an admission budget dropped them".
package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestOverlayPlanIntakeAttributesEditorialBudgetDrop certifies that a run whose
// caller ceiling admits only three of eight grounded phrases reports the plan,
// the candidates and the stage that removed the other five.
func TestOverlayPlanIntakeAttributesEditorialBudgetDrop(t *testing.T) {
	result := phraseCeilingFixture(t)
	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = 3

	plan, err := CompileOverlayPlan(result, "en", canvas, "plan-intake-budget", "video-intake", "project-intake")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.NotNil(t, plan.Intake, "a compiled plan must carry its intake report")

	intake := plan.Intake
	require.Equal(t, 1, intake.Scenes)
	require.Equal(t, 1, intake.TimedScenes)
	require.Zero(t, intake.UntimedScenes)
	require.Equal(t, 3, intake.Budget.PhraseOverlays)
	require.Equal(t, len(plan.Items), intake.Planned)
	require.Equal(t, 3, intake.PlannedByKind["text_phrase"])
	require.GreaterOrEqual(t, intake.DroppedByReason[capabilityoverlay.OverlayDropEditorialBudget], 5,
		"the eight candidates minus three admitted phrases must be attributed to the editorial budget")
	require.GreaterOrEqual(t, intake.Candidates, 8, "the intake must count the produced candidates, not only survivors")
}

// TestOverlayPlanIntakeCountsUntimedScenes certifies that scenes the compiler
// skips for lack of certified word timing are visible instead of disappearing
// from both the plan and the diagnostics.
func TestOverlayPlanIntakeCountsUntimedScenes(t *testing.T) {
	result := phraseCeilingFixture(t)
	result.Scenes = append(result.Scenes, Scene{
		ID:    "scene-no-timing",
		Index: 1,
		Text:  map[Language]string{"en": "delta four echo five"},
	})
	result.ResolvedScenes = append(result.ResolvedScenes, ResolvedScene{ID: "scene-no-timing", Index: 1, TimelineStartUS: 2_400_000, DurationUS: 800_000})

	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-intake-untimed", "video-intake", "project-intake")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.NotNil(t, plan.Intake)
	require.Equal(t, 2, plan.Intake.Scenes)
	require.Equal(t, 1, plan.Intake.TimedScenes)
	require.Equal(t, 1, plan.Intake.UntimedScenes)
}

// TestOverlayPlanIntakeStaysOffTheWireContract certifies the boundary: the
// report is result telemetry, never part of renderinggen.overlay-plan.v1, so it
// cannot change render identity or the worker schema.
func TestOverlayPlanIntakeStaysOffTheWireContract(t *testing.T) {
	result := phraseCeilingFixture(t)
	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-intake-wire", "video-intake", "project-intake")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.NotNil(t, plan.Intake)

	payload, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "intake", "the intake report must never enter the plan wire payload")
}

// TestOverlayPlanIntakeKindCountsMatchTheSealedPlan keeps the by-kind report
// honest: the per-kind counts must add up to the sealed item count.
func TestOverlayPlanIntakeKindCountsMatchTheSealedPlan(t *testing.T) {
	result := phraseCeilingFixture(t)
	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = capabilityoverlay.MaxPhraseOverlaysHardLimit

	plan, err := CompileOverlayPlan(result, "en", canvas, "plan-intake-kinds", "video-intake", "project-intake")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.NotNil(t, plan.Intake)

	total := 0
	for _, count := range plan.Intake.PlannedByKind {
		total += count
	}
	require.Equal(t, plan.Intake.Planned, total)
	require.Equal(t, len(plan.Items), total)

	// The fixture grounds eight phrases; the hard maximum admits them all, so
	// no admission stage can report a drop.
	require.Empty(t, plan.Intake.DroppedByReason)
	require.Equal(t, 8, plan.Intake.PlannedByKind["text_phrase"])
}
