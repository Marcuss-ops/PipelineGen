// Package adapters bridges the application script-generation port to Ollama.
package adapters

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// ScriptGeneratorAdapter translates the provider-neutral application request
// into the concrete Ollama request and translates the result back.
type ScriptGeneratorAdapter struct {
	generator *ollama.Generator
}

// WarmModel forwards the optional model-residency seam without widening the
// provider-neutral ScriptGenerator contract used by fakes and other providers.
func (a *ScriptGeneratorAdapter) WarmModel(ctx context.Context, model string) error {
	if a == nil || a.generator == nil {
		return scriptports.ErrScriptGeneratorUnavailable
	}
	return a.generator.WarmModel(ctx, model)
}

// NewScriptGeneratorAdapter constructs an application-port adapter for Ollama.
func NewScriptGeneratorAdapter(generator *ollama.Generator) scriptports.ScriptGenerator {
	if generator == nil {
		return nil
	}
	return &ScriptGeneratorAdapter{generator: generator}
}

func (a *ScriptGeneratorAdapter) GenerateScript(ctx context.Context, req scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	if a == nil || a.generator == nil {
		return nil, scriptports.ErrScriptGeneratorUnavailable
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
		return nil, scriptports.ErrScriptGenerationEmptyResult
	}
	return &scriptports.GenerationResult{
		Script: result.Script, WordCount: result.WordCount, EstDuration: result.EstDuration,
		Model: result.Model, Prompt: result.Prompt, GenerationSource: "ollama",
	}, nil
}

var _ scriptports.ScriptGenerator = (*ScriptGeneratorAdapter)(nil)
