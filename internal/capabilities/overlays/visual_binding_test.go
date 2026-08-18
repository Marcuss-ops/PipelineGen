package overlays

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPlan(t *testing.T) CompiledVisualPlan {
	t.Helper()
	return DefaultVisualPlanCompiler.Compile("scene_03", []VisualEventInput{
		EventInputFromSample(testIntent("sem_person_floyd", "ENTITY_IMAGE"), PresetSample{Preset: "portrait_right", Animation: "slide_left"}),
		EventInputFromSample(testIntent("sem_money_100m", "IMPORTANT_NUMBER"), PresetSample{Preset: "money_large_center", Animation: "scale_pop"}),
	})
}

// TestBuildVisualBindings pins that a compiled plan projects 1:1 onto binding
// rows with the given stage versions.
func TestBuildVisualBindings(t *testing.T) {
	plan := testPlan(t)
	bindings := BuildVisualBindings(7, plan, VisualIntentResolverVersion, DeterministicPresetSamplerVersion)
	require.Len(t, bindings, len(plan.VisualEvents))
	for i, b := range bindings {
		require.Equal(t, int64(7), b.ScriptID)
		require.Equal(t, plan.VisualEvents[i].EventID, b.VisualEventID)
		require.Equal(t, plan.VisualEvents[i].SemanticID, b.SemanticID)
		require.Equal(t, plan.VisualEvents[i].Preset, b.PresetID)
		require.Equal(t, plan.VisualEvents[i].AssetID, b.AssetID)
		require.Equal(t, VisualIntentResolverVersion, b.ResolverVersion)
		require.Equal(t, DeterministicPresetSamplerVersion, b.SamplerVersion)
		require.NoError(t, b.Validate())
	}
}

// TestInMemoryVisualBindingsStoreRoundTrip pins save-then-list determinism and
// upsert-by-event-id (replace whole set) semantics.
func TestInMemoryVisualBindingsStoreRoundTrip(t *testing.T) {
	store := NewInMemoryVisualBindingsStore()
	bindings := BuildVisualBindings(7, testPlan(t), VisualIntentResolverVersion, DeterministicPresetSamplerVersion)

	require.NoError(t, store.SaveBindings(context.Background(), 7, bindings))
	got, err := store.ListBindings(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, got, len(bindings))
	// Deterministic play order: start_us then event id.
	for i := 1; i < len(got); i++ {
		require.True(t, got[i-1].StartUS <= got[i].StartUS)
	}

	// Replace whole set: a smaller re-save must not leave stale rows.
	require.NoError(t, store.SaveBindings(context.Background(), 7, bindings[:1]))
	got, err = store.ListBindings(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

// TestInMemoryVisualBindingsStore_RejectsInvalid pins fail-closed writes.
func TestInMemoryVisualBindingsStore_RejectsInvalid(t *testing.T) {
	store := NewInMemoryVisualBindingsStore()
	bad := VisualBinding{ScriptID: 1, VisualEventID: "ev", SemanticID: "sem", PresetFamily: "money", StartUS: 0, DurationUS: 1000}
	bad.ScriptID = 2 // mismatched with the save call's script id
	require.Error(t, store.SaveBindings(context.Background(), 1, []VisualBinding{bad}))
}

func TestVisualBinding_Validate(t *testing.T) {
	base := VisualBinding{ScriptID: 1, VisualEventID: "ev", SemanticID: "sem", PresetFamily: "money", StartUS: 0, DurationUS: 1000}
	require.NoError(t, base.Validate())

	for name, mutate := range map[string]func(*VisualBinding){
		"negative script": func(b *VisualBinding) { b.ScriptID = -1 },
		"empty event_id":  func(b *VisualBinding) { b.VisualEventID = "" },
		"empty semantic":  func(b *VisualBinding) { b.SemanticID = "" },
		"empty family":    func(b *VisualBinding) { b.PresetFamily = "" },
		"negative start":  func(b *VisualBinding) { b.StartUS = -1 },
		"zero duration":   func(b *VisualBinding) { b.DurationUS = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			b := base
			mutate(&b)
			require.ErrorIs(t, b.Validate(), ErrInvalidVisualBinding)
		})
	}
}
