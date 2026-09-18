package scriptgeneration

import (
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestOutputFromScenesPreservesOrderedTextAndWordCount(t *testing.T) {
	output := outputFromScenes([]Scene{
		{Text: map[Language]string{"it": "La civiltà Maya"}},
		{Text: map[Language]string{"it": "costruì città e osservatori."}},
	}, "it")

	if output.Text != "La civiltà Maya\n\ncostruì città e osservatori." {
		t.Fatalf("output text = %q", output.Text)
	}
	if output.WordCount != 7 {
		t.Fatalf("word count = %d, want 7", output.WordCount)
	}
}

func TestValidateMinimumGeneratedOutputDoesNotBlockWordShortfall(t *testing.T) {
	req := GenerateRequest{ScriptParams: scriptpkg.ScriptSpec{MinWords: 5}}
	for _, text := range []string{"uno due tre quattro cinque", "uno due tre quattro", "uno due"} {
		if err := validateMinimumGeneratedOutput(req, GenerateOutput{Text: text}); err != nil {
			t.Fatalf("non-empty output %q was blocked by its word count: %v", text, err)
		}
	}
}

func TestValidateMinimumGeneratedOutputOnlyRequiresNonEmptyText(t *testing.T) {
	if err := validateMinimumGeneratedOutput(GenerateRequest{}, GenerateOutput{}); !errors.Is(err, ErrEmptyGeneratedText) {
		t.Fatalf("empty output error = %v, want ErrEmptyGeneratedText", err)
	}
}
