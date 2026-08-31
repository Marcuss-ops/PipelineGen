package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSceneExecutionFiltersProtectFixedMedia(t *testing.T) {
	scenes := []scriptpkg.SpecScene{
		{ID: "intro", Index: 0, Text: "fixed", ExecutionMode: scriptpkg.SceneExecutionFixedMedia},
		{ID: "body", Index: 1, Text: "generated", ExecutionMode: scriptpkg.SceneExecutionGenerated},
	}

	nlp := filterNLPScenes(scenes)
	if len(nlp) != 1 || nlp[0].ID != "body" {
		t.Fatalf("NLP filter = %#v, want only generated body", nlp)
	}

	media := filterMediaResolutionScenes(scenes)
	if len(media) != 1 || media[0].ID != "body" {
		t.Fatalf("media filter = %#v, want only generated body", media)
	}

	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}
	if sceneAllowsNLP(spec, "intro", "", 0) {
		t.Fatal("fixed scene allowed NLP")
	}
	if sceneAllowsMediaSearch(spec, "intro", "", 0) {
		t.Fatal("fixed scene allowed media search")
	}
}
