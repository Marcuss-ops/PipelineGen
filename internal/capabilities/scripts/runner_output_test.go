package scriptgeneration

import (
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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

func TestValidateMinimumGeneratedOutput(t *testing.T) {
	req := GenerateRequest{ScriptParams: scriptpkg.ScriptSpec{MinWords: 5}}
	valid := GenerateOutput{Text: "uno due tre quattro cinque", WordCount: 5}
	if err := validateMinimumGeneratedOutput(req, valid); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}

	short := GenerateOutput{Text: "uno due", WordCount: 2}
	err := validateMinimumGeneratedOutput(req, short)
	if !errors.Is(err, ErrMinimumTextGate) {
		t.Fatalf("short output error = %v, want ErrMinimumTextGate", err)
	}
}

func TestValidateMinimumGeneratedOutputRequiresOneWordWithoutExplicitMinimum(t *testing.T) {
	if err := validateMinimumGeneratedOutput(GenerateRequest{}, GenerateOutput{}); !errors.Is(err, ErrMinimumTextGate) {
		t.Fatalf("empty output error = %v, want ErrMinimumTextGate", err)
	}
}
