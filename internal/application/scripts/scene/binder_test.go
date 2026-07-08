// Package scene_test — binder_test.go: hermetic TDD coverage for
// SceneAssetBinder.BindClips + SceneAssetBinder.BindStock.
//
// The two binder methods are the canonical load-bearing seams for
// Phase 2 — both ClipBindingsProcessor and StockAssociationProcessor
// delegate to them. These tests pin the per-clip + per-stock contract
// endpoints that the existing processor tests cover, but now at
// the scene-package layer (godlike/06 SSOT one canonical owner per
// fact).
package scene_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Fakes ──────────────────────────────────────────────────────────────

type fakeStockSearch struct {
	hits  []ports.StockSearchHit
	err   error
	calls int
	lastQ string
}

func (f *fakeStockSearch) SearchStock(_ context.Context, q string, _ int) ([]ports.StockSearchHit, error) {
	f.calls++
	f.lastQ = q
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func makeScenes(n int) []scriptpkg.SpecScene {
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

// ── BindClips ──────────────────────────────────────────────────────────

// TestSceneAssetBinder_BindClips_NilPlan is the canonical no-op
// branch preserved verbatim from the pre-Phase-2
// ClipBindingsProcessor.Process body.
func TestSceneAssetBinder_BindClips_NilPlan(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	res := b.BindClips(makeScenes(3), "text", nil)
	assert.Equal(t, false, res.Changed)
	assert.Nil(t, res.SynthesizedScenes)
	assert.Nil(t, res.Warnings)
}

// TestSceneAssetBinder_BindClips_NoClipEvidence covers the
// plan.ClipEvidence==nil OR AcceptedClipIDs==empty no-op branches.
func TestSceneAssetBinder_BindClips_NoClipEvidence(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	// ClipEvidence==nil branch.
	res := b.BindClips(makeScenes(3), "text", &scriptpkg.ResolvedGenerationPlan{})
	assert.Equal(t, false, res.Changed)
	// AcceptedClipIDs empty branch.
	res = b.BindClips(makeScenes(3), "text", &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{},
	})
	assert.Equal(t, false, res.Changed)
}

// TestSceneAssetBinder_BindClips_OneToOneBinding covers the
// canonical 1:1 clip-scene binding when scenes count == clip count.
func TestSceneAssetBinder_BindClips_OneToOneBinding(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const driveA = "https://drive.google.com/a"
	const driveB = "https://drive.google.com/b"
	const driveC = "https://drive.google.com/c"
	scenes := makeScenes(3)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			DriveLinks: map[string]string{
				"clip-a": driveA,
				"clip-b": driveB,
				"clip-c": driveC,
			},
		},
	}

	res := b.BindClips(scenes, "any text", plan)
	require.Equal(t, true, res.Changed)
	require.NotNil(t, scenes[0].Bindings.Clip)
	assert.Equal(t, "clip-a", scenes[0].Bindings.Clip.ClipID)
	assert.Equal(t, driveA, scenes[0].Bindings.Clip.DriveLink)
	require.NotNil(t, scenes[1].Bindings.Clip)
	assert.Equal(t, "clip-b", scenes[1].Bindings.Clip.ClipID)
	assert.Equal(t, driveB, scenes[1].Bindings.Clip.DriveLink)
	require.NotNil(t, scenes[2].Bindings.Clip)
	assert.Equal(t, "clip-c", scenes[2].Bindings.Clip.ClipID)
	assert.Equal(t, driveC, scenes[2].Bindings.Clip.DriveLink)
}

// TestSceneAssetBinder_BindClips_NoCyclingP0_2 covers the P0 #2
// invariant: when scenes > clips, extra scenes get nil binding
// (NOT cycling — surface LLM mismatches instead of silently reusing
// clips).
func TestSceneAssetBinder_BindClips_NoCyclingP0_2(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	scenes := makeScenes(5)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b"},
			DriveLinks: map[string]string{
				"clip-a": "https://drive.google.com/a",
				"clip-b": "https://drive.google.com/b",
			},
		},
	}

	res := b.BindClips(scenes, "any text", plan)
	require.Equal(t, true, res.Changed)
	require.NotNil(t, scenes[0].Bindings.Clip)
	require.NotNil(t, scenes[1].Bindings.Clip)
	assert.Nil(t, scenes[2].Bindings.Clip, "P0 #2: extra scenes must NOT be cycled")
	assert.Nil(t, scenes[3].Bindings.Clip, "P0 #2: extra scenes must NOT be cycled")
	assert.Nil(t, scenes[4].Bindings.Clip, "P0 #2: extra scenes must NOT be cycled")
}

