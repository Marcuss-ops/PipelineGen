package generation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// StoragePort is the only storage seam generation needs. It prevents a child
// package -> parent images import cycle while keeping ImageStorageService as the
// concrete implementation composed by the root facade.
type StoragePort interface {
	IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*detail.ImageAsset, error)
}

type SyncCommand struct {
	Subject   string
	Topic     string
	Style     string
	Prompts   []string
	Tags      []string
	Width     int
	Height    int
	Model     string
	SkipDrive bool
}

func GenerateSync(ctx context.Context, svc *GenerationService, cmd SyncCommand) (*detail.ImageAsset, error) {
	if svc == nil {
		return nil, fmt.Errorf("image generation service is nil")
	}
	cleanPrompt := pickImagePrompt(cmd.Subject, cmd.Topic, cmd.Prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}
	out, err := RunUsage(ctx, UsecaseDeps{Registry: svc.registry, Styles: svc.styles, Log: svc.log}, UsecaseCommand{
		JobID: fmt.Sprintf("sync-%s", textutil.Slugify(cmd.Subject)), Prompt: cleanPrompt,
		Style: cmd.Style, Model: cmd.Model, Width: cmd.Width, Height: cmd.Height,
		Tags: cmd.Tags,
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}
	return svc.ingestGeneratedImage(ctx, out.Result, cmd.Style, cmd.Tags, cmd.SkipDrive)
}

func (g *GenerationService) ingestGeneratedImage(ctx context.Context, result *GeneratedImage, style string, tags []string, skipDrive bool) (*detail.ImageAsset, error) {
	if result == nil {
		return nil, fmt.Errorf("generated image result is nil")
	}
	if g == nil || g.storage == nil {
		return nil, fmt.Errorf("generated image storage is not wired")
	}
	slug := buildGeneratedImageSlug(result.PromptUsed)
	filename := buildGeneratedImageFilename(result.PromptUsed, result.Format)
	description := buildGeneratedImageDescription(result.PromptUsed)
	source := resolveGeneratedImageSource(string(result.Provider))
	var reader io.Reader = bytes.NewReader(result.Data)
	if result.OutputPath != "" {
		f, err := os.Open(result.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("ingestGeneratedImage: failed to open output path %s: %w", result.OutputPath, err)
		}
		defer f.Close()
		reader = f
	}
	if result.OutputPath == "" && len(result.Data) == 0 {
		return nil, fmt.Errorf("generated image has no data and no output path")
	}
	return g.storage.IngestImage(ctx, slug, style, result.SourceHash, reader, filename, source, description, tags, skipDrive, false)
}
