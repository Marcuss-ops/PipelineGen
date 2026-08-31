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
