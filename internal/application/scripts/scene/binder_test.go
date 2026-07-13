// Package scene_test — binder_test.go: hermetic TDD coverage for
// SceneAssetBinder.BindClips + SceneAssetBinder.BindStock.
//
// The two binder methods are the canonical load-bearing seams for
// Phase 2 — both ClipBindingsProcessor and StockAssociationProcessor
// delegate to them. These tests pin the per-clip + per-stock
// contract endpoints.
//
// Wave 2.0 (July 2026): the binder is pure binding. The 3
// prose-fallback scenarios are DELETED from this file because the
// pure binder no longer synthesizes scenes from prose (the FASE 3
// heuristic walked back in Wave 2.0). The model emits
// scene.Text / .Title / .Kind directly; the binder only writes
// scene.Bindings.*.
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

// SearchAssets is the no-op stub that satisfies the embedded
// ports.AssetSearchPort interface (which ports.StockSearchPort
// embeds). The binder under test routes through the legacy
// SearchStock seam, so SearchAssets is not exercised — the no-op
// return preserves the existing test surface byte-for-byte.
//
// godlike/07 minimum-blast-radius: same pattern as the
// processor_stock_association_test.go::fakeStockSearch.SearchAssets
// fix (PR-TRANSLATE-SCRIPT-SPEC-STOCK-ASSOCIATION-CHANGED-CONTRACT
// ship_sha 648e778b1).
func (f *fakeStockSearch) SearchAssets(_ context.Context, _ ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	return nil, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

// makeScenes returns n model-emitted scenes with Kind=SceneClip.
// Wave 2.0: kind is scene-shape (model-emitted), so makeScenes
// sets the canonical clip-kind default. Tests that care about
// preserved kinds can override directly.
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
// plan.ClipEvidence==nil OR AcceptedClipIDs==empty branches.
// When scenes already exist but there is no clip evidence, the
// binder is a no-op (Changed=false) — there is nothing to bind
// and the scenes are already present from the upstream LLM step.
// This preserves the godlike/07 NO-FAKE-AVAILABILITY contract:
// Changed=true only when actual mutations occur.
//
// Wave 2.0: the binder does NOT fall back to prose. Empty
// scenes + empty text + empty evidence returns Changed=false
// (previously TestSceneAssetBinder_BindClips_ProseFallbackEmptyText
// covered this branch via the prose-fallback path; that scenario
// is gone — the binder is pure binding, no synthesis).
func TestSceneAssetBinder_BindClips_NoClipEvidence(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	// ClipEvidence==nil branch — scenes exist, nothing to bind → no-op.
	scenes := makeScenes(3)
	res := b.BindClips(scenes, "", &scriptpkg.ResolvedGenerationPlan{})
	assert.Equal(t, false, res.Changed, "binder returns Changed=false when scenes exist and ClipEvidence is nil (no-op)")
	assert.Nil(t, res.SynthesizedScenes)
	// AcceptedClipIDs empty branch — same no-op behavior.
	scenes2 := makeScenes(3)
	res = b.BindClips(scenes2, "", &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{},
	})
	assert.Equal(t, false, res.Changed, "binder returns Changed=false when scenes exist and AcceptedClipIDs is empty (no-op)")
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

	res := b.BindClips(scenes, "", plan)
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
	// Wave 2.0: residue-fields always nil from the pure binder.
	assert.Nil(t, res.SynthesizedScenes)
	assert.Nil(t, res.Warnings)
}

// TestSceneAssetBinder_BindClipsFromManifest_UsesEvidenceRefs pins the
// ref-based binding contract: scene order can differ from the manifest
// order, but each scene must bind by its own EvidenceRefs entry.
func TestSceneAssetBinder_BindClipsFromManifest_UsesEvidenceRefs(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "scene zero", Kind: scriptpkg.SceneClip, EvidenceRefs: []string{"slot-2"}},
		{ID: "scene-1", Index: 1, Text: "scene one", Kind: scriptpkg.SceneClip, EvidenceRefs: []string{"slot-1"}},
	}
	manifest := scriptpkg.BindingManifest{
		Slots: []scriptpkg.BindingSlot{
			{Slot: "slot-1", ClipID: "clip-a", DriveLink: "https://drive/a"},
			{Slot: "slot-2", ClipID: "clip-b", DriveLink: "https://drive/b"},
		},
	}

	res := b.BindClipsFromManifest(scenes, manifest)
	require.True(t, res.Changed)
	require.NotNil(t, scenes[0].Bindings.Clip)
	require.NotNil(t, scenes[1].Bindings.Clip)
	assert.Equal(t, "clip-b", scenes[0].Bindings.Clip.ClipID)
	assert.Equal(t, "https://drive/b", scenes[0].Bindings.Clip.DriveLink)
	assert.Equal(t, "clip-a", scenes[1].Bindings.Clip.ClipID)
	assert.Equal(t, "https://drive/a", scenes[1].Bindings.Clip.DriveLink)
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

	res := b.BindClips(scenes, "", plan)
	require.Equal(t, true, res.Changed)
	require.NotNil(t, scenes[0].Bindings.Clip)
	require.NotNil(t, scenes[1].Bindings.Clip)
	assert.Nil(t, scenes[2].Bindings.Clip, "P0 #2: extra scenes must NOT be cycled")
	assert.Nil(t, scenes[3].Bindings.Clip, "P0 #2: extra scenes must NOT be cycled")
	assert.Nil(t, scenes[4].Bindings.Clip, "P0 #2: extra scenes must NOT be cycled")
}

