package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestNarrationSanitizerMovesInlineSourcesToMetadata(t *testing.T) {
	input := ProcessInput{
		Text: "Il punto di svolta arrivò nel 2003.",
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", Text: "Il punto di svolta arrivò nel 2003. [Fonte: Articolo](https://example.com/a)",
		}}},
	}
	result, err := NewNarrationSanitizer().Process(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	scene := result.UpdatedSpecScene.Scenes[0]
	if scene.Text != "Il punto di svolta arrivò nel 2003." {
		t.Fatalf("scene text = %q", scene.Text)
	}
	if scene.Metadata == nil || len(scene.Metadata.Sources) != 1 {
		t.Fatalf("scene metadata = %#v", scene.Metadata)
	}
	if err := scriptpkg.ValidateSpeakableText(scene.Text); err != nil {
		t.Fatalf("scene text rejected: %v", err)
	}
}
