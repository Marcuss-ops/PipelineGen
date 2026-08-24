// Package scene_test — binder_test.go: hermetic TDD coverage for
// SceneAssetBinder.BindClips + SceneAssetBinder.BindStock.
//
// The binder knows only scene_id, requirements, candidate assets,
// and binding policy. It does NOT know scene text, kind, title,
// index, or prose fallback.
package scene_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene"
)

// ── BindClips ──────────────────────────────────────────────────────────

// TestSceneAssetBinder_BindClips_EmptyRequests is the canonical no-op
// branch.
func TestSceneAssetBinder_BindClips_EmptyRequests(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	res := b.BindClips(nil)
	assert.Equal(t, false, res.Changed)
	assert.Empty(t, res.Bindings)
}

// TestSceneAssetBinder_BindClips_OneToOneBinding covers the
// canonical 1:1 clip-scene binding.
func TestSceneAssetBinder_BindClips_OneToOneBinding(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	const driveA = "https://drive.google.com/a"
	const driveB = "https://drive.google.com/b"
	const driveC = "https://drive.google.com/c"

	reqs := []scene.ClipBindingRequest{
		{SceneID: "scene-0", Candidates: []scene.ClipCandidate{{ClipID: "clip-a", DriveLink: driveA}}},
		{SceneID: "scene-1", Candidates: []scene.ClipCandidate{{ClipID: "clip-b", DriveLink: driveB}}},
		{SceneID: "scene-2", Candidates: []scene.ClipCandidate{{ClipID: "clip-c", DriveLink: driveC}}},
	}

	res := b.BindClips(reqs)
	require.Equal(t, true, res.Changed)
	require.Len(t, res.Bindings, 3)
	assert.Equal(t, "clip-a", res.Bindings["scene-0"].ClipID)
	assert.Equal(t, driveA, res.Bindings["scene-0"].DriveLink)
	assert.Equal(t, "clip-b", res.Bindings["scene-1"].ClipID)
	assert.Equal(t, driveB, res.Bindings["scene-1"].DriveLink)
	assert.Equal(t, "clip-c", res.Bindings["scene-2"].ClipID)
	assert.Equal(t, driveC, res.Bindings["scene-2"].DriveLink)
}

// TestSceneAssetBinder_BindClips_MultipleCandidatesFirstWins verifies
// that when a request carries multiple candidates, only the first
// valid candidate is bound.
func TestSceneAssetBinder_BindClips_MultipleCandidatesFirstWins(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.ClipBindingRequest{
		{
			SceneID: "scene-0",
			Candidates: []scene.ClipCandidate{
				{ClipID: "clip-a", DriveLink: "https://drive/a"},
				{ClipID: "clip-b", DriveLink: "https://drive/b"},
			},
		},
	}

	res := b.BindClips(reqs)
	require.Equal(t, true, res.Changed)
	require.Len(t, res.Bindings, 1)
	assert.Equal(t, "clip-a", res.Bindings["scene-0"].ClipID)
}

// TestSceneAssetBinder_BindClips_EmptyCandidateSkipped verifies that
// candidates with empty ClipID are skipped.
func TestSceneAssetBinder_BindClips_EmptyCandidateSkipped(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.ClipBindingRequest{
		{
			SceneID: "scene-0",
			Candidates: []scene.ClipCandidate{
				{ClipID: "", DriveLink: "https://drive/empty"},
				{ClipID: "clip-a", DriveLink: "https://drive/a"},
			},
		},
	}

	res := b.BindClips(reqs)
	require.Equal(t, true, res.Changed)
	assert.Equal(t, "clip-a", res.Bindings["scene-0"].ClipID)
}

// TestSceneAssetBinder_BindClips_MaxMatchesHonored verifies that
// MaxMatches caps the number of candidates considered.
func TestSceneAssetBinder_BindClips_MaxMatchesHonored(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.ClipBindingRequest{
		{
			SceneID: "scene-0",
			Candidates: []scene.ClipCandidate{
				{ClipID: "clip-a", DriveLink: "https://drive/a"},
				{ClipID: "clip-b", DriveLink: "https://drive/b"},
			},
			Policy: scene.ClipBindingPolicy{MaxMatches: 1},
		},
	}

	res := b.BindClips(reqs)
	require.Equal(t, true, res.Changed)
	assert.Equal(t, "clip-a", res.Bindings["scene-0"].ClipID)
}

