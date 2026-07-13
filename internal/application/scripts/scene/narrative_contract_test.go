// Package scene_test — narrative_contract_test.go: contract tests for
// the scene planner (owns text) and binding resolver (must not mutate
// text).
//
// ARCHITECTURE REFACTOR (July 2026):
//   - ScenePlanner: owns scene.Text. It splits narrative prose into
//     scenes. The binder NEVER modifies scene.Text.
//   - BindingResolver/Binder: binding-only. Attaches ClipBinding to
//     scenes but never touches Text, Title, Kind, or order.
//
// These tests verify the full contract at the scene level.
package scene_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Test 4: ScenePlanner owns scene text ────────────────────────────

// TestScenePlanner_OwnsSceneText verifies that the scene planner
// (synthesizer) splits narrative text into scenes deterministically.
// Each scene.Text is a substring of the input, no extra content is
// injected, and no metadata leaks in.
func TestScenePlanner_OwnsSceneText(t *testing.T) {
	t.Parallel()

	input := "Fin dai primi secondi, Pacquiao mostra una grande mobilità. Nel settimo round aumenta la pressione e scuote Broner."

	synthesizer := scene.NewSceneSynthesizer()
	scenes := synthesizer.FromProse(input, 2)

	require.Len(t, scenes, 2)
	require.Equal(
		t,
		"Fin dai primi secondi, Pacquiao mostra una grande mobilità.",
		scenes[0].Text,
	)
	require.Equal(
		t,
		"Nel settimo round aumenta la pressione e scuote Broner.",
		scenes[1].Text,
	)

	// Scene texts are substrings of the input — no content fabricated.
	require.Contains(t, input, scenes[0].Text)
	require.Contains(t, input, scenes[1].Text)

	// No infra data in scene text.
	allText := scenes[0].Text + " " + scenes[1].Text
	require.NotContains(t, allText, "clip_id")
	require.NotContains(t, allText, "drive.google.com")
	require.NotContains(t, allText, "youtube.com")
	require.NotContains(t, allText, "Tags:")
}

// ── Test 5: Binder purity + dirty clip metadata ─────────────────────

// TestBindingResolver_DoesNotMutateSceneText is the canonical purity
// test: the binder MUST NOT modify scene.Text, scene.Title, scene.Kind,
// or scene order. It can only attach bindings.
func TestBindingResolver_DoesNotMutateSceneText(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "Fin dai primi secondi, Pacquiao mostra una grande mobilità.", Title: "Intro", Kind: scriptpkg.SceneClip},
		{ID: "scene-1", Index: 1, Text: "Nel settimo round aumenta la pressione e scuote Broner.", Title: "Climax", Kind: scriptpkg.SceneClip},
	}

	// Clone before binding.
	before := cloneScenes(scenes)

	manifest := scriptpkg.BindingManifest{
		Slots: []scriptpkg.BindingSlot{
			{Slot: "slot-1", ClipID: "clip-a", DriveLink: "https://drive/a"},
			{Slot: "slot-2", ClipID: "clip-b", DriveLink: "https://drive/b"},
		},
	}

	res := b.BindClipsFromManifest(scenes, manifest)
	require.True(t, res.Changed)

	// Text, Title, Kind, order unchanged.
	require.Equal(t, before[0].Text, scenes[0].Text)
	require.Equal(t, before[1].Text, scenes[1].Text)
	require.Equal(t, before[0].Title, scenes[0].Title)
	require.Equal(t, before[1].Title, scenes[1].Title)
	require.Equal(t, before[0].Kind, scenes[0].Kind)
	require.Equal(t, before[1].Kind, scenes[1].Kind)
	require.Equal(t, before[0].Index, scenes[0].Index)
	require.Equal(t, before[1].Index, scenes[1].Index)

	// But bindings ARE populated.
	require.NotNil(t, scenes[0].Bindings.Clip)
	require.Equal(t, "clip-a", scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "https://drive/a", scenes[0].Bindings.Clip.DriveLink)
	require.NotNil(t, scenes[1].Bindings.Clip)
	require.Equal(t, "clip-b", scenes[1].Bindings.Clip.ClipID)
}

