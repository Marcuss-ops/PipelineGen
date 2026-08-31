package adapters

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

func filterNLPScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, 0, len(scenes))
	for _, scene := range scenes {
		if scene.AllowsNLP() {
			out = append(out, scene)
		}
	}
	return out
}

func filterMediaResolutionScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, 0, len(scenes))
	for _, scene := range scenes {
		if sceneAllowsMediaResolution(scene) {
			out = append(out, scene)
		}
	}
	return out
}

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

func sceneAllowsNLP(spec scriptpkg.SpecSceneOutput, sceneID, segmentID string, index int) bool {
	scene, ok := sceneByIdentity(spec, sceneID, segmentID, index)
	return !ok || scene.AllowsNLP()
}

func sceneAllowsMediaSearch(spec scriptpkg.SpecSceneOutput, sceneID, segmentID string, index int) bool {
	scene, ok := sceneByIdentity(spec, sceneID, segmentID, index)
	return !ok || scene.AllowsMediaSearch()
}

func sceneAllowsMediaResolution(scene scriptpkg.SpecScene) bool {
	return scene.AllowsVisualIntent() && scene.AllowsMediaResolution() && scene.AllowsMediaReplacement()
}

func sceneExecutionModeFor(scenes []scriptpkg.SpecScene, segmentID string, index int) scriptpkg.SceneExecutionMode {
	for _, scene := range scenes {
		if segmentID != "" && (scene.ID == segmentID || scene.SegmentID == segmentID) {
			return scene.ExecutionMode.Normalize()
		}
	}
	if index >= 0 && index < len(scenes) {
		return scenes[index].ExecutionMode.Normalize()
	}
	return scriptpkg.SceneExecutionGenerated
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

func hasMediaSearchSegments(input ProcessInput) bool {
	for _, segment := range input.VidRushSegments {
		if segment.ExecutionMode.IsFixedMedia() {
			continue
		}
		if sceneAllowsMediaSearch(input.SpecScene, segment.SceneID, segment.SegmentID, segment.Position) {
			return true
		}
	}
	return false
}

func markArtlistBypassed(input []scriptpkg.VidRushSegmentResult) *PostProcessResult {
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input))
	for _, segment := range input {
		cloned := cloneVidRushSegmentResult(segment)
		if cloned.ExecutionMode.IsFixedMedia() {
			cloned.Cache.Artlist = "BYPASSED"
		}
		segments = append(segments, cloned)
	}
	if len(segments) == 0 {
		return &PostProcessResult{}
	}
	return &PostProcessResult{VidRushSegments: segments, Changed: true}
}
