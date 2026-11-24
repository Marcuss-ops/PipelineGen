package vidrush

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PreserveSceneBindings preserves bindings across scene replacements.
func PreserveSceneBindings(previous, replacement []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	return preserveSceneBindings(previous, replacement)
}

// MergeVidRushSegments merges segment results by segment id.
func MergeVidRushSegments(dst, src []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	return mergeVidRushSegments(dst, src)
}

// CloneSpecSceneSlice copies a scene slice.
func CloneSpecSceneSlice(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	return cloneSpecSceneSlice(scenes)
}

// ReadyVidRushCandidate reports whether a candidate is materialized.
func ReadyVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	return readyVidRushCandidate(candidate)
}

// SceneIsFixedMedia reports whether a scene is fixed-media.
func SceneIsFixedMedia(spec scriptpkg.SpecSceneOutput, sceneID, segmentID string, index int) bool {
	return sceneIsFixedMedia(spec, sceneID, segmentID, index)
}

// SceneIndex returns the index of a scene id.
func SceneIndex(scenes []scriptpkg.SpecScene, id string) int {
	return sceneIndex(scenes, id)
}

// ResolveEntityVisualCandidates scores and filters entity image candidates.
func ResolveEntityVisualCandidates(ctx context.Context, scorer SemanticCandidateScorer, class, sourceContext string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	return resolveEntityVisualCandidates(ctx, scorer, class, sourceContext, candidates)
}

// IsPlaceholderImageReference reports whether an image URL is a placeholder.
func IsPlaceholderImageReference(rawURL, mimeType string) bool {
	return isPlaceholderImageReference(rawURL, mimeType)
}
