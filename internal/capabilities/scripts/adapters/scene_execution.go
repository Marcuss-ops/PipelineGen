package adapters

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

func sceneByIdentity(spec scriptpkg.SpecSceneOutput, sceneID, segmentID string, index int) (scriptpkg.SpecScene, bool) {
	for _, scene := range spec.Scenes {
		if sceneID != "" && scene.ID == sceneID {
			return scene, true
		}
		if segmentID != "" && scene.SegmentID == segmentID {
			return scene, true
		}
	}
	if index >= 0 && index < len(spec.Scenes) {
		return spec.Scenes[index], true
	}
	return scriptpkg.SpecScene{}, false
}

func sceneAllowsMediaSearch(spec scriptpkg.SpecSceneOutput, sceneID, segmentID string, index int) bool {
	scene, ok := sceneByIdentity(spec, sceneID, segmentID, index)
	return !ok || scene.AllowsMediaSearch()
}

// sceneHasDirectStockBinding delegates to the kernel gate: one owner for the
// direct stock-binding contract, shared with the incremental resolver chain
// in the parent scriptgeneration package.
func sceneHasDirectStockBinding(spec scriptpkg.SpecSceneOutput, bindings []scriptpkg.StockBindingInput, sceneID, segmentID string, index int) bool {
	return scriptpkg.SceneHasDirectStockBinding(spec, bindings, sceneID, segmentID, index)
}

func stockBindingForSegment(plan *scriptpkg.ResolvedGenerationPlan, inputBindings []scriptpkg.StockBindingInput, segment scriptpkg.VidRushSegmentResult) (scriptpkg.StockBindingInput, bool) {
	return scriptpkg.StockBindingForSegment(plan, inputBindings, segment)
}

func sceneAllowsMediaResolution(scene scriptpkg.SpecScene) bool {
	return scene.AllowsVisualIntent() && scene.AllowsMediaResolution() && scene.AllowsMediaReplacement()
}

func sceneIsFixedMedia(spec scriptpkg.SpecSceneOutput, sceneID, segmentID string, index int) bool {
	scene, ok := sceneByIdentity(spec, sceneID, segmentID, index)
	return ok && scene.ExecutionMode.IsFixedMedia()
}

func allSegmentsFixedMedia(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult) bool {
	if len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !segment.ExecutionMode.IsFixedMedia() && !sceneIsFixedMedia(spec, segment.SceneID, segment.SegmentID, segment.Position) {
			return false
		}
	}
	return true
}

func hasImageSearchSegments(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) bool {
	for _, segment := range input.VidRushSegments {
		if segment.ExecutionMode.IsFixedMedia() {
			continue
		}
		if scriptpkg.SceneHasDirectStockBinding(input.SpecScene, input.StockBindings, segment.SceneID, segment.SegmentID, segment.Position) {
			if stockBoundEntityImageMaterializationEnabled(plan) {
				return true
			}
			continue
		}
		if sceneAllowsMediaSearch(input.SpecScene, segment.SceneID, segment.SegmentID, segment.Position) {
			return true
		}
	}
	return false
}
