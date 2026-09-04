package images

import (
	"context"

	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"go.uber.org/zap"
)

// Generation compatibility surface. New generation code should import
// internal/capabilities/images/generation directly; the root keeps aliases for
// existing capability consumers while ownership moves into the leaf package.
type GenerateOptions = imggeneration.GenerateOptions
type GenerateImageRequest = imggeneration.GenerateImageRequest
type GenerateRequest = imggeneration.GenerateRequest
type GeneratedImage = imggeneration.GeneratedImage
type ImageGenerator = imggeneration.ImageGenerator
type GenerationProvider = imggeneration.Provider
type Provider = imggeneration.Provider
type GenerationProviderRegistry = imggeneration.Registry
type GenerationRegistryImpl = imggeneration.Registry
type GoogleSlidesProvider = imggeneration.GoogleSlidesProvider

const CanonicalGoogleSlidesModel = imggeneration.CanonicalGoogleSlidesModel

var (
	ErrProviderUnavailable          = imggeneration.ErrProviderUnavailable
	ErrProviderNotFound             = imggeneration.ErrProviderNotFound
	ErrNoGenerationProviderWired    = imggeneration.ErrNoGenerationProviderWired
	ErrImageGenProviderNotAvailable = imggeneration.ErrImageGenProviderNotAvailable
	ErrImageGenPermanent            = imggeneration.ErrImageGenPermanent
	ErrImageGenNetwork              = imggeneration.ErrImageGenNetwork
	ErrImageGenQuota                = imggeneration.ErrImageGenQuota
	ErrImageGenAuth                 = imggeneration.ErrImageGenAuth
	ErrImageGenNoImageCandidate     = imggeneration.ErrImageGenNoImageCandidate
	ErrImageGenBlankOrPlaceholder   = imggeneration.ErrImageGenBlankOrPlaceholder
	ErrImageGenTimeout              = imggeneration.ErrImageGenTimeout
	ErrImageGenPolicy               = imggeneration.ErrImageGenPolicy
	ErrImageGenRatioNotSelected     = imggeneration.ErrImageGenRatioNotSelected
)

type Registry interface {
	Resolve(providerID string) (Provider, error)
}

func NewGenerationProviderRegistry(log *zap.Logger, provider GenerationProvider) *GenerationProviderRegistry {
	return imggeneration.NewRegistry(log, provider)
}

func NewDefaultProviderRegistry(log *zap.Logger, generator ImageGenerator) *GenerationProviderRegistry {
	return imggeneration.NewDefaultRegistry(log, generator)
}

func NewGoogleSlidesProvider(generator ImageGenerator, log *zap.Logger) *GoogleSlidesProvider {
	return imggeneration.NewGoogleSlidesProvider(generator, log)
}

func ClassifyError(errMsg string) error {
	return imggeneration.ClassifyError(errMsg)
}

func ComputeSourceHash(provider, prompt, style string, width, height int, model string) string {
	return imggeneration.ComputeSourceHash(provider, prompt, style, width, height, model)
}

func IsRetryable(err error) bool {
	return imggeneration.IsRetryable(err)
}

func dispatchToRegistry(ctx context.Context, registry *GenerationProviderRegistry, req GenerateImageRequest) (*GeneratedImage, error) {
	return imggeneration.Dispatch(ctx, registry, req)
}

var (
	_ Provider = (*GoogleSlidesProvider)(nil)
	_ Registry = (*GenerationProviderRegistry)(nil)
	_ Registry = (*GenerationRegistryImpl)(nil)
)
