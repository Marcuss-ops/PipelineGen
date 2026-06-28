// Package scripts — processor_stock_association_test.go pins the
// per-scene Stock binding + clip-fallback contract of
// StockAssociationProcessor.
//
// Contract under test (mirrors the wired behaviour in
// processor_stock_association.go):
//
//  1. Qdrant hit                 → scene.Bindings.Stock { AssetID, DriveLink, Score, Fallback:false }
//  2. Qdrant empty, scene has Clip.DriveLink  → scene.Bindings.Stock { DriveLink = clip.DriveLink, Fallback:true }
//  3. Qdrant empty, scene has NO Clip binding → scene.Bindings.Stock stays nil (silent)
//  4. Qdrant error, scene has Clip.DriveLink  → scene.Bindings.Stock { DriveLink = clip.DriveLink, Fallback:true }
//  5. Empty scene.Text → still attempts clip fallback (matches processor code path)
//
// Best-effort policy: a missing or failing stock search must NOT
// abort the pipeline. The tests assert the side-effect on
// input.SpecScene.Scenes (mutated in-place, as wired in
// generate_one_usecase.go) rather than the returned PostProcessResult
// (which is empty by design — the processor is binding-only).
package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Fakes ──────────────────────────────────────────────────────────────

// fakeStockSearch is a deterministic ports.StockSearchPort
// implementation for table-driven tests. Configure `hits` for the
// happy path, `err` for the transport-error path, `nil` for
// empty-match. `calls` records the number of SearchStock invocations.
type fakeStockSearch struct {
	hits  []ports.StockSearchHit
	err   error
	calls int
}

func (f *fakeStockSearch) SearchStock(_ context.Context, _ string, _ int) ([]ports.StockSearchHit, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func stockPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ID:    "stock-test-item",
		Title: "Jackie Chan",
		Topic: "Jackie Chan",
	}
}

// sceneWithTextAndClip returns a scene with both narrative text and
// a non-empty Clip binding (the canonical "fallback target present"
// case).
func sceneWithTextAndClip(id string, text, clipDrive string) *scriptpkg.SpecScene {
	return &scriptpkg.SpecScene{
		ID:    id,
		Index: 0,
		Text:  text,
		Kind:  scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{
				ClipID:    "clip-" + id,
				DriveLink: clipDrive,
			},
		},
	}
}

// sceneTextOnly (no Clip binding) — used to verify the silent-nil
// edge when neither Qdrant nor a clip drive link can supply a stock.
func sceneTextOnly(id, text string) *scriptpkg.SpecScene {
	return &scriptpkg.SpecScene{
		ID:    id,
		Index: 0,
		Text:  text,
		Kind:  scriptpkg.SceneNarration,
	}
}

// ── Acceptance tests ───────────────────────────────────────────────────

// TestStockAssociation_QdrantHit populates the Stock binding from
// the search hit (Fallback:false, AssetID/DriveLink/Score copied).
func TestStockAssociation_QdrantHit(t *testing.T) {
	t.Parallel()
	const qdrantDrive = "https://drive.google.com/file/d/qdrant-asset"
	search := &fakeStockSearch{
		hits: []ports.StockSearchHit{{
			AssetID:   "asset-qdrant-1",
			Name:      "Qdrant Stock 1",
			Source:    "stock",
			DriveLink: qdrantDrive,
			Score:     0.91,
		}},
	}
	proc := NewStockAssociationProcessor(search, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		*sceneWithTextAndClip("scene-0", "Jackie Chan combat scene", "https://drive.google.com/file/d/clip-clip"),
	}
	input := ProcessInput{
		Text:      "Jackie Chan combat",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	res, err := proc.Process(context.Background(), stockPlan(), input)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, search.calls)
	require.NotNil(t, input.SpecScene.Scenes[0].Bindings.Stock,
		"Stock binding must be populated when Qdrant returns a hit")
	stock := input.SpecScene.Scenes[0].Bindings.Stock
	assert.Equal(t, "asset-qdrant-1", stock.AssetID)
	assert.Equal(t, qdrantDrive, stock.DriveLink)
	assert.InDelta(t, 0.91, stock.Score, 0.001)
	assert.False(t, stock.Fallback, "Fallback must be false on a real Qdrant hit")
}

