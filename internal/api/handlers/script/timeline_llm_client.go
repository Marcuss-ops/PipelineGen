package script

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/pkg/llmjson"
)

// callTimelineLLM calls the LLM to generate a timeline plan
func callTimelineLLM(ctx context.Context, gen *ollama.Generator, model, prompt string) (*timelineLLMPlan, error) {
	raw, err := gen.GetClient().GenerateWithOptions(ctx, model, prompt, map[string]interface{}{
		"temperature": 0.0,
		"num_predict": 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("timeline planning failed: %w", err)
	}

	zap.L().Info("Raw LLM timeline response", zap.String("raw", raw))

	cleaned := llmjson.StripCodeFence(raw)
	jsonPayload := llmjson.ExtractObject(cleaned)
	if jsonPayload == "" {
		return nil, fmt.Errorf("timeline planning returned empty payload")
	}

	var plan timelineLLMPlan
	if err := json.Unmarshal([]byte(jsonPayload), &plan); err != nil {
		return nil, fmt.Errorf("timeline planning returned invalid json: %w", err)
	}

	return &plan, nil
}
