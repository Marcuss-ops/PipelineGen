// Package scriptgeneration — result_reducer_test.go certifies the
// VidRushResultReducer contract: results arriving out of order are applied in
// canonical SceneIndex order, the input envelope is never mutated, scenes are
// matched by canonical identity, and a nil merger leaves scenes unchanged.
package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingMerger records the SceneID of each result it applies, in call order.
// It never mutates the input scene beyond setting a marker field, proving the
// reducer invoked it in canonical order.
type recordingMerger struct {
	order []string
}

func (m *recordingMerger) Merge(scene scriptpkg.SpecScene, result scriptpkg.VidRushSegmentResult) scriptpkg.SpecScene {
	m.order = append(m.order, result.SceneID)
	out := scene
	out.Title = "merged:" + result.SceneID
	return out
}

func TestResultReducer_PreservesCanonicalSceneOrder(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "First scene text"},
			{ID: "scene-1", Index: 1, Text: "Second scene text"},
			{ID: "scene-2", Index: 2, Text: "Third scene text"},
		},
	}

	// Arrive out of order: scene-2, scene-0, scene-1.
	results := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-2", Position: 2, Text: "Third scene text"},
		{SceneID: "scene-0", Position: 0, Text: "First scene text"},
		{SceneID: "scene-1", Position: 1, Text: "Second scene text"},
	}

	out := reducer.Reduce(spec, results)
	require.Equal(t, 1, out.Version)
	require.Len(t, out.Scenes, 3)

	assert.Equal(t, []string{"scene-0", "scene-1", "scene-2"}, merger.order, "merger must be invoked in canonical SceneIndex order")
	for i, scene := range out.Scenes {
		assert.Equal(t, "merged:scene-"+string(rune('0'+i)), scene.Title, "scene %d must carry its own merged result", i)
	}
}

func TestResultReducer_DoesNotMutateInputEnvelope(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "First scene text", Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{{ClipID: "clip-0"}}}},
		},
	}

	results := []scriptpkg.VidRushSegmentResult{{SceneID: "scene-0", Position: 0, Text: "First scene text"}}
	out := reducer.Reduce(spec, results)

	// The input scene must remain untouched.
	require.Equal(t, "", spec.Scenes[0].Title, "input scene title must not be mutated")
	require.Equal(t, "clip-0", spec.Scenes[0].Bindings.Clips[0].ClipID, "input scene bindings must not be mutated")

	// The output scene is a distinct value with the merged marker.
	assert.Equal(t, "merged:scene-0", out.Scenes[0].Title)
}

func TestResultReducer_MatchesByCanonicalIdentity(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	// SegmentID matches before SceneID before Position.
	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", SegmentID: "seg-a", Index: 0, Text: "First"},
			{ID: "scene-1", SegmentID: "seg-b", Index: 1, Text: "Second"},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-b", SceneID: "scene-1", Position: 1, Text: "Second"},
		{SegmentID: "seg-a", SceneID: "scene-0", Position: 0, Text: "First"},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 2)
	assert.Equal(t, []string{"scene-0", "scene-1"}, merger.order)
	assert.Equal(t, "merged:scene-0", out.Scenes[0].Title)
	assert.Equal(t, "merged:scene-1", out.Scenes[1].Title)
}

func TestResultReducer_PositionFallbackWhenIDsEmpty(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	// The canonical fallback applies only when the scene carries neither a
	// SegmentID nor an ID, mirroring the batch path's findSegmentForScene.
	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{Index: 0, Text: "First"},
			{Index: 1, Text: "Second"},
		},
	}
	// Results carry only Position (no SceneID/SegmentID), so matching falls
	// back to the scene index.
	results := []scriptpkg.VidRushSegmentResult{
		{Position: 1, Text: "Second"},
		{Position: 0, Text: "First"},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 2)
	assert.Equal(t, "merged:", out.Scenes[0].Title, "position-matched result with empty SceneID is still applied")
	assert.Equal(t, "merged:", out.Scenes[1].Title)
	assert.Len(t, merger.order, 2)
}

func TestResultReducer_NoPositionFallbackWhenSceneHasID(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	// When a scene carries an ID, a result whose SceneID does not match must
	// not be silently applied by position — identity precedence wins, exactly
	// as in the batch path.
	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "First"},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{Position: 0, Text: "First"}, // no SceneID
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 1)
	assert.Empty(t, merger.order, "identity mismatch must not fall back to position when a scene ID exists")
	assert.Equal(t, "", out.Scenes[0].Title)
}

func TestResultReducer_NilMergerLeavesScenesUnchanged(t *testing.T) {
	reducer := NewVidRushResultReducer(nil)

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "First"},
			{ID: "scene-1", Index: 1, Text: "Second"},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-1", Position: 1, Text: "Second"},
		{SceneID: "scene-0", Position: 0, Text: "First"},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 2)
	assert.Equal(t, spec.Scenes, out.Scenes, "nil merger must leave scenes unchanged")
}

func TestResultReducer_UnmatchedResultsAreIgnored(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "First"},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-99", Position: 99, Text: "Orphan"},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 1)
	assert.Empty(t, merger.order, "unmatched results must never be applied")
	assert.Equal(t, "", out.Scenes[0].Title)
}

func TestResultReducer_DiscardsStaleTextHashResults(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	// The current scene carries the new text; the result was derived from the
	// old text, so its TextHash differs and it must be fenced out.
	currentText := "New scene text"
	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: currentText},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-0", Position: 0, Text: "Old scene text", TextHash: SceneTextHash("Old scene text")},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 1)
	assert.Empty(t, merger.order, "stale text-hash result must be discarded before the merge")
	assert.Equal(t, "", out.Scenes[0].Title)
	assert.Nil(t, out.Scenes[0].Annotations)
}

func TestResultReducer_AppliesMatchingTextHashResult(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	text := "Current scene text"
	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: text},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-0", Position: 0, Text: text, TextHash: SceneTextHash(text)},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 1)
	assert.Equal(t, []string{"scene-0"}, merger.order, "matching text-hash result must be applied")
	assert.Equal(t, "merged:scene-0", out.Scenes[0].Title)
}

func TestResultReducer_EmptyTextHashIsNotFenced(t *testing.T) {
	merger := &recordingMerger{}
	reducer := NewVidRushResultReducer(merger)

	// An empty TextHash has no identity to compare; upstream (the
	// coordinator) owns revision fencing for such results. The reducer must
	// not falsely discard them.
	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "Text"},
		},
	}
	results := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-0", Position: 0, Text: "Text"},
	}

	out := reducer.Reduce(spec, results)
	require.Len(t, out.Scenes, 1)
	assert.Len(t, merger.order, 1, "empty TextHash must not be fenced by the reducer")
	assert.Equal(t, "merged:scene-0", out.Scenes[0].Title)
}
