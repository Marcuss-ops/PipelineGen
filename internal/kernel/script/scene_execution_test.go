package script

import "testing"

func TestSceneExecutionModeGeneratedAllowsPipelineWork(t *testing.T) {
	mode := SceneExecutionGenerated
	checks := map[string]bool{
		"translation":       mode.AllowsTranslation(),
		"tts":               mode.AllowsTTS(),
		"nlp":               mode.AllowsNLP(),
		"visual_intent":     mode.AllowsVisualIntent(),
		"media_search":      mode.AllowsMediaSearch(),
		"media_replacement": mode.AllowsMediaReplacement(),
		"generated_audio":   mode.AllowsGeneratedAudio(),
		"media_resolution":  mode.AllowsMediaResolution(),
	}
	for name, allowed := range checks {
		if !allowed {
			t.Errorf("generated mode disallows %s", name)
		}
	}
}

func TestSceneExecutionModeFixedMediaBlocksMutatingPipelineWork(t *testing.T) {
	mode := SceneExecutionFixedMedia
	checks := map[string]bool{
		"translation":       mode.AllowsTranslation(),
		"tts":               mode.AllowsTTS(),
		"nlp":               mode.AllowsNLP(),
		"visual_intent":     mode.AllowsVisualIntent(),
		"media_search":      mode.AllowsMediaSearch(),
		"media_replacement": mode.AllowsMediaReplacement(),
		"generated_audio":   mode.AllowsGeneratedAudio(),
		"media_resolution":  mode.AllowsMediaResolution(),
	}
	for name, allowed := range checks {
		if allowed {
			t.Errorf("fixed_media mode allows %s", name)
		}
	}
}

func TestSceneExecutionModeEmptyIsGeneratedAndUnknownFailsClosed(t *testing.T) {
	if got := SceneExecutionMode("").Normalize(); got != SceneExecutionGenerated {
		t.Fatalf("empty mode normalized to %q, want generated", got)
	}
	if got := SceneExecutionMode("future_mode").Normalize(); got != SceneExecutionFixedMedia {
		t.Fatalf("unknown mode normalized to %q, want fixed_media fail-closed", got)
	}
	if SceneExecutionMode("future_mode").Valid() {
		t.Fatal("unknown mode reported valid")
	}
}
