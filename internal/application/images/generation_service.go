package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// GenerationService handles AI-powered image generation via the
// injected ImageGenerator port and StyleResolver. It is the canonical
// entry point for both sync (GenerateSmartImage) and async (HandleJob)
// generation.
//
// Step 4 (July 2026): StyleResolver is now wired — every generation
// request goes through Resolve → PromptComposer → ImageGenerator.Generate
// so the style suffix, negative prompt, and dimensions are resolved in
// one place (PromptComposer is the single source of truth for prompt
// composition).
//
// Cross-boundary ingestion: ingestGeneratedImage calls
// ImageStorageService.IngestImage to persist generated images through
// the canonical media_assets pipeline.
type GenerationService struct {
	imageGen ImageGenerator
	styles   generation.StyleResolver // Step 4: fail-closed style resolution
	log      *zap.Logger
	storage  *ImageStorageService // for ingestGeneratedImage → IngestImage
}

// ── PromptComposer (Step 4) ────────────────────────────────────────────

// promptComposer is the single place where the user prompt and the
// resolved style suffix are combined into the final prompt.
//
// Rules:
//   - Empty style suffix → prompt is used unchanged.
//   - Non-empty suffix → prompt + ", " + suffix.
//   - Empty prompt + non-empty suffix → suffix becomes the prompt.
func promptComposer(originalPrompt, styleSuffix string) string {
	original := strings.TrimSpace(originalPrompt)
	suffix := strings.TrimSpace(styleSuffix)

	if suffix == "" {
		return original
	}
	if original == "" {
		return suffix
	}
	return original + ", " + suffix
}

// ── Public API ─────────────────────────────────────────────────────────

// GenerateSmartImage generates an AI image via the injected ImageGenerator port.
// When imageGen is nil (not wired), returns ErrImageGenNotImplemented.
func (g *GenerationService) GenerateSmartImage(
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
	return g.GenerateSmartImageWithAccount(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive, "", "")
}

