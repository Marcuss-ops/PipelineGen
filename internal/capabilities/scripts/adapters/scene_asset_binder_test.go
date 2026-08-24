// Package adapters — scene_asset_binder_test.go: TDD tests locking
// the adapter-layer behaviour of SceneAssetBinder.BindClips.
//
// The binder itself lives at internal/application/scripts/scene/
// binder.go (canonical); these tests pin the adapter-layer
// observation surface (the canonical user of BindClips is
// ClipBindingsProcessor).
package adapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// makeBinderScenes is a small helper that builds N scenes with
// stable IDs ("scene-0".."scene-N-1"). Each scene starts with NO
// clip binding (the binder/processor must populate them).
func makeBinderScenes(n int) []scriptpkg.SpecScene {
	scenes := make([]scriptpkg.SpecScene, n)
	for i := range scenes {
		scenes[i] = scriptpkg.SpecScene{
			ID:    "scene-" + string(rune('0'+i)),
			Index: i,
			Text:  "Scene text",
			Kind:  scriptpkg.SceneClip,
		}
	}
	return scenes
}

// TestSceneAssetBinder_BindClips_OneToOnePreservesOrder pins the
// canonical 1:1 binding contract: when the processor has N clips
// and N scenes, each scene[i] gets exactly clipIDs[i] in arrival
// order.
func TestSceneAssetBinder_BindClips_OneToOnePreservesOrder(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	clipIDs := []string{"first-clip", "second-clip", "third-clip"}
	driveLinks := map[string]string{
		"first-clip":  "https://drive.google.com/first",
		"second-clip": "https://drive.google.com/second",
		"third-clip":  "https://drive.google.com/third",
	}

	reqs := make([]scene.ClipBindingRequest, 0, len(clipIDs))
	for i, id := range clipIDs {
		reqs = append(reqs, scene.ClipBindingRequest{
			SceneID: "scene-" + string(rune('0'+i)),
			Candidates: []scene.ClipCandidate{
				{ClipID: id, DriveLink: driveLinks[id]},
			},
		})
	}

	res := b.BindClips(reqs)

	require.Equal(t, true, res.Changed, "1:1 binding must report Changed=true")
	for i, want := range clipIDs {
		sceneID := "scene-" + string(rune('0'+i))
		binding, ok := res.Bindings[sceneID]
		require.True(t, ok, "expected binding for %s", sceneID)
		assert.Equal(t, want, binding.ClipID)
		assert.Equal(t, driveLinks[want], binding.DriveLink)
	}
}

// TestSceneAssetBinder_BindClips_RespectsNumClips pins the
// NumClips contract at the adapter layer: only the first N clips
// bind; scenes beyond that get no binding.
func TestSceneAssetBinder_BindClips_RespectsNumClips(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	clipIDs := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	driveLinks := map[string]string{
		"alpha":   "https://drive.google.com/alpha",
		"beta":    "https://drive.google.com/beta",
		"gamma":   "https://drive.google.com/gamma",
		"delta":   "https://drive.google.com/delta",
		"epsilon": "https://drive.google.com/epsilon",
	}
	const numClips = 2

	reqs := make([]scene.ClipBindingRequest, 0, 5)
	for i := 0; i < 5; i++ {
		var candidates []scene.ClipCandidate
		if i < numClips {
			candidates = []scene.ClipCandidate{{ClipID: clipIDs[i], DriveLink: driveLinks[clipIDs[i]]}}
		}
		reqs = append(reqs, scene.ClipBindingRequest{
			SceneID:    "scene-" + string(rune('0'+i)),
			Candidates: candidates,
		})
	}

	res := b.BindClips(reqs)

	require.Equal(t, true, res.Changed, "NumClips-respecting binding must report Changed=true")
	for i := 0; i < numClips; i++ {
		sceneID := "scene-" + string(rune('0'+i))
		binding, ok := res.Bindings[sceneID]
		require.True(t, ok, "expected binding for %s", sceneID)
		assert.Equal(t, clipIDs[i], binding.ClipID)
		assert.Equal(t, driveLinks[clipIDs[i]], binding.DriveLink)
	}
	for i := numClips; i < 5; i++ {
		sceneID := "scene-" + string(rune('0'+i))
		_, ok := res.Bindings[sceneID]
		assert.False(t, ok, "scene[%d] must not have a binding", i)
	}
}

// TestSceneAssetBinder_BindClips_ClearsExtraStaleClipBindings
// pins the P0 #2 invariant: when scenes > clips, the processor
// clears the stale Bindings.Clip for the unbound scenes.
func TestSceneAssetBinder_BindClips_ClearsExtraStaleClipBindings(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	clipIDs := []string{"clip-a", "clip-b"}
	driveLinks := map[string]string{
		"clip-a": "https://drive.google.com/a",
		"clip-b": "https://drive.google.com/b",
	}

	reqs := make([]scene.ClipBindingRequest, 0, 5)
	for i := 0; i < 5; i++ {
		var candidates []scene.ClipCandidate
		if i < len(clipIDs) {
			candidates = []scene.ClipCandidate{{ClipID: clipIDs[i], DriveLink: driveLinks[clipIDs[i]]}}
		}
		reqs = append(reqs, scene.ClipBindingRequest{
			SceneID:    "scene-" + string(rune('0'+i)),
			Candidates: candidates,
		})
	}

	res := b.BindClips(reqs)

	require.Equal(t, true, res.Changed)
	for i := 0; i < 2; i++ {
		sceneID := "scene-" + string(rune('0'+i))
		binding, ok := res.Bindings[sceneID]
		require.True(t, ok)
		assert.Equal(t, clipIDs[i], binding.ClipID)
	}
	for i := 2; i < 5; i++ {
		sceneID := "scene-" + string(rune('0'+i))
		_, ok := res.Bindings[sceneID]
		assert.False(t, ok, "scene[%d] must not have a binding", i)
	}
}

// TestClipBindingsProcessor_ClearsStaleBindings pins the P0 #2
// invariant at the processor layer: scenes beyond the clip count
// get their Bindings.Clip explicitly nil-ed.
func TestClipBindingsProcessor_ClearsStaleBindings(t *testing.T) {
	t.Parallel()
	p := NewClipBindingsProcessor(zap.NewNop())

	scenes := makeBinderScenes(5)
	// Pre-populate scenes 2-4 with stale bindings.
	for i := 2; i < 5; i++ {
		scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    "stale-" + string(rune('0'+i-2)),
			DriveLink: "https://drive.google.com/stale-" + string(rune('0'+i-2)),
		}
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b"},
			DriveLinks: map[string]string{
				"clip-a": "https://drive.google.com/a",
				"clip-b": "https://drive.google.com/b",
			},
		},
	}

	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: scenes}}
	res, err := p.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.True(t, res.Changed)

	// First two scenes get the canonical clips.
	require.NotNil(t, scenes[0].Bindings.Clip)
	require.NotNil(t, scenes[1].Bindings.Clip)
	assert.Equal(t, "clip-a", scenes[0].Bindings.Clip.ClipID)
	assert.Equal(t, "clip-b", scenes[1].Bindings.Clip.ClipID)

	// Scenes 2-4 had stale bindings pre-call; processor must clear them.
	for i := 2; i < 5; i++ {
		assert.Nil(t, scenes[i].Bindings.Clip,
			"scene[%d].Bindings.Clip must be nil after processor (P0 #2: stale binding cleared)", i)
	}
}
