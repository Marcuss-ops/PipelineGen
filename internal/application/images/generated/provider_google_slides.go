// Package generated (application/images/generated) — provider_google_slides.go
// holds the GoogleSlidesProvider — the only supported AI-image generation
// provider. Per PR-IMG-SPLIT-5 (July 2026), each concrete provider lives
// in its own file.
//
// GoogleSlidesProvider wraps the canonical images.ImageGenerator port.
// The production delegate is ChromeImageProvider, which drives
// Playwright → slides.new → Nano Banana Pro.
package generated

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// GoogleSlidesProvider wraps the canonical images.ImageGenerator port. The
// production delegate is ChromeImageProvider, which drives Playwright →
// slides.new → Nano Banana Pro.
type GoogleSlidesProvider struct {
	delegate ImageGeneratorPort
	log      *zap.Logger
}

// NewGoogleSlidesProvider constructs a GoogleSlidesProvider wired to the
// Chrome/Playwright backend via ImageGeneratorPort.
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

	// surface-4 (July 2026): the request no longer carries a Model
	// field. The canonical model is CanonicalGoogleSlidesModel, set
	// server-side; callers have no selection surface.
	portOut, err := p.delegate.Generate(ctx, PortGenerateRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
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

	// Backend reports the canonical model it used. Fall back to the
	// canonical constant when the backend omits the field (e.g. an
	// older adapter version) so GeneratedImage.Model is always
	// informative and consistent with surface-4's single-canonical
	// backend invariant.
	resultModel := strings.TrimSpace(portOut.Model)
	if resultModel == "" {
		resultModel = CanonicalGoogleSlidesModel
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

// TriggerPrewarm forwards the warmup signal to the underlying image generator
// port. The count parameter lets a pool warm only the requested number of
// browser workers while still keeping the Google Slides provider surface thin.
func (p *GoogleSlidesProvider) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if p == nil || p.delegate == nil {
		return
	}
	p.delegate.TriggerPrewarm(ctx, jobID, count)
}

// ID returns the canonical string ID of this provider.
func (p *GoogleSlidesProvider) ID() string { return string(p.Name()) }
