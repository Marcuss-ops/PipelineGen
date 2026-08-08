package adapters

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// OllamaScriptGeneratorAdapter translates the application generation contract
// to the infrastructure Ollama generator without leaking provider types upward.
type OllamaScriptGeneratorAdapter struct {
	generator *ollama.Generator
}

func NewOllamaScriptGeneratorAdapter(generator *ollama.Generator) ports.ScriptGenerator {
	if generator == nil {
		return nil
	}
	return &OllamaScriptGeneratorAdapter{generator: generator}
}

func (a *OllamaScriptGeneratorAdapter) GenerateScript(ctx context.Context, req ports.TextGenerationRequest) (*ports.GenerationResult, error) {
	if a == nil || a.generator == nil {
		return nil, ports.ErrScriptGeneratorUnavailable
	}
	result, err := a.generator.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
		Language: req.Language, Duration: req.Duration, DurationMinutes: req.DurationMinutes,
		MinWords: req.MinWords, WordsPerMinute: req.WordsPerMinute, MaxChars: req.MaxChars,
		Tone: req.Tone, Model: req.Model, Prompt: req.Prompt, SourceText: req.SourceText,
		Title: req.Title, ClipIDs: req.ClipIDs, Options: req.Options, WebContext: req.WebContext,
		DisableWebSearch: req.DisableWebSearch, GroundingPolicy: req.GroundingPolicy,
		OutputMode: ollamatypes.OutputMode(req.OutputMode), Format: req.Format,
		Temperature: req.Temperature, TopP: req.TopP, Seed: req.Seed, NoSeed: req.NoSeed,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, ports.ErrScriptGenerationEmptyResult
	}
	return &ports.GenerationResult{
		Script: result.Script, WordCount: result.WordCount, EstDuration: result.EstDuration,
		Model: result.Model, Prompt: result.Prompt,
	}, nil
}

var _ ports.ScriptGenerator = (*OllamaScriptGeneratorAdapter)(nil)
