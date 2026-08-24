package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// GenerateVideoMetadata generates YouTube metadata using the Generator's default model.
// To use a lighter model, call GenerateVideoMetadataWithModel.
func (g *Generator) GenerateVideoMetadata(ctx context.Context, title string) (string, []string, error) {
	return g.GenerateVideoMetadataWithModel(ctx, title, "")
}

// GenerateVideoMetadataWithModel generates YouTube metadata using the specified model.
// If model is empty, falls back to g.metadataModel, then the Generator's default model.
func (g *Generator) GenerateVideoMetadataWithModel(ctx context.Context, title string, model string) (string, []string, error) {
	if g.client == nil {
		return "", nil, fmt.Errorf("ollama client not initialized")
	}

	var systemPrompt, userPrompt string
	if cfg := prompts.Get(); cfg != nil {
		s, u, err := cfg.RenderVideoMetadata(title)
		if err == nil {
			systemPrompt, userPrompt = s, u
		}
	}
	if systemPrompt == "" {
		systemPrompt = "You are a professional video optimizer. Provide metadata strictly in English based on the given title."
		userPrompt = fmt.Sprintf(`Given the video title: "%s"

Generate:
1. A concise, professional, engaging video description (1 to 2 lines max) in English. Do not write intros or greetings, start directly.
2. A list of 5 to 8 generic keywords/tags in English relevant to the topic.

You must respond ONLY with a raw JSON object matching the following structure:
{
  "description": "Engaging description of the video...",
  "tags": ["tag1", "tag2", "tag3"]
}`, title)
	}

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	opts := map[string]any{}
	if effectiveModel := g.resolveModel(model); effectiveModel != "" {
		opts["model"] = effectiveModel
	}
	result, err := g.client.Chat(ctx, messages, opts, nil)
	if err != nil {
		return "", nil, fmt.Errorf("metadata generation failed: %w", err)
	}

	// Clean code blocks or extra text if any, and parse the json
	cleanJSON := result
	if idx := strings.Index(cleanJSON, "{"); idx != -1 {
		cleanJSON = cleanJSON[idx:]
	}
	if idx := strings.LastIndex(cleanJSON, "}"); idx != -1 {
		cleanJSON = cleanJSON[:idx+1]
	}

	type MetadataResponse struct {
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}

	var meta MetadataResponse
	if err := json.Unmarshal([]byte(cleanJSON), &meta); err != nil {
		// Fallback parse logic if LLM failed to return valid JSON
		return strings.TrimSpace(result), []string{}, nil
	}

	return meta.Description, meta.Tags, nil
}