// TestStockAssociation_FallbackToClip asserts the documented fallback:
// when SearchStock returns no hits AND the scene carries a Clip
// binding with a non-empty DriveLink, the Stock binding is populated
// from the clip's drive link with Fallback:true.
func TestStockAssociation_FallbackToClip(t *testing.T) {
	t.Parallel()
	const clipDrive = "https://drive.google.com/file/d/clip-as-stock"
	search := &fakeStockSearch{hits: nil} // qdrant: no match
	proc := NewStockAssociationProcessor(search, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		*sceneWithTextAndClip("scene-0", "Jackie Chan interview", clipDrive),
	}
	input := ProcessInput{
		Text:      "Jackie Chan interview",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	res, err := proc.Process(context.Background(), stockPlan(), input)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, input.SpecScene.Scenes[0].Bindings.Stock,
		"Stock binding must be populated from clip.DriveLink on fallback")
	stock := input.SpecScene.Scenes[0].Bindings.Stock
	assert.True(t, stock.Fallback, "Fallback must be true when sourcing from clip")
	assert.Equal(t, clipDrive, stock.DriveLink, "Stock DriveLink must mirror clip.DriveLink")
	assert.Empty(t, stock.AssetID, "AssetID must be empty on fallback (no Qdrant match)")
	assert.Zero(t, stock.Score, "Score must be zero on fallback")
}

// TestStockAssociation_NoHitNoClipLeavesStockNil pins the current
// silent-nil behaviour when neither Qdrant nor the scene can
// supply a drive link. The processor must NOT allocate an empty
// Stock binding that would mask the absence downstream.
func TestStockAssociation_NoHitNoClipLeavesStockNil(t *testing.T) {
	t.Parallel()
	search := &fakeStockSearch{hits: nil}
	proc := NewStockAssociationProcessor(search, zap.NewNop())

	scenes := []scriptpkg.SpecScene{*sceneTextOnly("scene-0", "pure narration")}
	input := ProcessInput{
		Text:      "narration",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	res, err := proc.Process(context.Background(), stockPlan(), input)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Nil(t, input.SpecScene.Scenes[0].Bindings.Stock,
		"Stock must stay nil when both Qdrant and Clip binding are absent")
}

// TestStockAssociation_SearchErrorFallsBackToClip asserts the
// transport-error path: when SearchStock returns a non-nil error,
// the processor must log a warning AND fall back to the scene's
// clip drive link (best-effort policy contract).
func TestStockAssociation_SearchErrorFallsBackToClip(t *testing.T) {
	t.Parallel()
	const clipDrive = "https://drive.google.com/file/d/clip-fallback-err"
	search := &fakeStockSearch{err: errors.New("qdrant unreachable")}
	proc := NewStockAssociationProcessor(search, zap.NewNop())

	scenes := []scriptpkg.SpecScene{*sceneWithTextAndClip("scene-0", "scene", clipDrive)}
	input := ProcessInput{
		Text:      "scene",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	res, err := proc.Process(context.Background(), stockPlan(), input)
	require.NoError(t, err, "best-effort processor must NOT propagate Qdrant errors")
	require.NotNil(t, res)
	require.NotNil(t, input.SpecScene.Scenes[0].Bindings.Stock)
	assert.True(t, input.SpecScene.Scenes[0].Bindings.Stock.Fallback)
	assert.Equal(t, clipDrive, input.SpecScene.Scenes[0].Bindings.Stock.DriveLink)
}

// TestStockAssociation_EmptySceneTextStillTriesClip mirrors the
// processor's branch for empty scene.Text: it skips SearchStock and
// goes straight to the clip fallback. Pin this so a future refactor
// does not accidentally omit the clip-bound scenes from processing.
func TestStockAssociation_EmptySceneTextStillTriesClip(t *testing.T) {
	t.Parallel()
	const clipDrive = "https://drive.google.com/file/d/clip-empty-text"
	search := &fakeStockSearch{}
	proc := NewStockAssociationProcessor(search, zap.NewNop())

	scenes := []scriptpkg.SpecScene{*sceneWithTextAndClip("scene-0", "", clipDrive)}
	input := ProcessInput{
		Text:      "",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	_, err := proc.Process(context.Background(), stockPlan(), input)
	require.NoError(t, err)
	assert.Equal(t, 0, search.calls,
		"SearchStock must NOT be called when scene.Text is empty")
	require.NotNil(t, input.SpecScene.Scenes[0].Bindings.Stock)
	assert.True(t, input.SpecScene.Scenes[0].Bindings.Stock.Fallback)
	assert.Equal(t, clipDrive, input.SpecScene.Scenes[0].Bindings.Stock.DriveLink)
}

// TestStockAssociation_NilSearchIsNoOp asserts the composition-time
// defensive short-circuit: when the postprocessor is constructed
// without a StockSearchPort (e.g., Qdrant not wired at startup),
// Process returns an empty result with no panic — the pipeline
// keeps running without per-scene stock bindings.
func TestStockAssociation_NilSearchIsNoOp(t *testing.T) {
	t.Parallel()
	proc := NewStockAssociationProcessor(nil, zap.NewNop())

	scenes := []scriptpkg.SpecScene{*sceneWithTextAndClip("scene-0", "x", "https://drive/x")}
	input := ProcessInput{
		Text:      "x",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	res, err := proc.Process(context.Background(), stockPlan(), input)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Nil(t, input.SpecScene.Scenes[0].Bindings.Stock,
		"nil port must NOT populate Stock binding on its own")
}