// TestSceneAssetBinder_BindClips_BindingPreservesModelKind is the
// Wave 2.0 replacement for the deleted pre-Wave-1.1
// "ClipEvidenceBuildsScenes" prose-fallback test. It pins the
// pure-binding preservation contract: when the model emits
// scene.Kind directly, the binder does NOT touch Kind; Kind
// passes through to the binding step untouched.
//
// Pre-Wave 2.0 the binder assigned intro/clip/outro kinds by
// position (scene_planner.go::assignKindsByPosition). Wave 2.0
// walked that back — kinds are scene-shape owned by the LLM, not
// binding policy owned by the binder. This test asserts the
// preservation contract so a future regression that re-introduces
// kind assignment fails loudly.
func TestSceneAssetBinder_BindClips_BindingPreservesModelKind(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-a": {Transcript: "transcript a", Description: "desc a", Name: "name a", DriveLink: "https://drive/a", StartMs: 0, EndMs: 1000, Tags: []string{"tag1"}},
				"clip-b": {Description: "desc b", Name: "name b", DriveLink: "https://drive/b", StartMs: 1000, EndMs: 2000},
				"clip-c": {Name: "name c", DriveLink: "https://drive/c", StartMs: 2000, EndMs: 3000},
			},
			DriveLinks: map[string]string{
				"clip-a": "https://drive/a",
				"clip-b": "https://drive/b",
				"clip-c": "https://drive/c",
			},
		},
	}

	// Model sets kinds on its own scene-shape decisions (intro
	// for first, clip for middle, outro for last). The binder
	// must preserve these — pure binding does NOT assign kinds.
	modelScenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "Creative scene 1", Kind: scriptpkg.SceneIntro},
		{ID: "scene-1", Index: 1, Text: "Creative scene 2", Kind: scriptpkg.SceneClip},
		{ID: "scene-2", Index: 2, Text: "Creative scene 3", Kind: scriptpkg.SceneOutro},
	}

	res := b.BindClips(modelScenes, "", plan)
	require.Equal(t, true, res.Changed)
	require.Nil(t, res.SynthesizedScenes)
	require.Nil(t, res.Warnings)

	// Model text is preserved verbatim — binder never overwrites.
	assert.Equal(t, "Creative scene 1", modelScenes[0].Text)
	assert.Equal(t, "Creative scene 2", modelScenes[1].Text)
	assert.Equal(t, "Creative scene 3", modelScenes[2].Text)

	// Model kinds are preserved verbatim — binder does NOT
	// assign intro/clip/outro (Wave 2.0 walked that back).
	assert.Equal(t, scriptpkg.SceneIntro, modelScenes[0].Kind, "Wave 2.0: binder preserves model-emitted kind")
	assert.Equal(t, scriptpkg.SceneClip, modelScenes[1].Kind, "Wave 2.0: binder preserves model-emitted kind")
	assert.Equal(t, scriptpkg.SceneOutro, modelScenes[2].Kind, "Wave 2.0: binder preserves model-emitted kind")

	// Bindings populated with enriched metadata from clip evidence.
	assert.Equal(t, "clip-a", modelScenes[0].Bindings.Clip.ClipID)
	assert.Equal(t, "https://drive/a", modelScenes[0].Bindings.Clip.DriveLink)
	assert.Equal(t, "name a", modelScenes[0].Bindings.Clip.ClipTitle)
	assert.Equal(t, int64(0), modelScenes[0].Bindings.Clip.StartMs)
	assert.Equal(t, int64(1000), modelScenes[0].Bindings.Clip.EndMs)

	assert.Equal(t, "clip-b", modelScenes[1].Bindings.Clip.ClipID)
	assert.Equal(t, "name b", modelScenes[1].Bindings.Clip.ClipTitle)
	assert.Equal(t, int64(1000), modelScenes[1].Bindings.Clip.StartMs)
	assert.Equal(t, int64(2000), modelScenes[1].Bindings.Clip.EndMs)

	assert.Equal(t, "clip-c", modelScenes[2].Bindings.Clip.ClipID)
	assert.Equal(t, "name c", modelScenes[2].Bindings.Clip.ClipTitle)
	assert.Equal(t, int64(2000), modelScenes[2].Bindings.Clip.StartMs)
	assert.Equal(t, int64(3000), modelScenes[2].Bindings.Clip.EndMs)
}

