package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
)

// validateClipSceneOutput contains the source-specific safety checks for
// clip-backed narration. It keeps runSceneTextPhase focused on generation and
// leaves failure persistence at the same execution-step boundary.
func (r *Runner) validateClipSceneOutput(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, scenes []Scene) bool {
	for i, scene := range scenes {
		text := strings.TrimSpace(scene.Text[req.SourceLanguage])
		words := len(strings.Fields(text))
		lower := strings.ToLower(text)
		placeholder := text == "" || words < minimumClipSceneWords || lower == fmt.Sprintf("scene %d", i+1) || lower == "the"
		if placeholder || contaminatedClipNarration(text) {
			code := "SCRIPT_SCENE_TEXT_INVALID"
			if contaminatedClipNarration(text) {
				code = "SCRIPT_SCENE_TEXT_CONTAMINATED"
			}
			cause := fmt.Errorf("%s: scene=%d words=%d minimum=%d placeholder=%t", code, i, words, minimumClipSceneWords, lower == fmt.Sprintf("scene %d", i+1) || lower == "the")
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return false
		}
	}
	return true
}
