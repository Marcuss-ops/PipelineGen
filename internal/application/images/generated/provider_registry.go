// Package generated owns the single AI-image generation backend used by
// PipelineGen.
//
// Google Slides, driven through Chrome/Playwright and Nano Banana Pro, is the
// only supported generation path. Flux and NVIDIA provider stubs were removed
// deliberately: unavailable providers must not appear in registries,
// diagnostics, model routing, or public capability surfaces.
package generated

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

const (
	// CanonicalGoogleSlidesModel is the only model accepted by the generated
	// image pipeline. Empty model values are normalized to this value for
	// backward-compatible callers that relied on provider defaults.
	CanonicalGoogleSlidesModel = "nano-banana-pro"
)

// GenerateOptions are per-call execution options. They remain separate from
// GenerateRequest so transport-only settings do not pollute the canonical
// generation request.
type GenerateOptions struct {
	Account   string
	ProjectID string
	Timeout   time.Duration
	SkipDrive bool
}

// GenerationProvider is the single backend contract for AI image generation.
// Production composition must register GoogleSlidesProvider and nothing else.
type GenerationProvider interface {
	Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error)
	Name() asset.ImageProvider
	Healthy(ctx context.Context) error
}

// GenerateRequest is the provider-facing subset of images.GenerateImageRequest.
type GenerateRequest struct {
	Prompt         string
	Style          string
	Width          int
	Height         int
	Model          string
	Tags           []string
	NegativePrompt string
	OutputPath     string
}

// GeneratedImage is the provider-facing generated image result.
type GeneratedImage struct {
	Data       []byte
	Format     string
	Width      int
	Height     int
	PromptUsed string
	Provider   asset.ImageProvider
	Model      string
	SourceHash string
	OutputPath string
}

// ErrProviderUnavailable is returned when Google Slides is not wired or is
// temporarily unavailable.
var ErrProviderUnavailable = errors.New("generated image provider unavailable")

// ErrUnsupportedModel is returned when a caller attempts to select an image
// model other than the one canonical Google Slides model.
var ErrUnsupportedModel = errors.New("generated image model unsupported; only nano-banana-pro via google-slides is available")

// GoogleSlidesProvider wraps the canonical images.ImageGenerator port. The
// production delegate is ChromeImageProvider, which drives Playwright →
// slides.new → Nano Banana Pro.
type GoogleSlidesProvider struct {
	delegate ImageGeneratorPort
	log      *zap.Logger
}

// ImageGeneratorPort is the minimal contract the registry needs to invoke the
// Chrome/Playwright backend without importing the parent images package.
type ImageGeneratorPort interface {
	Generate(ctx context.Context, req PortGenerateRequest) (*PortGeneratedImage, error)
}

// PortGenerateRequest is the adapter-level request passed to the backend.
type PortGenerateRequest struct {
	Prompt         string
	Style          string
	Width          int
	Height         int
	Model          string
	NegativePrompt string
	Tags           []string
	OutputPath     string
}

// PortGeneratedImage is the adapter-level result returned by the backend.
type PortGeneratedImage struct {
	Data       []byte
	Format     string
	Width      int
	Height     int
	PromptUsed string
	Provider   string
	Model      string
	SourceHash string
	OutputPath string
}

func NewGoogleSlidesProvider(delegate ImageGeneratorPort, log *zap.Logger) *GoogleSlidesProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &GoogleSlidesProvider{delegate: delegate, log: log}
}

func (p *GoogleSlidesProvider) Name() asset.ImageProvider { return asset.ProviderGoogleSlides }

func (p *GoogleSlidesProvider) Healthy(_ context.Context) error {
	if p == nil || p.delegate == nil {
		return fmt.Errorf("google-slides provider not wired: %w", ErrProviderUnavailable)
	}
	return nil
}

