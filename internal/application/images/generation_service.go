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
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
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
// Step 8 (July 2026): when `registry` is non-nil, the Generate call
// routes through GenerationProviderRegistry and dispatches by
// `req.Model`. When nil, falls back to the legacy imageGen.Generate
// direct call — preserves Step 4 behaviour for tests that pre-date
// Step 8.
//
// Cross-boundary ingestion: ingestGeneratedImage calls
// ImageStorageService.IngestImage to persist generated images through
// the canonical media_assets pipeline.
type GenerationService struct {
	imageGen ImageGenerator
	styles   generation.StyleResolver // Step 4: fail-closed style resolution
	log      *zap.Logger
	storage  *ImageStorageService // for ingestGeneratedImage → IngestImage
	// registry (Step 8) dispatches GenerationProvider implementations
	// (GoogleSlidesProvider / FluxProvider / NvidiaProvider) by Model.
	// nil = legacy direct imageGen.Generate (back-compat path).
	registry *generated.GenerationProviderRegistry
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

	result, err := g.generateThroughRegistry(ctx, GenerateImageRequest{
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

// generateThroughRegistry routes the request through the Step 8
// GenerationProviderRegistry when wired, falling back to the
// canonical imageGen port for tests + pre-Step-8 wiring. The
// registry dispatches by Model to the right provider
// (GoogleSlides / Flux / Nvidia) — see generated/provider_registry.go.
func (g *GenerationService) generateThroughRegistry(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error) {
	if g.registry != nil {
		out, err := g.registry.Generate(ctx, generated.GenerateRequest{
			Prompt:         req.Prompt,
			Style:          req.Style,
			Width:          req.Width,
			Height:         req.Height,
			Model:          req.Model,
			Tags:           req.Tags,
			NegativePrompt: req.NegativePrompt,
			OutputPath:     req.OutputPath,
		}, generated.GenerateOptions{})
		if err != nil {
			return nil, err
		}
		return &GeneratedImage{
			Data:       out.Data,
			Format:     out.Format,
			Width:      out.Width,
			Height:     out.Height,
			PromptUsed: out.PromptUsed,
			Provider:   string(out.Provider),
			SourceHash: out.SourceHash,
			OutputPath: out.OutputPath,
		}, nil
	}
	return g.imageGen.Generate(ctx, req)
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

	result, err := g.generateThroughRegistry(ctx, GenerateImageRequest{
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
			zap.String("artifact_file_path", outputPath),
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
			zap.String("artifact_file_path", outputPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("image ingest failed: %w", err)
	}

	// C11 (P0 Commit 11): build the canonical Sender-side ArtifactManifest
	// sidecar instead of relying on output_path/workspace_path string keys.
	// The runner's uploadManifest reads handlerResult[ManifestKey] via
	// job.Decode and iterates manifest.Artifacts, so every Sender-side
	// upload now flows through the sidecar — drop the legacy file-map
	// emission below (workspace_path / workspace_dir / output_path).
	//
	// The local PNG file on disk at outputPath survives this function
	// return: the runner's deferred workspace.Cleanup removes it when the
	// job terminalises (via C9's per-job WorkspaceManager). Removing the
	// file here would defeat the runner's "stats the file, computes SHA,
	// uploads to Drive" cycle.
	if tools.Progress != nil {
		tools.Progress(95, "Building ArtifactManifest sidecar")
	}

	manifest, mErr := g.buildImageManifest(j.ID, payload.Position, outputPath, result.Format)
	if mErr != nil {
		g.log.Error("image.generate.google: manifest build failed",
			zap.String("job_id", j.ID),
			zap.String("artifact_file_path", outputPath),
			zap.Error(mErr),
		)
		return nil, fmt.Errorf("artifact manifest build: %w", mErr)
	}
	if vErr := manifest.Validate(); vErr != nil {
		return nil, fmt.Errorf("artifact manifest validate: %w", vErr)
	}

	if tools.Progress != nil {
		tools.Progress(100, "Image generation completed")
	}

	handlerResult := map[string]any{
		"batch_id":      payload.BatchID,
		"position":      payload.Position,
		"prompt":        payload.Prompt,
		"style":         payload.Style,
		"asset_hash":    asset.Hash,
		"path_rel":      asset.PathRel,
		"drive_file_id": asset.DriveFileID,
		"drive_link":    g.storage.FormatDriveLink(asset.DriveFileID),
		"description":   asset.Description,
	}
	handlerResult[job.ManifestKey] = manifest
	return handlerResult, nil
}

// RegisterHandler registers the image.generate.google handler with the
// job service. Called from composition.go late-bindings.
//
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, appjobs.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs.
func (g *GenerationService) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.GenerationService.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeImageGenerateGoogle, g.HandleJob); err != nil {
		return fmt.Errorf("images.GenerationService.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeImageGenerateGoogle, err)
	}
	g.log.Info("registered image.generate.google job handler")
	return nil
}

// buildImageManifest is the C11 (P0 Commit 11) helper that materialises
// the canonical Sender-side ArtifactManifest sidecar from the on-disk
// generated image. One REQUIRED kind=image artifact (per the user
// spec — "one required Image artifact"); the runner's uploadManifest
// path then uploads it via delivery.Publisher.Publish, reading
// Filename / MIMEType / SizeBytes from the manifest sidecar rather
// than from any legacy file-map keys in handlerResult.
//
// ID convention follows the canonical script.generate precedent
// (internal/application/scripts/jobs/generation_job.go::buildAndInjectManifest):
//
//	fmt.Sprintf("%s:image:%d", jobID, position)
//
// so the runner's `uploaded[ID]` map cannot collide when the same
// jobID is processed in a batch context (multiple Position values).
//
// MIMEType derives from the provider's result.Format (e.g. png/jpg/
// webp) — NOT from the local filename. Generation services may use
// a hardcoded outputPath while generating a JPEG (the provider's
// denoted format wins; the .png suffix on outputPath is a hint, not
// a contract).
func (g *GenerationService) buildImageManifest(jobID string, position int, outputPath, format string) (*job.ArtifactManifest, error) {
	if strings.TrimSpace(outputPath) == "" {
		return nil, fmt.Errorf("buildImageManifest: outputPath is empty")
	}
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("buildImageManifest: jobID is empty")
	}

	fi, statErr := os.Stat(outputPath)
	if statErr != nil {
		return nil, fmt.Errorf("buildImageManifest: stat %q: %w", outputPath, statErr)
	}
	size := fi.Size()

	sha, shaErr := job.ComputeSHA256(outputPath)
	if shaErr != nil {
		return nil, fmt.Errorf("buildImageManifest: sha256 %q: %w", outputPath, shaErr)
	}

	mimeType := "image/" + strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		mimeType = "image/png"
	}

	filename := filepath.Base(outputPath)
	if filename == "" || filename == "." || filename == "/" {
		filename = fmt.Sprintf("image-%d.png", position)
	}

	art := job.Artifact{
		ID:        fmt.Sprintf("%s:image:%d", jobID, position),
		Kind:      job.ArtifactKindImage,
		Path:      outputPath,
		Filename:  filename,
		MIMEType:  mimeType,
		SizeBytes: size,
		SHA256:    sha,
		Required:  true,
	}

	return &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "",
		JobID:         jobID,
		Artifacts:     []job.Artifact{art},
	}, nil
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