// TestBindingResolver_DirtyClipMetadataNeverLeaksIntoText verifies
// that dirty clip metadata (YouTube URLs, Drive links, clip IDs,
// tags, speaker labels) NEVER appears in scene.Text — the binder
// only writes to scene.Bindings.Clip.
func TestBindingResolver_DirtyClipMetadataNeverLeaksIntoText(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{
			ID: "scene-0", Index: 0,
			Text:  "Pacquiao comincia il combattimento con grande mobilità.",
			Title: "Opening", Kind: scriptpkg.SceneClip,
		},
	}

	dirtyPlan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"yt_RRJvrDKunyA_32_37_v1"},
			DriveLinks: map[string]string{
				"yt_RRJvrDKunyA_32_37_v1": "https://drive.google.com/file/test",
			},
		},
	}

	// Clone before.
	textBefore := scenes[0].Text

	res := b.BindClips(scenes, dirtyPlan)
	require.True(t, res.Changed)

	// Scene text MUST NOT contain any infra data.
	require.Equal(t, textBefore, scenes[0].Text)
	require.NotContains(t, scenes[0].Text, "yt_RRJvrDKunyA")
	require.NotContains(t, scenes[0].Text, "drive.google.com")
	require.NotContains(t, scenes[0].Text, "youtube.com")
	require.NotContains(t, scenes[0].Text, "commentator")
	require.NotContains(t, scenes[0].Text, "opening round footwork")

	// But binding MUST carry the infra data.
	require.NotNil(t, scenes[0].Bindings.Clip)
	require.Equal(t, "yt_RRJvrDKunyA_32_37_v1", scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "https://drive.google.com/file/test", scenes[0].Bindings.Clip.DriveLink)
}

// TestBindingResolver_DirtyClipMetadataNeverLeaksIntoText_FromManifest
// verifies the same contract through the manifest-based binding path.
func TestBindingResolver_DirtyClipMetadataNeverLeaksIntoText_FromManifest(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{
			ID: "scene-0", Index: 0,
			Text:  "Pacquiao comincia il combattimento con grande mobilità.",
			Title: "Opening", Kind: scriptpkg.SceneClip,
		},
		{
			ID: "scene-1", Index: 1,
			Text:  "Nel settimo round aumenta la pressione e scuote Broner.",
			Title: "Climax", Kind: scriptpkg.SceneClip,
		},
	}

	manifest := scriptpkg.BindingManifest{
		Slots: []scriptpkg.BindingSlot{
			{
				Slot:      "slot-1",
				ClipID:    "yt_RRJvrDKunyA_32_37_v1",
				ClipTitle: "opening round footwork jab",
				DriveLink: "https://drive.google.com/file/test",
				StartMs:   32000,
				EndMs:     37000,
			},
			{
				Slot:      "slot-2",
				ClipID:    "clip-round7-id",
				ClipTitle: "round seven pressure",
				DriveLink: "https://drive.google.com/file/test2",
				StartMs:   90000,
				EndMs:     95000,
			},
		},
	}

	beforeTexts := []string{scenes[0].Text, scenes[1].Text}
	res := b.BindClipsFromManifest(scenes, manifest)
	require.True(t, res.Changed)

	// Texts unchanged.
	require.Equal(t, beforeTexts[0], scenes[0].Text)
	require.Equal(t, beforeTexts[1], scenes[1].Text)

	// No infra data in text.
	for _, s := range scenes {
		require.NotContains(t, s.Text, "yt_RRJvrDKunyA")
		require.NotContains(t, s.Text, "drive.google.com")
		require.NotContains(t, s.Text, "clip-round7")
	}

	// Bindings carry infra data.
	require.Equal(t, "yt_RRJvrDKunyA_32_37_v1", scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "https://drive.google.com/file/test", scenes[0].Bindings.Clip.DriveLink)
	require.Equal(t, int64(32000), scenes[0].Bindings.Clip.StartMs)
	require.Equal(t, "clip-round7-id", scenes[1].Bindings.Clip.ClipID)
}

