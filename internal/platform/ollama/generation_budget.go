package ollama

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// ResolveOutputBudget returns a bounded output budget for the actual script
// shape. The old formula reserved 512 tokens per clip for Gemma thinking even
// though the client explicitly sends think=false.
func ResolveOutputBudget(req types.TextGenerationRequest) int {
	words := req.MinWords
	if words <= 0 && req.MaxChars > 0 {
		words = (req.MaxChars + 4) / 5
	}
	if words <= 0 {
		seconds := req.Duration
		if seconds <= 0 {
			seconds = req.DurationMinutes * 60
		}
		if seconds > 0 {
			wpm := req.WordsPerMinute
			if wpm <= 0 {
				wpm = types.WordsPerMinute
			}
			words = seconds * wpm / 60
		}
	}
	if words <= 0 {
		words = 32
	}
	budget := words*2 + 32
	if budget < 96 {
		budget = 96
	}
	if budget > 8192 {
		budget = 8192
	}
	return budget
}

// ResolveContextBudget chooses the smallest safe Ollama context bucket for a
// prompt plus its output budget. Prompt-token estimation is intentionally
// conservative and deterministic; explicit request options remain authoritative.
func ResolveContextBudget(messages []types.Message, output any) int {
	promptChars := 0
	for _, message := range messages {
		promptChars += len(message.Role) + len(message.Content)
	}
	promptTokens := (promptChars + 3) / 4
	outputTokens := 256
	switch value := output.(type) {
	case int:
		outputTokens = value
	case int64:
		outputTokens = int(value)
	case float64:
		outputTokens = int(value)
	case string:
		if parsed, err := parseBudget(value); err == nil {
			outputTokens = parsed
		}
	}
	if outputTokens < 96 {
		outputTokens = 96
	}
	required := promptTokens + outputTokens + 256
	for _, bucket := range []int{2048, 4096, 8192, 16384} {
		if required <= bucket {
			return bucket
		}
	}
	return 32768
}

func parseBudget(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty budget")
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return 0, err
	}
	return parsed, nil
}
