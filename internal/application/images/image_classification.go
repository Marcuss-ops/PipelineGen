package images

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

func classifyImageOrigin(source, generator string) asset.ImageOrigin {
	if IsAIImageSource(source) || IsAIImageSource(generator) {
		return asset.ImageOriginGenerated
	}
	if strings.EqualFold(strings.TrimSpace(source), "upload") {
		return asset.ImageOriginUploaded
	}
	return asset.ImageOriginRetrieved
}

func classifyImageProvider(source, generator string) asset.ImageProvider {
	if provider := imageProviderFromValue(source); provider != asset.ProviderUnknown {
		return provider
	}
	return imageProviderFromValue(generator)
}

func imageProviderFromValue(value string) asset.ImageProvider {
	d, ok := asset.DefaultProviderRegistry().Match(value)
	if !ok {
		return asset.ProviderUnknown
	}
	return d.ID
}
