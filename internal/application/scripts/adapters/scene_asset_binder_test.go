// Package adapters_test — scene_asset_binder_test.go: 3 NEW TDD
// tests (pasted plan §5) locking the EXISTING behavior of
// SceneAssetBinder.BindClips at the adapter layer (mirrors
// scene/binder_test.go's coverage but at the canonical test
// surface for adapter consumers).
//
// The 3 tests focus on the three production contract endpoints:
//   - 1:1 clip-scene binding with order preservation (clip[0] →
//     scene[0], clip[1] → scene[1], …) — no shuffling, no sorting.
//   - NumClips option honored (when set, only the first N clips
//     bind; scenes beyond that get no binding).
//   - P0 #2 invariant: extra scenes beyond the (clipped) clip
//     count get their stale Bindings.Clip nil-ed out so LLM
//     mismatches are observable, not silently absorbed.
//
// godlike/06 SSOT (one canonical owner per fact): the production
// BindClips lives ONLY at internal/application/scripts/scene/
// binder.go (canonical); these tests pin the adapter-layer
// observation surface (the canonical user of BindClips is
// ClipBindingsProcessor, which lives in this package).
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion inspects the
// actual slice backing array (slice-variable pattern, NOT slice
// literal) so a future refactor that incorrectly mutates or fails
// to mutate Bindings.Clip surfaces as test failure, not silent
// pass.
package adapters_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// makeBinderScenes is a small helper that builds N scenes with
// a SceneClip kind and stable IDs ("scene-0".."scene-N-1"). Each
// scene starts with NO clip binding (the binder must populate
// them on the assignment loop).
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
// canonical 1:1 binding contract: when the plan has N clips and
// the caller supplies N scenes, each scene[i] gets exactly
// clipIDs[i] in arrival order — no shuffle, no sort, no
// modulo cycling. Each binding must reference the correct
// DriveLink for its clip (PR 6 canonical keying).
func TestSceneAssetBinder_BindClips_OneToOnePreservesOrder(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	// Deliberately non-alphabetical clip IDs to make the
	// order-preservation invariant observable: a future bug that
	// did e.g. `sort.Strings(clipIDs)` would surface as test
	// failure (scene[0] would bind to "third-clip" alphabetically).
	clipIDs := []string{"first-clip", "second-clip", "third-clip"}
	driveLinks := map[string]string{
		"first-clip":  "https://drive.google.com/first",
		"second-clip": "https://drive.google.com/second",
		"third-clip":  "https://drive.google.com/third",
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: clipIDs,
			DriveLinks:      driveLinks,
		},
	}

	scenes := makeBinderScenes(3)
	res := b.BindClips(scenes, "any text", plan)

	require.Equal(t, true, res.Changed, "1:1 binding must report Changed=true")
	for i, want := range clipIDs {
		require.NotNil(t, scenes[i].Bindings.Clip,
			"scene[%d].Bindings.Clip = nil (expected binding to %q)", i, want)
		assert.Equal(t, want, scenes[i].Bindings.Clip.ClipID,
			"scene[%d].ClipID must equal clipIDs[%d] in arrival order", i, i)
		assert.Equal(t, driveLinks[want], scenes[i].Bindings.Clip.DriveLink,
			"scene[%d].DriveLink must match the canonical DriveLinks key %q", i, want)
	}
}

