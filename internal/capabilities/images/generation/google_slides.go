package generation

import (
	"context"
	"fmt"
	"strings"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// GoogleSlidesProvider is the only production AI-image provider. Its delegate
// implements the canonical ImageGenerator port directly.
type GoogleSlidesProvider struct {
	delegate ImageGenerator
	log      *zap.Logger
}

func NewGoogleSlidesProvider(delegate ImageGenerator, log *zap.Logger) *GoogleSlidesProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &GoogleSlidesProvider{delegate: delegate, log: log}
}

func (p *GoogleSlidesProvider) Name() detail.ImageProvider { return detail.ProviderGoogleSlides }
func (p *GoogleSlidesProvider) ID() string                 { return string(p.Name()) }

func (p *GoogleSlidesProvider) Healthy(_ context.Context) error {
	if p == nil || p.delegate == nil {
		return fmt.Errorf("google-slides provider not wired: %w", ErrProviderUnavailable)
	}
	return nil
}

func (p *GoogleSlidesProvider) Generate(ctx context.Context, req GenerateImageRequest, _ GenerateOptions) (*GeneratedImage, error) {
	if p == nil || p.delegate == nil {
		return nil, fmt.Errorf("google-slides backend not wired: %w", ErrProviderUnavailable)
	}
	out, err := p.delegate.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("google-slides generate: %w", err)
	}
	if out == nil {
		return nil, fmt.Errorf("google-slides generate returned nil result: %w", ErrProviderUnavailable)
	}
	result := *out
	result.Provider = detail.ProviderGoogleSlides
	if strings.TrimSpace(result.Model) == "" {
		result.Model = CanonicalGoogleSlidesModel
	}
	return &result, nil
}

func (p *GoogleSlidesProvider) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if p == nil || p.delegate == nil {
		return
	}
	p.delegate.TriggerPrewarm(ctx, jobID, count)
}

var _ Provider = (*GoogleSlidesProvider)(nil)
