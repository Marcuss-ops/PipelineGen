package images

import (
	"context"

	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// Root compatibility surface. Canonical generation ownership lives in
// internal/capabilities/images/generation.
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
type GenerationService = imggeneration.GenerationService
type JobHandler = imggeneration.JobHandler
type UsecaseDeps = imggeneration.UsecaseDeps
type UsecaseCommand = imggeneration.UsecaseCommand
type UsecaseOutput = imggeneration.UsecaseOutput
type SyncCommand = imggeneration.SyncCommand

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
func NewGenerationService(registry *GenerationProviderRegistry, styles StyleResolver, log *zap.Logger, storage *ImageStorageService) *GenerationService {
	return imggeneration.NewGenerationService(registry, styles, log, storage)
}
func NewJobHandler(registry *GenerationProviderRegistry, styles StyleResolver, log *zap.Logger) *JobHandler {
	return imggeneration.NewJobHandler(registry, styles, log)
}
func RunUsage(ctx context.Context, deps UsecaseDeps, cmd UsecaseCommand) (*UsecaseOutput, error) {
	return imggeneration.RunUsage(ctx, deps, cmd)
}
func GenerateSync(ctx context.Context, svc *GenerationService, cmd SyncCommand) (*detail.ImageAsset, error) {
	return imggeneration.GenerateSync(ctx, svc, cmd)
}
func ClassifyError(errMsg string) error { return imggeneration.ClassifyError(errMsg) }
func ComputeSourceHash(provider, prompt, style string, width, height int, model string) string {
	return imggeneration.ComputeSourceHash(provider, prompt, style, width, height, model)
}
func IsRetryable(err error) bool { return imggeneration.IsRetryable(err) }
func dispatchToRegistry(ctx context.Context, registry *GenerationProviderRegistry, req GenerateImageRequest) (*GeneratedImage, error) {
	return imggeneration.Dispatch(ctx, registry, req)
}

var (
	_ Provider = (*GoogleSlidesProvider)(nil)
	_ Registry = (*GenerationProviderRegistry)(nil)
)