// TestSceneAssetBinder_BindClips_RespectsNumClips pins the
// NumClips contract: when plan.NumClips > 0 AND
// plan.NumClips < len(AcceptedClipIDs), the binder uses the
// FIRST NumClips entries of AcceptedClipIDs (NOT a random
// subset, NOT a sorted subset). The truncated count is the
// binding ceiling; scenes beyond that get no binding (P0 #2).
func TestSceneAssetBinder_BindClips_RespectsNumClips(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	// 5 clips available, NumClips=2 → bind first 2 only.
	clipIDs := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	driveLinks := map[string]string{
		"alpha":   "https://drive.google.com/alpha",
		"beta":    "https://drive.google.com/beta",
		"gamma":   "https://drive.google.com/gamma",
		"delta":   "https://drive.google.com/delta",
		"epsilon": "https://drive.google.com/epsilon",
	}
	const numClips = 2
	plan := &scriptpkg.ResolvedGenerationPlan{
		NumClips: numClips,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: clipIDs,
			DriveLinks:      driveLinks,
		},
	}

	scenes := makeBinderScenes(5)
	res := b.BindClips(scenes, "any text", plan)

	require.Equal(t, true, res.Changed, "NumClips-respecting binding must report Changed=true")
	// First numClips scenes get the first numClips entries of
	// AcceptedClipIDs in order.
	for i := 0; i < numClips; i++ {
		require.NotNil(t, scenes[i].Bindings.Clip,
			"scene[%d].Bindings.Clip = nil (expected binding to %q under NumClips=%d)",
			i, clipIDs[i], numClips)
		assert.Equal(t, clipIDs[i], scenes[i].Bindings.Clip.ClipID,
			"scene[%d].ClipID must equal clipIDs[%d] (NOT a re-sorted subset)", i, i)
		assert.Equal(t, driveLinks[clipIDs[i]], scenes[i].Bindings.Clip.DriveLink)
	}
	// Scenes beyond numClips get NO binding (P0 #2 no-cycling).
	for i := numClips; i < len(scenes); i++ {
		assert.Nil(t, scenes[i].Bindings.Clip,
			"scene[%d].Bindings.Clip must be nil (P0 #2: no cycling beyond NumClips=%d)",
			i, numClips)
	}
}

// TestSceneAssetBinder_BindClips_ClearsExtraStaleClipBindings
// pins the P0 #2 invariant: when scenes > clips, the binder
// MUST nil out any pre-existing Bindings.Clip on the unbound
// scenes. This surfaces LLM output mismatches (e.g. an LLM
// emitted stale clip IDs in earlier pipeline stages; the
// binder clears them so the mismatch is visible to downstream
// consumers, NOT silently preserved).
//
// Pre-condition: scenes 2-4 carry stale (canonical clip IDs
// from a previous run) bindings; the binder must clear them
// because there are only 2 clips available.
func TestSceneAssetBinder_BindClips_ClearsExtraStaleClipBindings(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())

	clipIDs := []string{"clip-a", "clip-b"}
	driveLinks := map[string]string{
		"clip-a": "https://drive.google.com/a",
		"clip-b": "https://drive.google.com/b",
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: clipIDs,
			DriveLinks:      driveLinks,
		},
	}

	// 5 scenes; pre-populate scenes 2-4 with STALE clip
	// bindings (canonical IDs from a previous run that the
	// resolver dropped in the current run).
	scenes := makeBinderScenes(5)
	staleIDs := []string{"stale-x", "stale-y", "stale-z"}
	for i, staleID := range staleIDs {
		scenes[2+i].Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    staleID,
			DriveLink: "https://drive.google.com/" + staleID,
		}
	}

	res := b.BindClips(scenes, "any text", plan)

	require.Equal(t, true, res.Changed,
		"clearing stale bindings counts as a mutation → Changed=true")
	// First 2 scenes get the canonical clips.
	for i := 0; i < 2; i++ {
		require.NotNil(t, scenes[i].Bindings.Clip,
			"scene[%d].Bindings.Clip = nil (expected binding to %q)", i, clipIDs[i])
		assert.Equal(t, clipIDs[i], scenes[i].Bindings.Clip.ClipID)
	}
	// Scenes 2-4 had stale bindings pre-call; binder must clear
	// them. Asserting the post-call value is the load-bearing
	// observable — a future regression that forgets to nil
	// stale bindings would surface as test failure (P0 #2
	// invariant: extra scenes get nil binding, not the
	// pre-existing value).
	//
	// godlike/07 NO-FAKE-AVAILABILITY: do NOT access
	// scenes[i].Bindings.Clip.ClipID / .DriveLink in the
	// assertion's format args — testify evaluates format
	// args eagerly, so a nil binding would nil-deref and
	// crash the test instead of failing it cleanly. The
	// assert.Nil call itself reports file/line on failure;
	// a separate OnStaleRemaining diagnostic below is the
	// canonical escape hatch for the rare "binding survived
	// but the diagnostic would be useful" case.
	for i := 2; i < 5; i++ {
		assert.Nil(t, scenes[i].Bindings.Clip,
			"scene[%d].Bindings.Clip must be nil after binder (P0 #2: stale binding cleared)", i)
		if scenes[i].Bindings.Clip != nil {
			t.Errorf("diagnostic: scene[%d] retained stale binding ClipID=%q DriveLink=%q (P0 #2 regression)",
				i, scenes[i].Bindings.Clip.ClipID, scenes[i].Bindings.Clip.DriveLink)
		}
	}
}
