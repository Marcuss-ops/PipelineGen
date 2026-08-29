package job

import (
	"encoding/json"
	"testing"
)

func TestCanonicalManifestsAreStableAndTyped(t *testing.T) {
	manifests := []CanonicalManifest{
		LLMManifest("src", "model", "v1", "prompt-v1", "en", "documentary", .4, 42, 512, "schema-v1"),
		ResearchManifest("topic", []string{"q1", "q2"}, "en", "2026", "provider-v1", "planner-v1", "ranker-v1", "fetch-v1", "source-v1", "prompt-v1"),
		TTSManifest("text", "en", "provider", "model", "v1", "voice", 1, 0, 48000, 2, "wav", "word-v2", "norm-v1"),
		TranslationManifest("src", "en", "it", "model", "v1", "prompt-v1", "glossary-v1", "style-v1"),
		ClipManifest("sha", 1, 2, 1920, 1080, 30, 1, "h264", "high", "yuv420p", "strip", "norm-v1", "processor-v1"),
		VidRushManifest("scene", "semantic", "v1", "providers-v1", 5000, 10, "rank", "rank-v1", "reranker", "v1", "rights-v1"),
		OverlayManifest("plan", "template", "v1", "timing", "renderer-v1", "media-v1", "require_gpu_native", "chronon-v1", []string{"a", "b"}, 0, 1, 1920, 1080, 30, 1),
		AudioManifest("voice", "bgm", "sfx", "clip", "mix-v1", "duck-v1", -14, 48000, 2, "audio-v1"),
		RenderManifest("plan", "chronon", "v1", "require_gpu_native", "nvdec", "nvenc", 1920, 1080, 30, 1),
	}
	for _, manifest := range manifests {
		first, err := manifest.JSON()
		if err != nil {
			t.Fatalf("%s: %v", manifest.Kind, err)
		}
		second, err := manifest.JSON()
		if err != nil {
			t.Fatalf("%s second: %v", manifest.Kind, err)
		}
		if string(first) != string(second) {
			t.Fatalf("%s JSON is unstable", manifest.Kind)
		}
		var decoded map[string]any
		if err := json.Unmarshal(first, &decoded); err != nil {
			t.Fatalf("%s invalid JSON: %v", manifest.Kind, err)
		}
		if len(decoded) == 0 {
			t.Fatalf("%s manifest is empty", manifest.Kind)
		}
	}
}
