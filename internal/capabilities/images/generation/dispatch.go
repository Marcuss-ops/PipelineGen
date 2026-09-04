package generation

import "context"

// Dispatch is the single registry dispatch entry point used by sync and async
// generation flows. There is no direct-generator fallback.
func Dispatch(ctx context.Context, registry *Registry, req GenerateImageRequest) (*GeneratedImage, error) {
	if registry == nil {
		return nil, ErrNoGenerationProviderWired
	}
	return registry.Generate(ctx, req, GenerateOptions{})
}