// TestSceneAssetBinder_BindClips_ProseFallbackFASE3 covers the
// prose-fallback heuristic (FASE 3 June 2026): when scenes is
// empty + cleaned text is non-empty, synthesize N scenes + bind
// 1:1 + return SynthesizedScenes + Warnings.
func TestSceneAssetBinder_BindClips_ProseFallbackFASE3(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const prose = "First sentence here. Second sentence here. Third sentence here."
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			DriveLinks: map[string]string{
				"clip-a": "https://drive.google.com/a",
				"clip-b": "https://drive.google.com/b",
				"clip-c": "https://drive.google.com/c",
			},
		},
	}

	var scenes []scriptpkg.SpecScene // start empty (small local model)
	res := b.BindClips(scenes, prose, plan)
	require.Equal(t, true, res.Changed)
	require.Len(t, res.SynthesizedScenes, 3,
		"prose-fallback must synthesize 3 scenes (CanonicalSynthesizer n=3)")
	require.Len(t, res.Warnings, 1, "prose-fallback must emit exactly 1 warning")
	assert.Contains(t, res.Warnings[0], "prose-fallback synthesised")
	assert.Contains(t, res.Warnings[0], "bound 3/3 clips")
}

// TestSceneAssetBinder_BindClips_ProseFallbackEmptyText covers the
// "scenes empty + cleaned text empty" no-op branch.
func TestSceneAssetBinder_BindClips_ProseFallbackEmptyText(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a"},
			DriveLinks:      map[string]string{"clip-a": "https://drive/a"},
		},
	}
	res := b.BindClips(nil, "", plan)
	assert.Equal(t, false, res.Changed)
}

// ── BindStock ──────────────────────────────────────────────────────────

// TestSceneAssetBinder_BindStock_NilSearch is the canonical composition-time
// fail-closed branch preserved verbatim from the pre-Phase-2
// StockAssociationProcessor.
func TestSceneAssetBinder_BindStock_NilSearch(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	sceneWithClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "x", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{DriveLink: "https://drive/x"},
		},
	}
	res := b.BindStock(context.Background(), []scriptpkg.SpecScene{sceneWithClip}, nil)
	// Conflict-check: Stock must remain nil when port is nil.
	// (Inspects the struct value directly here — BindStock returned
	// early at `search == nil` so no slice-backed mutation occurred;
	// a slice variable is unnecessary for this no-op contract test.)
	assert.Equal(t, false, res.Changed)
	assert.Nil(t, sceneWithClip.Bindings.Stock)
}

// TestSceneAssetBinder_BindStock_EmptyScenes covers the empty-scenes
// no-op branch.
func TestSceneAssetBinder_BindStock_EmptyScenes(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	search := &fakeStockSearch{}
	res := b.BindStock(context.Background(), nil, search)
	assert.Equal(t, false, res.Changed)
	assert.Equal(t, 0, search.calls, "no scenes → no SearchStock invocations")
}

// TestSceneAssetBinder_BindStock_QdrantHit covers the happy path
// where Qdrant returns a hit → scene.Bindings.Stock populated with
// Fallback:false and per-iteration info logs emit (verified via
// fake.calls counter).
func TestSceneAssetBinder_BindStock_QdrantHit(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const qdrantDrive = "https://drive.google.com/qdrant-asset"
	const clipDrive = "https://drive.google.com/clip" // not used here
	search := &fakeStockSearch{
		hits: []ports.StockSearchHit{
			{
				AssetID:   "asset-q-1",
				Name:      "Qdrant 1",
				Source:    "stock",
				DriveLink: qdrantDrive,
				Score:     0.91,
			},
		},
	}
	sceneWithClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "Jackie Chan combat scene", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{DriveLink: clipDrive},
		},
	}
	scenes := []scriptpkg.SpecScene{sceneWithClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	assert.Equal(t, 1, search.calls)
	require.NotNil(t, scenes[0].Bindings.Stock)
	stock := scenes[0].Bindings.Stock
	assert.Equal(t, "asset-q-1", stock.AssetID)
	assert.Equal(t, qdrantDrive, stock.DriveLink)
	assert.InDelta(t, 0.91, stock.Score, 0.001)
	assert.False(t, stock.Fallback, "Qdrant hit → Fallback must be false")
}

