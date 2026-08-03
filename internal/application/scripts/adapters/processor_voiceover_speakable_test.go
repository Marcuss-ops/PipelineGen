package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestVoiceoverProcessorSanitizesInlineSourcesBeforeTTS(t *testing.T) {
	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "test", Title: "test", Language: "it", VoiceoverFolderID: "folder"}
	_, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:      "Narrazione [Fonte: articolo](https://example.com)",
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Text: "Narrazione [Fonte: articolo](https://example.com)"}}},
	})
	if err != nil {
		t.Fatalf("error = %v, want sanitized narration to reach TTS", err)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("TTS calls = %d, want 1", stub.calls.Load())
	}
}
