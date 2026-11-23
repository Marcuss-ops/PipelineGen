package scriptgeneration

import (
	"context"
	"testing"
)

// TestValidateClipSceneOutputSkipsFixedMediaBelowWordFloor pins CODEX-08: a
// no_voiceover cutaway is fixed media by design and must not be rejected by
// the 12-word clip-narration floor. The old gate counted the cutaway's one-word
// label as narration and failed the whole run.
func TestValidateClipSceneOutputSkipsFixedMediaBelowWordFloor(t *testing.T) {
	r := &Runner{}
	scenes := []Scene{{
		ID:         "scene-1",
		NativeClip: true,
		Text:       map[Language]string{"it": "x"},
	}}
	req := GenerateRequest{SourceLanguage: "it"}
	if !r.validateClipSceneOutput(context.Background(), "run", req, ExecutionContext{}, ExecutionStep{}, scenes) {
		t.Fatal("fixed-media scene must not be rejected by the narration word floor")
	}
}
