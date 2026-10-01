package script

import "strings"

// SceneHasDirectStockBinding reports whether the scene is anchored to a
// caller-supplied direct stock binding: an explicit stock_bindings entry, or
// the segment stock_folder shorthand expanded into the plan/input bindings.
// Such a scene keeps generated execution mode (it still gets narration, NLP
// and subtitles) but its VISUAL source is authoritative, so no provider,
// catalog or image search may run for it.
//
// Kernel home: both the adapters package (search processors) and the parent
// scriptgeneration package (incremental resolver chain) must apply the SAME
// gate, and neither may depend on the other.
func SceneHasDirectStockBinding(spec SpecSceneOutput, bindings []StockBindingInput, sceneID, segmentID string, index int) bool {
	for i := range spec.Scenes {
		scene := spec.Scenes[i]
		if segmentID != "" && scene.SegmentID == segmentID || sceneID != "" && scene.ID == sceneID {
			if scene.Bindings.Stock != nil {
				return true
			}
		}
	}
	if index >= 0 && index < len(spec.Scenes) && spec.Scenes[index].Bindings.Stock != nil {
		// Positional fallback mirrors the adapters sceneByIdentity contract.
		if sceneID == "" && segmentID == "" {
			return true
		}
	}
	for _, binding := range bindings {
		if segmentID != "" && strings.TrimSpace(binding.SegmentID) == segmentID {
			return true
		}
		if sceneID != "" && strings.TrimSpace(binding.SceneID) == sceneID {
			return true
		}
	}
	return false
}

// StockBindingForSegment resolves the direct stock binding for one segment
// across the two surfaces that can carry it: the resolved plan (canonical run
// path, the incremental coordinator and the single-segment Materialize port)
// and the process input (compositions that pass caller bindings outside the
// plan).
func StockBindingForSegment(plan *ResolvedGenerationPlan, inputBindings []StockBindingInput, segment VidRushSegmentResult) (StockBindingInput, bool) {
	segmentID := strings.TrimSpace(segment.SegmentID)
	sceneID := strings.TrimSpace(segment.SceneID)
	surfaces := [][]StockBindingInput{inputBindings}
	if plan != nil {
		surfaces = [][]StockBindingInput{plan.StockBindings, inputBindings}
	}
	for _, surface := range surfaces {
		for _, binding := range surface {
			if segmentID != "" && strings.TrimSpace(binding.SegmentID) == segmentID {
				return binding, true
			}
			if sceneID != "" && strings.TrimSpace(binding.SceneID) == sceneID {
				return binding, true
			}
		}
	}
	return StockBindingInput{}, false
}
