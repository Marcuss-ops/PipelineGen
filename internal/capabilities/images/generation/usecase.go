package generation

import (
	"context"
	"fmt"

	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type UsecaseDeps struct {
	Registry *Registry
	Styles   imagestyles.StyleResolver
	Log      *zap.Logger
}

type UsecaseCommand struct {
	JobID      string
	Position   int
	Prompt     string
	Style      string
	Model      string
	Width      int
	Height     int
	Tags       []string
	OutputPath string
}

type UsecaseOutput struct {
	OutputPath string
	Manifest   *job.ArtifactManifest
	Result     *GeneratedImage
}

func RunUsage(ctx context.Context, deps UsecaseDeps, cmd UsecaseCommand) (*UsecaseOutput, error) {
	if deps.Registry == nil {
		return nil, ErrNoGenerationProviderWired
	}
	resolved, err := resolveStyle(deps.Styles, cmd.Style, cmd.Model)
	if err != nil {
		return nil, fmt.Errorf("style resolution: %w", err)
	}
	composed, err := NewPromptComposer().Compose(ctx, GenerateCommand{Prompt: cmd.Prompt, Width: cmd.Width, Height: cmd.Height, Tags: cmd.Tags}, resolved)
	if err != nil {
		return nil, fmt.Errorf("prompt composition: %w", err)
	}
	width := composed.Width
	if width == 0 {
		width = 1920
	}
	height := composed.Height
	if height == 0 {
		height = 1080
	}
	result, err := Dispatch(ctx, deps.Registry, GenerateImageRequest{
		Prompt: composed.PromptFinal, NegativePrompt: composed.NegativePrompt, Style: cmd.Style,
		Width: width, Height: height, Tags: composed.Tags, OutputPath: cmd.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}
	manifestPath := result.OutputPath
	if manifestPath == "" {
		manifestPath = cmd.OutputPath
	}
	manifest, err := BuildImageManifest(cmd.JobID, cmd.Position, manifestPath, result.Format)
	if err != nil {
		return nil, fmt.Errorf("artifact manifest build: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("artifact manifest validate: %w", err)
	}
	if deps.Log != nil {
		deps.Log.Info("image generation usecase completed", zap.String("job_id", cmd.JobID), zap.String("artifact_manifest", manifestPath), zap.String("provider", string(result.Provider)), zap.Int("width", result.Width), zap.Int("height", result.Height))
	}
	return &UsecaseOutput{OutputPath: manifestPath, Manifest: manifest, Result: result}, nil
}

func resolveStyle(resolver imagestyles.StyleResolver, style, model string) (imagestyles.ResolvedStyle, error) {
	if resolver == nil {
		return imagestyles.ResolvedStyle{}, nil
	}
	return resolver.Resolve(style, "", model)
}
