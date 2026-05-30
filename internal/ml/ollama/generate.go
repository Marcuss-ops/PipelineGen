package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/ml/ollama/prompts"
	"velox/go-master/internal/ml/ollama/types"
)

type Generator struct {
	client *client.Client
}

func NewGenerator(c *client.Client) *Generator {
	return &Generator{client: c}
}

func (g *Generator) GetClient() *client.Client {
	return g.client
}

func (g *Generator) GenerateDescription(ctx context.Context, mediaType, prompt, style string) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	systemPrompt := "You are a helpful assistant that writes concise, 2-line human-like semantic descriptions for AI generated media assets."
	userPrompt := fmt.Sprintf("Write a 2-line semantic description for a generated %s.\nPROMPT: %s\nSTYLE: %s\n\nRULES:\n1. Be descriptive and natural.\n2. Do NOT use technical terms like 'AI generated' or model names.\n3. Focus on what is seen and the mood.\n4. Return ONLY the 2 lines of description.", mediaType, prompt, style)

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := g.client.Chat(ctx, messages, nil)
	if err != nil {
		return "", fmt.Errorf("description generation failed: %w", err)
	}

	return strings.TrimSpace(result), nil
}

func (g *Generator) GenerateScript(ctx context.Context, req types.TextGenerationRequest) (*types.GenerationResult, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	setTextDefaults(&req)
	messages := prompts.BuildChatMessages(&req)
	result, err := g.client.Chat(ctx, messages, req.Options)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:      result,
		WordCount:   wordCount,
		EstDuration: estimateDurationSeconds(wordCount),
		Model:       req.Model,
		Prompt:      prompts.BuildTextPrompt(&req),
	}, nil
}

// estimateDurationSeconds estimates speech duration from word count using WordsPerMinute (140 WPM)
func estimateDurationSeconds(wordCount int) int {
	if wordCount <= 0 {
		return 0
	}
	return (wordCount * 60) / types.WordsPerMinute
}

func setTextDefaults(req *types.TextGenerationRequest) {
	types.ApplyDefaults(req)
}

func (g *Generator) RegenerateScript(ctx context.Context, req types.RegenerationRequest) (*types.GenerationResult, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	types.ApplyDefaultsToRegeneration(&req)
	messages := prompts.BuildRegenerationChatMessages(&req)
	result, err := g.client.Chat(ctx, messages, req.Options)
	if err != nil {
		return nil, fmt.Errorf("script regeneration failed: %w", err)
	}
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:      result,
		WordCount:   wordCount,
		EstDuration: estimateDurationSeconds(wordCount),
		Model:       req.Model,
		Prompt:      req.OriginalScript,
	}, nil
}

func (g *Generator) TranslateText(ctx context.Context, text, targetLanguage string) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	systemPrompt := "You are a professional translator. Translate the text EXCLUSIVELY into the requested target language. Preserve formatting, paragraphs, tone, and do NOT add any introductions, explanations or metadata. Return only the translated text."
	userPrompt := fmt.Sprintf("Translate the following text to target language: %s\n\nTEXT:\n%s", targetLanguage, text)

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := g.client.Chat(ctx, messages, nil)
	if err != nil {
		return "", fmt.Errorf("translation failed: %w", err)
	}

	return strings.TrimSpace(result), nil
}

func (g *Generator) GenerateVideoMetadata(ctx context.Context, title string) (string, []string, error) {
	if g.client == nil {
		return "", nil, fmt.Errorf("ollama client not initialized")
	}

	systemPrompt := "You are a professional video optimizer. Provide metadata strictly in English based on the given title."
	userPrompt := fmt.Sprintf(`Given the video title: "%s"

Generate:
1. A concise, professional, engaging video description (1 to 2 lines max) in English. Do not write intros or greetings, start directly.
2. A list of 5 to 8 generic keywords/tags in English relevant to the topic.

You must respond ONLY with a raw JSON object matching the following structure:
{
  "description": "Engaging description of the video...",
  "tags": ["tag1", "tag2", "tag3"]
}`, title)

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := g.client.Chat(ctx, messages, nil)
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