// TestBindingResolver_CloneNotAffected verifies binding does not
// affect the original clone when scenes are deep-copied.
func TestBindingResolver_CloneNotAffected(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	original := []scriptpkg.SpecScene{
		{ID: "s1", Index: 0, Text: "Original A", Kind: scriptpkg.SceneNarration},
		{ID: "s2", Index: 1, Text: "Original B", Kind: scriptpkg.SceneNarration},
	}

	// Deep clone.
	snapshot := cloneScenes(original)

	// Bind to original.
	res := b.BindClips(original, &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-x", "clip-y"},
			DriveLinks:      map[string]string{"clip-x": "https://drive/x", "clip-y": "https://drive/y"},
		},
	})
	require.True(t, res.Changed)

	// Snapshot must be unaffected.
	require.Equal(t, "Original A", snapshot[0].Text)
	require.Equal(t, "Original B", snapshot[1].Text)
	require.Nil(t, snapshot[0].Bindings.Clip)
	require.Nil(t, snapshot[1].Bindings.Clip)
}

// ── Section 14: binder preserves narrative text ───────────────────

// TestSceneAssetBinderPreservesNarrativeText is the canonical unit
// test: binding must never mutate narrative text. The test does not
// search for URLs or specific words — it protects the general rule.
func TestSceneAssetBinderPreservesNarrativeText(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	original := []scriptpkg.SpecScene{
		{Index: 0, Text: "Pacquiao controls the distance with speed and timing.", Kind: scriptpkg.SceneClip},
		{Index: 1, Text: "He later increases the pressure and forces Broner to defend.", Kind: scriptpkg.SceneClip},
	}

	before := []string{original[0].Text, original[1].Text}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b"},
			DriveLinks:      map[string]string{"clip-a": "https://drive/a", "clip-b": "https://drive/b"},
		},
	}

	res := b.BindClips(original, plan)
	require.True(t, res.Changed)

	require.Equal(t, before[0], original[0].Text)
	require.Equal(t, before[1], original[1].Text)
	require.Equal(t, "clip-a", original[0].Bindings.Clip.ClipID)
	require.Equal(t, "clip-b", original[1].Bindings.Clip.ClipID)
}

// ── Section 15: dirty clip metadata never becomes narration ───────

// TestDirtyClipMetadataNeverBecomesNarration verifies that even
// deliberately dirty clip metadata (YouTube URLs, clip IDs, tags,
// commentator labels, Drive links) NEVER appears in scene.Text.
func TestDirtyClipMetadataNeverBecomesNarration(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{
			Text: "Pacquiao opens the fight with quick movement and a sharp jab.",
			Kind: scriptpkg.SceneClip,
		},
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"yt_RRJvrDKunyA_32_37_v1"},
			DriveLinks: map[string]string{
				"yt_RRJvrDKunyA_32_37_v1": "https://drive.google.com/file/test",
			},
		},
	}

	before := scenes[0].Text
	res := b.BindClips(scenes, plan)
	require.True(t, res.Changed)

	require.Equal(t, before, scenes[0].Text)
	require.Equal(t, "https://drive.google.com/file/test", scenes[0].Bindings.Clip.DriveLink)

	// Scene text must NOT contain any dirty metadata.
	require.NotContains(t, scenes[0].Text, "yt_RRJvrDKunyA")
	require.NotContains(t, scenes[0].Text, "drive.google.com")
	require.NotContains(t, scenes[0].Text, "youtube.com")
	require.NotContains(t, scenes[0].Text, "commentator")
	require.NotContains(t, scenes[0].Text, "boxing highlights")
}

// ── Helpers ─────────────────────────────────────────────────────────

func cloneScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, len(scenes))
	for i := range scenes {
		out[i] = scenes[i]
	}
	return out
}