// ── BindStock ──────────────────────────────────────────────────────────

// TestSceneAssetBinder_BindStock_EmptyRequests is the canonical no-op
// branch.
func TestSceneAssetBinder_BindStock_EmptyRequests(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	res := b.BindStock(nil)
	assert.Equal(t, false, res.Changed)
	assert.Empty(t, res.Bindings)
}

// TestSceneAssetBinder_BindStock_QdrantHit covers the happy path
// where a stock candidate is bound.
func TestSceneAssetBinder_BindStock_QdrantHit(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.StockBindingRequest{
		{
			SceneID: "scene-0",
			Candidates: []scene.StockCandidate{
				{
					AssetID:   "asset-q-1",
					Name:      "Qdrant 1",
					Source:    "stock",
					DriveLink: "https://drive.google.com/qdrant-asset",
					Score:     0.91,
				},
			},
		},
	}

	res := b.BindStock(reqs)
	require.Equal(t, true, res.Changed)
	require.Len(t, res.Bindings, 1)
	stock := res.Bindings["scene-0"]
	assert.Equal(t, "asset-q-1", stock.AssetID)
	assert.Equal(t, "https://drive.google.com/qdrant-asset", stock.DriveLink)
	assert.InDelta(t, 0.91, stock.Score, 0.001)
	assert.False(t, stock.Fallback)
}

// TestSceneAssetBinder_BindStock_FallbackToClip covers the Qdrant
// empty + clip drive present branch.
func TestSceneAssetBinder_BindStock_FallbackToClip(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.StockBindingRequest{
		{
			SceneID:    "scene-0",
			Candidates: nil,
			Policy: scene.StockBindingPolicy{
				FallbackToClip:    true,
				FallbackDriveLink: "https://drive.google.com/clip-as-stock",
			},
		},
	}

	res := b.BindStock(reqs)
	require.Equal(t, true, res.Changed)
	require.Len(t, res.Bindings, 1)
	stock := res.Bindings["scene-0"]
	assert.True(t, stock.Fallback)
	assert.Equal(t, "https://drive.google.com/clip-as-stock", stock.DriveLink)
	assert.Empty(t, stock.AssetID)
	assert.Zero(t, stock.Score)
}

// TestSceneAssetBinder_BindStock_NoHitNoClipLeavesStockNil covers the
// silent-nil branch.
func TestSceneAssetBinder_BindStock_NoHitNoClipLeavesStockNil(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.StockBindingRequest{
		{
			SceneID:    "scene-0",
			Candidates: nil,
			Policy:     scene.StockBindingPolicy{FallbackToClip: false},
		},
	}

	res := b.BindStock(reqs)
	assert.Equal(t, false, res.Changed)
	assert.Empty(t, res.Bindings)
}

// TestSceneAssetBinder_BindStock_EmptyCandidateSkipped verifies that
// candidates with empty AssetID are skipped.
func TestSceneAssetBinder_BindStock_EmptyCandidateSkipped(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.StockBindingRequest{
		{
			SceneID: "scene-0",
			Candidates: []scene.StockCandidate{
				{AssetID: ""},
				{AssetID: "asset-q-1", DriveLink: "https://drive/q"},
			},
		},
	}

	res := b.BindStock(reqs)
	require.Equal(t, true, res.Changed)
	assert.Equal(t, "asset-q-1", res.Bindings["scene-0"].AssetID)
}

// TestSceneAssetBinder_BindStock_MaxMatchesHonored verifies that
// MaxMatches caps the number of candidates considered.
func TestSceneAssetBinder_BindStock_MaxMatchesHonored(t *testing.T) {
	t.Parallel()
	b := scene.NewSceneAssetBinder(zap.NewNop())
	reqs := []scene.StockBindingRequest{
		{
			SceneID: "scene-0",
			Candidates: []scene.StockCandidate{
				{AssetID: "asset-a", DriveLink: "https://drive/a"},
				{AssetID: "asset-b", DriveLink: "https://drive/b"},
			},
			Policy: scene.StockBindingPolicy{MaxMatches: 1},
		},
	}

	res := b.BindStock(reqs)
	require.Equal(t, true, res.Changed)
	assert.Equal(t, "asset-a", res.Bindings["scene-0"].AssetID)
}
