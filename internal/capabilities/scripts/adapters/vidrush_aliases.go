package adapters

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/vidrush"
)

type SemanticCandidateScorer = vidrush.SemanticCandidateScorer

var (
	preserveSceneBindings         = vidrush.PreserveSceneBindings
	mergeVidRushSegments          = vidrush.MergeVidRushSegments
	cloneSpecSceneSlice           = vidrush.CloneSpecSceneSlice
	readyVidRushCandidate         = vidrush.ReadyVidRushCandidate
	sceneIsFixedMedia             = vidrush.SceneIsFixedMedia
	sceneIndex                    = vidrush.SceneIndex
	resolveEntityVisualCandidates = vidrush.ResolveEntityVisualCandidates
	isPlaceholderImageReference   = vidrush.IsPlaceholderImageReference
)
