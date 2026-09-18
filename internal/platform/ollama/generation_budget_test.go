package ollama

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestResolveOutputBudgetShortScene(t *testing.T) {
	got := ResolveOutputBudget(types.TextGenerationRequest{MinWords: 18})
	if got != 96 {
		t.Fatalf("short scene budget = %d, want 96", got)
	}
}

func TestResolveOutputBudgetDoesNotReserveThinkingOverhead(t *testing.T) {
	got := ResolveOutputBudget(types.TextGenerationRequest{MinWords: 36})
	if got >= 512 {
		t.Fatalf("scene budget = %d, still contains legacy thinking overhead", got)
	}
}

func TestResolveOutputBudgetAllowsLongNaturalCompletionForGemma(t *testing.T) {
	got := ResolveOutputBudget(types.TextGenerationRequest{Model: "gemma4:e4b", MinWords: 200})
	if got < 432 {
		t.Fatalf("200-word narration output budget = %d tokens, want at least 432 so prose is not truncated", got)
	}
}

func TestResolveContextBudgetUsesSmallestSafeBucket(t *testing.T) {
	messages := []types.Message{{Role: "user", Content: "short prompt"}}
	if got := ResolveContextBudget(messages, 96); got != types.ProductionRunnerContext {
		t.Fatalf("short context = %d, want %d resident runner", got, types.ProductionRunnerContext)
	}
	large := []types.Message{{Role: "user", Content: string(make([]byte, 18000))}}
	if got := ResolveContextBudget(large, 512); got != types.ProductionRunnerContext {
		t.Fatalf("large context = %d, want %d", got, types.ProductionRunnerContext)
	}
}
