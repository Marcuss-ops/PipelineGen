package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func testIntent(semanticID, kind string) VisualIntent {
	return VisualIntent{
		IntentID:     "intent-scene_03-" + semanticID,
		SemanticID:   semanticID,
		SceneID:      "scene_03",
		Kind:         VisualIntentKind(kind),
		StartUS:      12_400_000,
		DurationUS:   1_500_000,
		Priority:     80,
		PresetFamily: FamilyPersonImage,
		AssetID:      "floyd_img_04",
		EntityID:     "person:floyd-mayweather-jr",
	}
}

// TestVisualPlanCompiler_CompileSealsEvents pins that a resolved + sampled
// intent seals into an event carrying the renderer contract: id, type, asset,
// preset, animation and integer-microsecond timing.
func TestVisualPlanCompiler_CompileSealsEvents(t *testing.T) {
	in := EventInputFromSample(testIntent("sem_person_floyd", "ENTITY_IMAGE"),
		PresetSample{Preset: "portrait_right", Animation: "slide_left"})
	plan := DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{in})

	require.Equal(t, "scene_03", plan.SceneID)
	require.Len(t, plan.VisualEvents, 1)
	ev := plan.VisualEvents[0]
	require.Equal(t, "sem_person_floyd", ev.SemanticID)
	require.Equal(t, "ENTITY_IMAGE", ev.Type)
	require.Equal(t, string(FamilyPersonImage), ev.PresetFamily)
	require.Equal(t, "floyd_img_04", ev.AssetID)
	require.Equal(t, "portrait_right", ev.Preset)
	require.Equal(t, "slide_left", ev.AnimationIn)
	require.Equal(t, "", ev.AnimationIdle)
	require.Equal(t, "", ev.AnimationOut)
	require.Equal(t, int64(12_400_000), ev.StartUS)
	require.Equal(t, int64(1_500_000), ev.DurationUS)
	require.NoError(t, ev.Validate())
	require.NoError(t, plan.Validate())
}

// TestVisualPlanCompiler_EventInputFromSample pins that the sampler's single
// Animation becomes the entry animation (idle/out stay empty).
func TestVisualPlanCompiler_EventInputFromSample(t *testing.T) {
	in := EventInputFromSample(testIntent("sem_x", "ENTITY_IMAGE"),
		PresetSample{Preset: "p", Animation: "a_in"})
	require.Equal(t, "p", in.Preset)
	require.Equal(t, "a_in", in.AnimationIn)
	require.Equal(t, "", in.AnimationIdle)
	require.Equal(t, "", in.AnimationOut)
}

// TestVisualPlanCompiler_SkipsUnresolvedIntent pins fail-closed compilation:
// an intent with no semantic id or no visual kind is skipped, never sealed.
func TestVisualPlanCompiler_SkipsUnresolvedIntent(t *testing.T) {
	noSemantic := testIntent("", "ENTITY_IMAGE")
	noKind := testIntent("sem_x", "")
	plan := DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{
		{Intent: noSemantic, Preset: "p"},
		{Intent: noKind, Preset: "p"},
		EventInputFromSample(testIntent("sem_ok", "ENTITY_IMAGE"), PresetSample{Preset: "p"}),
	})
	require.Len(t, plan.VisualEvents, 1)
	require.Equal(t, "sem_ok", plan.VisualEvents[0].SemanticID)
}

// TestVisualPlanCompiler_EventIDDeterministic pins that the event id is a pure
// function of (scene, semantic) and unique per semantic id.
func TestVisualPlanCompiler_EventIDDeterministic(t *testing.T) {
	a := DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{{Intent: testIntent("sem_a", "ENTITY_IMAGE")}})
	b := DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{{Intent: testIntent("sem_a", "ENTITY_IMAGE")}})
	c := DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{{Intent: testIntent("sem_b", "ENTITY_IMAGE")}})

	require.Equal(t, a.VisualEvents[0].EventID, b.VisualEvents[0].EventID)
	require.NotEqual(t, a.VisualEvents[0].EventID, c.VisualEvents[0].EventID)
	require.Equal(t, "visual-scene-03-sem-a", a.VisualEvents[0].EventID)
}

// TestCompiledVisualPlan_Validate pins the sealed-plan invariants.
func TestCompiledVisualPlan_Validate(t *testing.T) {
	plan := DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{
		EventInputFromSample(testIntent("sem_a", "ENTITY_IMAGE"), PresetSample{Preset: "p"}),
	})
	require.NoError(t, plan.Validate())

	plan.SceneID = ""
	require.ErrorIs(t, plan.Validate(), ErrInvalidVisualPlan)
}

func TestVisualEvent_Validate(t *testing.T) {
	base := VisualEvent{EventID: "visual-1", SemanticID: "sem_a", Type: "ENTITY_IMAGE", StartUS: 0, DurationUS: 1000}
	require.NoError(t, base.Validate())

	for name, mutate := range map[string]func(*VisualEvent){
		"empty event_id": func(e *VisualEvent) { e.EventID = "" },
		"empty semantic": func(e *VisualEvent) { e.SemanticID = "" },
		"empty type":     func(e *VisualEvent) { e.Type = "" },
		"negative start": func(e *VisualEvent) { e.StartUS = -1 },
		"zero duration":  func(e *VisualEvent) { e.DurationUS = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			ev := base
			mutate(&ev)
			require.ErrorIs(t, ev.Validate(), ErrInvalidVisualPlan)
		})
	}
}