func (p *GoogleSlidesProvider) Generate(ctx context.Context, req GenerateRequest, _ GenerateOptions) (*GeneratedImage, error) {
	if p == nil || p.delegate == nil {
		return nil, fmt.Errorf("google-slides backend not wired: %w", ErrProviderUnavailable)
	}

	model, err := normalizeModel(req.Model)
	if err != nil {
		return nil, err
	}

	portOut, err := p.delegate.Generate(ctx, PortGenerateRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
		Model:          model,
		NegativePrompt: req.NegativePrompt,
		Tags:           req.Tags,
		OutputPath:     req.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("google-slides generate: %w", err)
	}
	if portOut == nil {
		return nil, fmt.Errorf("google-slides generate returned nil result: %w", ErrProviderUnavailable)
	}

	resultModel := strings.TrimSpace(portOut.Model)
	if resultModel == "" {
		resultModel = model
	}

	return &GeneratedImage{
		Data:       portOut.Data,
		Format:     portOut.Format,
		Width:      portOut.Width,
		Height:     portOut.Height,
		PromptUsed: portOut.PromptUsed,
		Provider:   asset.ProviderGoogleSlides,
		Model:      resultModel,
		SourceHash: portOut.SourceHash,
		OutputPath: portOut.OutputPath,
	}, nil
}

func normalizeModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return CanonicalGoogleSlidesModel, nil
	}
	if !strings.EqualFold(model, CanonicalGoogleSlidesModel) {
		return "", fmt.Errorf("model=%q: %w", model, ErrUnsupportedModel)
	}
	return CanonicalGoogleSlidesModel, nil
}

// GenerationProviderRegistry retains the registry seam while enforcing a
// single-provider invariant. This keeps composition/test boundaries stable
// without preserving unnecessary provider selection logic.
type GenerationProviderRegistry struct {
	provider GenerationProvider
	log      *zap.Logger
}

func NewGenerationProviderRegistry(log *zap.Logger, provider GenerationProvider) *GenerationProviderRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	return &GenerationProviderRegistry{provider: provider, log: log}
}

// NewDefaultProviderRegistry builds the only supported generation registry.
func NewDefaultProviderRegistry(log *zap.Logger, googleSlidesPort ImageGeneratorPort) *GenerationProviderRegistry {
	return NewGenerationProviderRegistry(log, NewGoogleSlidesProvider(googleSlidesPort, log))
}

func (r *GenerationProviderRegistry) Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error) {
	if r == nil || r.provider == nil {
		return nil, fmt.Errorf("google-slides provider not registered: %w", ErrProviderUnavailable)
	}
	if r.provider.Name() != asset.ProviderGoogleSlides {
		return nil, fmt.Errorf("invalid generation provider %q: only %q is allowed: %w",
			r.provider.Name(), asset.ProviderGoogleSlides, ErrProviderUnavailable)
	}

	out, err := r.provider.Generate(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	if r.log != nil {
		r.log.Info("generation provider dispatched",
			zap.String("provider", string(asset.ProviderGoogleSlides)),
			zap.String("model", CanonicalGoogleSlidesModel),
			zap.Int("bytes", len(out.Data)),
		)
	}
	return out, nil
}

// ProviderByName returns Google Slides for its canonical ID and nil for every
// other provider name.
func (r *GenerationProviderRegistry) ProviderByName(name asset.ImageProvider) GenerationProvider {
	if r == nil || r.provider == nil || name != asset.ProviderGoogleSlides {
		return nil
	}
	return r.provider
}

// Providers returns either the sole Google Slides provider or an empty list.
func (r *GenerationProviderRegistry) Providers() []GenerationProvider {
	if r == nil || r.provider == nil {
		return nil
	}
	return []GenerationProvider{r.provider}
}

// Diagnostics probes only the real Google Slides provider.
func (r *GenerationProviderRegistry) Diagnostics(ctx context.Context) map[asset.ImageProvider]error {
	out := make(map[asset.ImageProvider]error, 1)
	if r == nil || r.provider == nil {
		return out
	}
	out[asset.ProviderGoogleSlides] = r.provider.Healthy(ctx)
	return out
}

func (p *GoogleSlidesProvider) ID() string { return string(p.Name()) }

// Resolve implements the generation Registry contract. Only google-slides is
// resolvable; all former provider IDs fail closed.
func (r *GenerationProviderRegistry) Resolve(providerID string) (GenerationProvider, error) {
	if r == nil {
		return nil, errors.New("generated: nil registry")
	}
	if providerID != string(asset.ProviderGoogleSlides) || r.provider == nil {
		return nil, fmt.Errorf("%w (id=%q)", ErrProviderNotFound, providerID)
	}
	return r.provider, nil
}
