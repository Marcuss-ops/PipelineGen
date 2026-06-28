package images

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ErrImageGenNotImplemented is the honest error returned by GenerateSmartImage
// and GenerateSmartImageWithAccount. The Google Slides API path has been removed
// because it only produced slide thumbnails containing text, not real AI images.
// The real AI generation pipeline (Playwright → Chrome → slides.new → Nano Banana Pro)
// will be re-introduced via async jobs (image.generate.google) and the worker system
// in FASE 3-8 of the image generation refactoring plan.
var ErrImageGenNotImplemented = fmt.Errorf("image generation via Google Slides API has been removed: it produced only slide thumbnails, not AI-generated images. This endpoint will be replaced by the async image.generate.google job pipeline (pending FASE 3-8)")

// GenerateSmartImage generates an AI image via the injected ImageGenerator port.
// When imageGen is nil (not wired), returns ErrImageGenNotImplemented.
// When wired (e.g. ChromeImageProvider), delegates generation and then ingests
// the result through the normal media_assets pipeline.
func (s *Service) GenerateSmartImage(
	ctx context.Context,
	subject string,
	topic string,
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
) (*asset.ImageAsset, error) {
	return s.GenerateSmartImageWithAccount(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive, "", "")
}

// GenerateSmartImageWithAccount generates an AI image via the injected
// ImageGenerator port. When imageGen is nil (not wired), returns
// ErrImageGenNotImplemented. The account and projectID parameters are
// reserved for future multi-account Google Slides support (FASE 7).
func (s *Service) GenerateSmartImageWithAccount(
	ctx context.Context,
	subject string,
	topic string,
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
	_, _ string, // account, projectID — reserved for FASE 7 multi-account
) (*asset.ImageAsset, error) {
	if s.imageGen == nil {
		return nil, ErrImageGenNotImplemented
	}

	cleanPrompt := pickImagePrompt(subject, topic, prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	// Delegate to the injected ImageGenerator port.
	// OutputPath is empty here (sync endpoint); the provider chooses a temp path.
	result, err := s.imageGen.Generate(ctx, GenerateImageRequest{
		Prompt: cleanPrompt,
		Style:  style,
		Width:  width,
		Height: height,
		Model:  model,
		Tags:   tags,
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}

	// Ingest the generated image through the normal pipeline

	return s.ingestGeneratedImage(ctx, result, style, tags, skipDrive)
}

// ingestGeneratedImage ingests a GeneratedImage result into the canonical
// media_assets pipeline (local storage, Drive upload, DB record).
//
// FASE 9 (June 2026): when result.OutputPath is set (canonical workspace
// path), the file is opened directly for zero-copy ingest. Falls back to
// in-memory Data bytes for backward compatibility (sync endpoints).
func (s *Service) ingestGeneratedImage(
	ctx context.Context,
	result *GeneratedImage,
	style string,
	tags []string,
	skipDrive bool,
) (*asset.ImageAsset, error) {
	if result == nil {
		return nil, fmt.Errorf("generated image result is nil")
	}

	slug := textutil.Slugify(result.PromptUsed)
	if len(slug) > 50 {
		slug = slug[:50]
	}

	filename := result.PromptUsed
	if len(filename) > 80 {
		filename = filename[:80]
	}
	filename = textutil.Slugify(filename) + "." + result.Format

	description := fmt.Sprintf("AI generated image via Chrome/Playwright for prompt: %s", result.PromptUsed)

	source := result.Provider
	if source == "" {
		source = "google-slides"
	}

	// Canonical file-based ingest: open the workspace file directly instead
	// of passing in-memory bytes. Zero-copy into IngestImage → media_assets.
	var dataReader io.Reader = bytes.NewReader(result.Data)
	if result.OutputPath != "" {
		f, err := os.Open(result.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("ingestGeneratedImage: failed to open output path %s: %w", result.OutputPath, err)
		}
		defer f.Close()
		dataReader = f
	}

	if result.OutputPath == "" && len(result.Data) == 0 {
		return nil, fmt.Errorf("generated image has no data and no output path")
	}

	return s.IngestImage(
		ctx,
		slug,
		style,
		result.SourceHash,
		dataReader,
		filename,
		source,
		description,
		tags,
		skipDrive,
		false,
	)
}

// pickImagePrompt extracts the most specific prompt from a list of prompts
// or constructs one from subject+topic.
// KEPT: will be reused by the future Playwright-based ChromeImageProvider (FASE 3-8).
func pickImagePrompt(subject, topic string, prompts []string) string {
	for _, p := range prompts {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}

	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)

	switch {
	case subject != "" && topic != "":
		return fmt.Sprintf("%s, %s, cinematic landscape", subject, topic)
	case subject != "":
		return fmt.Sprintf("%s, cinematic landscape", subject)
	case topic != "":
		return fmt.Sprintf("%s, cinematic landscape", topic)
	default:
		return ""
	}
}