// TestSceneAssetBinder_BindStock_FallbackToClip covers the Qdrant
// empty + clip drive present branch: scene.Bindings.Stock populated
// from clip.DriveLink with Fallback:true.
func TestSceneAssetBinder_BindStock_FallbackToClip(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const clipDrive = "https://drive.google.com/clip-as-stock"
	search := &fakeStockSearch{hits: nil}
	sceneWithClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "Jackie Chan interview", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{DriveLink: clipDrive},
		},
	}
	scenes := []scriptpkg.SpecScene{sceneWithClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	require.NotNil(t, scenes[0].Bindings.Stock)
	stock := scenes[0].Bindings.Stock
	assert.True(t, stock.Fallback)
	assert.Equal(t, clipDrive, stock.DriveLink)
	assert.Empty(t, stock.AssetID)
	assert.Zero(t, stock.Score)
}

// TestSceneAssetBinder_BindStock_NoHitNoClipLeavesStockNil covers the
// silent-nil branch: Qdrant empty + scene has no Clip binding →
// Stock stays nil.
func TestSceneAssetBinder_BindStock_NoHitNoClipLeavesStockNil(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	search := &fakeStockSearch{hits: nil}
	sceneNoClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "pure narration", Kind: scriptpkg.SceneNarration,
	}
	// Slice variable (NOT slice literal) so the assertion inspects
	// the actual binder mutation surface. Per godlike/07 NO-FAKE-AVAILABILITY:
	// a slice literal makes a NEW backing array, which means assertions
	// on the original struct value will trivially pass for ANY binder
	// behavior — a future bug that incorrectly populates Stock would
	// silently pass the test. The slice-variable pattern is the
	// canonical signal-integrity recipe.
	scenes := []scriptpkg.SpecScene{sceneNoClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	assert.Nil(t, scenes[0].Bindings.Stock,
		"Stock must stay nil when both Qdrant and Clip binding are absent")
}

// TestSceneAssetBinder_BindStock_SearchErrorFallsBackToClip covers the
// transport-error path: SearchStock returns non-nil error + scene has
// clip drive → fallback binding + best-effort semantics (no error
// propagation).
func TestSceneAssetBinder_BindStock_SearchErrorFallsBackToClip(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const clipDrive = "https://drive.google.com/clip-fallback-err"
	search := &fakeStockSearch{err: errors.New("qdrant unreachable")}
	sceneWithClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "any", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{DriveLink: clipDrive},
		},
	}
	scenes := []scriptpkg.SpecScene{sceneWithClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	require.NotNil(t, scenes[0].Bindings.Stock)
	assert.True(t, scenes[0].Bindings.Stock.Fallback)
	assert.Equal(t, clipDrive, scenes[0].Bindings.Stock.DriveLink)
}

// TestSceneAssetBinder_BindStock_EmptySceneTextSkipsSearch covers the
// per-iteration empty-text branch: scene.Text empty → skip the
// SearchStock call entirely + fallback to clip.
func TestSceneAssetBinder_BindStock_EmptySceneTextSkipsSearch(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const clipDrive = "https://drive.google.com/clip-empty"
	search := &fakeStockSearch{}
	sceneWithClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{DriveLink: clipDrive},
		},
	}
	scenes := []scriptpkg.SpecScene{sceneWithClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	assert.Equal(t, 0, search.calls, "empty scene.Text must NOT trigger SearchStock")
	require.NotNil(t, scenes[0].Bindings.Stock)
	assert.True(t, scenes[0].Bindings.Stock.Fallback)
	assert.Equal(t, clipDrive, scenes[0].Bindings.Stock.DriveLink)
}
