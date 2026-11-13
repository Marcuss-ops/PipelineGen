package scriptgeneration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestWholeScenePhraseTextRejectsLongNarration(t *testing.T) {
	atLimit := strings.TrimSpace(strings.Repeat("parola ", maxWholeScenePhraseWords))
	got, ok := wholeScenePhraseText("  " + atLimit + "  ")
	require.True(t, ok)
	require.Equal(t, atLimit, got)

	_, ok = wholeScenePhraseText(atLimit + " oltre")
	require.False(t, ok, "a phrase over the callout ceiling must be rejected")

	_, ok = wholeScenePhraseText("   ")
	require.False(t, ok)
}

// TestCompileOverlayPlanSkipsOverlongUntimedScenePhrase reproduces the live
// Floyd Mayweather failure: phrases are enabled, the scene has no certified
// word timing yet, and the whole 106-word narration was projected as one
// phrase card. The fallback must keep the scene's other candidates but never
// emit an unrenderable phrase.
func TestCompileOverlayPlanSkipsOverlongUntimedScenePhrase(t *testing.T) {
	result := phraseCeilingFixture(t)
	result.Scenes = append(result.Scenes, Scene{
		ID:         "long-untimed",
		Index:      1,
		DurationMS: 4000,
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryPhrases: scriptpkg.ToggleEnabled,
		},
		Text: map[Language]string{"en": strings.TrimSpace(strings.Repeat("parola ", 106))},
	})
	result.ResolvedScenes = append(result.ResolvedScenes, ResolvedScene{
		ID: "long-untimed", Index: 1, TimelineStartUS: 2_400_000, DurationUS: 4_000_000,
	})

	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-long-untimed", "video-long-untimed", "project-long-untimed")
	require.NoError(t, err)
	require.NotNil(t, plan)
	for _, item := range plan.Items {
		if item.SceneID == "long-untimed" {
			t.Fatalf("overlong untimed scene must not become a phrase card: kind=%s words=%d", item.Kind, len(strings.Fields(item.Text)))
		}
	}
}