// TestSceneAssetBinder_BindClips_ClipEvidenceNumClipsCap verifies
// that NumClips caps the number of bound clips when model scenes
// exist.
func TestSceneAssetBinder_BindClips_ClipEvidenceNumClipsCap(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		NumClips:   2,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-a": {Name: "name a"},
				"clip-b": {Name: "name b"},
				"clip-c": {Name: "name c"},
			},
			DriveLinks: map[string]string{
				"clip-a": "https://drive/a",
				"clip-b": "https://drive/b",
				"clip-c": "https://drive/c",
			},
		},
	}

	modelScenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "Scene A", Kind: scriptpkg.SceneClip},
		{ID: "scene-1", Index: 1, Text: "Scene B", Kind: scriptpkg.SceneClip},
	}

	res := b.BindClips(modelScenes, "", plan)
	require.Equal(t, true, res.Changed)
	require.Nil(t, res.SynthesizedScenes)

	assert.Equal(t, "Scene A", modelScenes[0].Text)
	assert.Equal(t, "Scene B", modelScenes[1].Text)

	assert.Equal(t, "clip-a", modelScenes[0].Bindings.Clip.ClipID)
	assert.Equal(t, "clip-b", modelScenes[1].Bindings.Clip.ClipID)
}

// TestSceneAssetBinder_BindClips_ClipEvidenceLegacyFallback
// verifies that clip bindings work when ClipDetails is not
// populated, falling back to ClipNames/DriveLinks for metadata.
func TestSceneAssetBinder_BindClips_ClipEvidenceLegacyFallback(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a"},
			ClipNames: map[string]string{
				"clip-a": "legacy name",
			},
			DriveLinks: map[string]string{
				"clip-a": "https://drive/legacy",
			},
		},
	}

	modelScenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "Model text", Kind: scriptpkg.SceneClip},
	}

	res := b.BindClips(modelScenes, "", plan)
	require.Equal(t, true, res.Changed)
	require.Nil(t, res.SynthesizedScenes)

	assert.Equal(t, "Model text", modelScenes[0].Text)
	assert.Equal(t, "clip-a", modelScenes[0].Bindings.Clip.ClipID)
	assert.Equal(t, "https://drive/legacy", modelScenes[0].Bindings.Clip.DriveLink)
}

// TestSceneAssetBinder_BindClips_ClipEvidenceEmptyNoProseFallback
// verifies the clips-source fail-closed branch (Wave 1.1 contract
// preserved): when SourceKind == clips AND AcceptedClipIDs is empty,
// the binder does NOT fall back to prose (no SceneSynthesizer
// available in Wave 2.0 anyway). Changed=false signals to the
// downstream registry that the plan is unavailable.
func TestSceneAssetBinder_BindClips_ClipEvidenceEmptyNoProseFallback(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{},
		},
	}

	res := b.BindClips(nil, "", plan)
	assert.Equal(t, false, res.Changed,
		"clips source + empty evidence → pure-binding no-op (Wave 2.0: no prose fallback available)")
	assert.Nil(t, res.SynthesizedScenes)
	assert.Nil(t, res.Warnings)
}

// ── BindStock ──────────────────────────────────────────────────────────

// TestSceneAssetBinder_BindStock_NilSearch is the canonical
// composition-time fail-closed branch preserved verbatim from the
// pre-Phase-2 StockAssociationProcessor.
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
		ID: "scene-0", Index: 0, Text: "any text", Kind: scriptpkg.SceneClip,
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
		ID: "scene-0", Index: 0, Text: "any text", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{DriveLink: clipDrive},
		},
	}
	scenes := []scriptpkg.SpecScene{sceneWithClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	assert.Nil(t, scenes[0].Bindings.Stock, "stock fallback must not duplicate the clip binding")
}

// TestSceneAssetBinder_BindStock_NoHitNoClipLeavesStockNil covers
// the silent-nil branch: Qdrant empty + scene has no Clip binding
// → Stock stays nil.
func TestSceneAssetBinder_BindStock_NoHitNoClipLeavesStockNil(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	search := &fakeStockSearch{hits: nil}
	sceneNoClip := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "pure narration", Kind: scriptpkg.SceneNarration,
	}
	scenes := []scriptpkg.SpecScene{sceneNoClip}
	res := b.BindStock(context.Background(), scenes, search)
	require.Equal(t, true, res.Changed)
	assert.Nil(t, scenes[0].Bindings.Stock,
		"Stock must stay nil when both Qdrant and Clip binding are absent")
}

// TestSceneAssetBinder_BindStock_SearchErrorFallsBackToClip covers
// the transport-error path: SearchStock returns non-nil error +
// scene has clip drive → fallback binding + best-effort semantics
// (no error propagation).
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
	assert.Nil(t, scenes[0].Bindings.Stock, "stock fallback must not duplicate the clip binding on errors")
}

// TestSceneAssetBinder_BindStock_EmptySceneTextSkipsSearch covers
// the per-iteration empty-text branch: scene.Text empty → skip the
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
	assert.Nil(t, scenes[0].Bindings.Stock, "empty scene.Text must not synthesize a duplicate stock binding")
}
