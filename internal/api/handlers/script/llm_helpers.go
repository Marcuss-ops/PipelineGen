package script

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
)

func chooseTimelinePlanWithLLM(ctx context.Context, gen *ollama.Generator, topic string, duration int, sourceText, narrative string) (*timelineLLMPlan, error) {
	if gen == nil || gen.GetClient() == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("invalid duration")
	}

	client := gen.GetClient()
	model := client.Model()
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("ollama model not configured")
	}

	prompt := buildTimelinePlanningPrompt(topic, duration, narrative, sourceText)
	plan, err := callTimelineLLM(ctx, gen, model, prompt)
	if err != nil {
		return nil, err
	}

	if normalized := normalizeTimelineLLMPlan(plan, duration); normalized != nil {
		sanitizeTimelineLLMPlan(normalized, topic)
		return normalized, nil
	}

	return nil, fmt.Errorf("timeline planning returned unusable segments")
}
