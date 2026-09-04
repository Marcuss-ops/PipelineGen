package generation

import (
	"context"
	"fmt"
	"strings"

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
	JobID string
	Position int
	Prompt string
	Style string
	Model string
	Width int
	Height int
	Tags []string
	OutputPath string
}

type UsecaseOutput struct {
	OutputPath string
	Manifest *job.ArtifactManifest
	Result *GeneratedImage
}

// RunUsage is the persistence-agnostic generation pipeline.
func RunUsage(ctx context.Context, deps UsecaseDeps, cmd UsecaseCommand) (*UsecaseOutput, error) {
	if deps.Registry == nil {
		return nil, ErrNoGenerationProviderWired
	}
	resolved, err := resolveStyle(deps.Styles, cmd.Style, cmd.Model)
	if err != nil {
		return nil, fmt.Errorf("style resolution: %w", err)
	}
	finalPrompt := promptComposer(cmd.Prompt, resolved.PromptSuffix)
	width := cmd.Width
	if width == 0 { width = 1920 }
	height := cmd.Height
	if height == 0 { height = 1080 }
	result, err := Dispatch(ctx, deps.Registry, GenerateImageRequest{
		Prompt: finalPrompt, NegativePrompt: resolved.NegativePrompt, Style: cmd.Style,
		Width: width, Height: height, Tags: cmd.Tags, OutputPath: cmd.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}
	manifestPath := result.OutputPath
	if manifestPath == "" { manifestPath = cmd.OutputPath }
	manifest, err := BuildImageManifest(cmd.JobID, cmd.Position, manifestPath, result.Format)
	if err != nil {
		return nil, fmt.Errorf("artifact manifest build: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("artifact manifest validate: %w", err)
	}
	if deps.Log != nil {
		deps.Log.Info("image generation usecase completed", zap.String("job_id", cmd.JobID), zap.String("output_path", manifestPath), zap.String("provider", string(result.Provider)), zap.Int("width", result.Width), zap.Int("height", result.Height))
	}
	return &UsecaseOutput{OutputPath: manifestPath, Manifest: manifest, Result: result}, nil
}

func resolveStyle(resolver imagestyles.StyleResolver, style, model string) (imagestyles.ResolvedStyle, error) {
	if resolver == nil { return imagestyles.ResolvedStyle{}, nil }
	return resolver.Resolve(style, "", model)
}

func promptComposer(originalPrompt, styleSuffix string) string {
	original := strings.TrimSpace(originalPrompt)
	suffix := strings.TrimSpace(styleSuffix)
	if suffix == "" { return original }
	if original == "" { return suffix }
	return original + ", " + suffix
}
