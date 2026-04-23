package textgen

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// generateWithOllama generates text using Ollama API
func (g *Generator) generateWithOllama(ctx context.Context, req *GenerationRequest, gpuEnabled bool) (string, error) {
	logger.Info("Generating with Ollama",
		zap.String("model", req.Model),
		zap.Bool("gpu_enabled", gpuEnabled),
	)

	url := g.config.OllamaURL + "/api/generate"
	ollamaReq := map[string]interface{}{
		"model":  req.Model,
		"prompt": req.Prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": req.Temperature,
			"num_predict": req.MaxTokens,
		},
	}
	if req.SystemPrompt != "" {
		ollamaReq["system"] = req.SystemPrompt
	}

	return g.doJSONPost(ctx, url, ollamaReq, "response")
}

// generateWithOpenAI generates text using OpenAI Chat Completions API
func (g *Generator) generateWithOpenAI(ctx context.Context, req *GenerationRequest) (string, error) {
	if g.config.OpenAIKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured: set OPENAI_API_KEY env var")
	}

	logger.Info("Generating with OpenAI",
		zap.String("model", req.Model),
	)

	url := "https://api.openai.com/v1/chat/completions"
	return g.doChatCompletion(ctx, url, g.config.OpenAIKey, req)
}

// generateWithGroq generates text using Groq Chat Completions API
func (g *Generator) generateWithGroq(ctx context.Context, req *GenerationRequest) (string, error) {
	if g.config.GroqKey == "" {
		return "", fmt.Errorf("Groq API key not configured: set GROQ_API_KEY env var")
	}

	logger.Info("Generating with Groq",
		zap.String("model", req.Model),
	)

	url := g.config.GroqURL + "/chat/completions"
	return g.doChatCompletion(ctx, url, g.config.GroqKey, req)
}