// GenerateSmartImageWithAccount generates an AI image via the injected
// ImageGenerator port and StyleResolver. The account and projectID
// parameters are reserved for future multi-account support.
//
// Step 4 (July 2026): style resolution is now fail-closed through
// StyleResolver.Resolve. An unknown/incompatible style returns a typed
// error instead of silently falling back to no-style.
func (g *GenerationService) GenerateSmartImageWithAccount(
	ctx context.Context,
	subject string,
	topic string,
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
	_, _ string, // account, projectID — reserved
) (*asset.ImageAsset, error) {
	if g.imageGen == nil {
		return nil, ErrImageGenNotImplemented
	}

	cleanPrompt := pickImagePrompt(subject, topic, prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	// Step 4: resolve style fail-closed via StyleResolver.
	resolved, err := g.resolveStyle(style, model)
	if err != nil {
		return nil, fmt.Errorf("style resolution: %w", err)
	}

	// Step 4: compose the final prompt through PromptComposer.
	finalPrompt := promptComposer(cleanPrompt, resolved.PromptSuffix)

	// Merge resolved dimensions with caller-supplied values.
	finalWidth := width
	if finalWidth == 0 {
		finalWidth = resolved.Width
	}
	if finalWidth == 0 {
		finalWidth = 1920
	}
	finalHeight := height
	if finalHeight == 0 {
		finalHeight = resolved.Height
	}
	if finalHeight == 0 {
		finalHeight = 1080
	}

	result, err := g.imageGen.Generate(ctx, GenerateImageRequest{
		Prompt:         finalPrompt,
		NegativePrompt: resolved.NegativePrompt,
		Style:          style,
		Width:          finalWidth,
		Height:         finalHeight,
		Model:          model,
		Tags:           tags,
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}

	return g.ingestGeneratedImage(ctx, result, style, tags, skipDrive)
}

// resolveStyle is the internal helper that calls StyleResolver.Resolve
// with safe defaults. Returns a zero ResolvedStyle if the resolver is
// not wired (backward-compatible with no-style-registry paths).
func (g *GenerationService) resolveStyle(style, model string) (generation.ResolvedStyle, error) {
	if g.styles == nil {
		return generation.ResolvedStyle{}, nil
	}
	return g.styles.Resolve(style, "", model)
}

// ingestGeneratedImage ingests a GeneratedImage result into the canonical
// media_assets pipeline via ImageStorageService.IngestImage.
func (g *GenerationService) ingestGeneratedImage(
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

	return g.storage.IngestImage(
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

// TriggerPrewarm satisfies the ImageSearchService interface so the script
// job handler can request a pre-warm of the Playwright tab pool.
func (g *GenerationService) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if g.log != nil {
		g.log.Info("Google Slides: automation session tab pool prewarmed",
			zap.String("job_id", jobID),
			zap.Int("count", count))
	}
}

// imageGeneratePayload is the job payload shape for image.generate.google jobs.
type imageGeneratePayload struct {
	BatchID  string   `json:"batch_id"`
	Position int      `json:"position"`
	Prompt   string   `json:"prompt"`
	Style    string   `json:"style,omitempty"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Model    string   `json:"model,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// HandleJob processes an image.generate.google job from the worker queue.
//
// Step 4 (July 2026): style resolution is now fail-closed via
// StyleResolver.Resolve before calling ImageGenerator.Generate.
func (g *GenerationService) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	g.log.Info("handling image.generate.google job",
		zap.String("job_id", j.ID),
		zap.String("correlation_id", j.CorrelationID),
	)

	if tools.IsCancelled != nil && tools.IsCancelled() {
		return nil, fmt.Errorf("image.generate.google: job %s was cancelled before execution started", j.ID)
	}

	var payload imageGeneratePayload
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("image.generate.google: failed to unmarshal job payload: %w", err)
	}

	if payload.Prompt == "" {
		return nil, fmt.Errorf("image.generate.google: empty prompt in job payload (job_id=%s)", j.ID)
	}

	if payload.Width == 0 {
		payload.Width = 1920
	}
	if payload.Height == 0 {
		payload.Height = 1080
	}

	workspaceDir := filepath.Join("/tmp", "pipelinegen", "jobs", j.ID)
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return nil, fmt.Errorf("image.generate.google: failed to create workspace dir %s: %w", workspaceDir, err)
	}
	outputPath := filepath.Join(workspaceDir, "output.png")

	if tools.Progress != nil {
		tools.Progress(5, "Starting image generation via Chrome/Playwright")
	}

	if g.imageGen == nil {
		return nil, fmt.Errorf("image.generate.google: ImageGenerator not wired")
	}

	// Step 4: resolve style fail-closed via StyleResolver.
	resolved, err := g.resolveStyle(payload.Style, payload.Model)
	if err != nil {
		g.log.Error("image.generate.google: style resolution failed",
			zap.String("job_id", j.ID),
			zap.String("style", payload.Style),
			zap.Error(err),
		)
		return nil, fmt.Errorf("style resolution: %w", err)
	}

	// Step 4: compose the final prompt through PromptComposer.
	finalPrompt := promptComposer(payload.Prompt, resolved.PromptSuffix)

	// Merge resolved dimensions with payload values.
	finalWidth := payload.Width
	if finalWidth == 0 && resolved.Width != 0 {
		finalWidth = resolved.Width
	}
	finalHeight := payload.Height
	if finalHeight == 0 && resolved.Height != 0 {
		finalHeight = resolved.Height
	}

	result, err := g.imageGen.Generate(ctx, GenerateImageRequest{
		Prompt:         finalPrompt,
		NegativePrompt: resolved.NegativePrompt,
		Style:          payload.Style,
		Width:          finalWidth,
		Height:         finalHeight,
		Model:          payload.Model,
		Tags:           payload.Tags,
		OutputPath:     outputPath,
	})
	if err != nil {
		g.log.Error("image.generate.google: generation failed",
			zap.String("job_id", j.ID),
			zap.String("prompt", payload.Prompt),
			zap.String("output_path", outputPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("image generation failed: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(50, "Image generated, ingesting into media_assets")
	}

	asset, err := g.ingestGeneratedImage(ctx, result, payload.Style, payload.Tags, false)
	if err != nil {
		g.log.Error("image.generate.google: ingest failed",
			zap.String("job_id", j.ID),
			zap.String("output_path", outputPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("image ingest failed: %w", err)
	}

	_ = os.Remove(outputPath)

	if tools.Progress != nil {
		tools.Progress(100, "Image generation completed")
	}

	return map[string]any{
		"batch_id":       payload.BatchID,
		"position":       payload.Position,
		"prompt":         payload.Prompt,
		"style":          payload.Style,
		"asset_hash":     asset.Hash,
		"path_rel":       asset.PathRel,
		"drive_file_id":  asset.DriveFileID,
		"drive_link":     g.storage.FormatDriveLink(asset.DriveFileID),
		"description":    asset.Description,
		"workspace_path": outputPath,
		"workspace_dir":  workspaceDir,
	}, nil
}

// RegisterHandler registers the image.generate.google handler with the
// job service. Called from composition.go late-bindings.
func (g *GenerationService) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.GenerationService.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register)")
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeImageGenerateGoogle, g.HandleJob); err != nil {
		return fmt.Errorf("images.GenerationService.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeImageGenerateGoogle, err)
	}
	g.log.Info("registered image.generate.google job handler")
	return nil
}

// pickImagePrompt extracts the most specific prompt from a list.
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
