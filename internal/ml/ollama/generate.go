package ollama

import (
	"context"
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
		EstDuration: wordCount * types.SecondsPerWord,
		Model:       req.Model,
		Prompt:      prompts.BuildTextPrompt(&req),
	}, nil
}

func setTextDefaults(req *types.TextGenerationRequest) {
	if req.Language == "" {
		req.Language = "english"
	}
	if req.Duration == 0 {
		req.Duration = 60
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}
}

func (g *Generator) RegenerateScript(ctx context.Context, req types.RegenerationRequest) (*types.GenerationResult, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	setRegenerationDefaults(&req)
	messages := prompts.BuildRegenerationChatMessages(&req)
	result, err := g.client.Chat(ctx, messages, req.Options)
	if err != nil {
		return nil, fmt.Errorf("script regeneration failed: %w", err)
	}
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:      result,
		WordCount:   wordCount,
		EstDuration: wordCount * types.SecondsPerWord,
		Model:       req.Model,
		Prompt:      req.OriginalScript,
	}, nil
}

func setRegenerationDefaults(req *types.RegenerationRequest) {
	if req.Language == "" {
		req.Language = "english"
	}
}
