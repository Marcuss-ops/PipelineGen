package images

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

func classifyImageOrigin(source, generator string) asset.ImageOrigin {
	if isAIImageSource(source) || isAIImageSource(generator) {
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
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(lower, "wikipedia"):
		return asset.ProviderWikipedia
	case strings.Contains(lower, "duckduckgo"):
		return asset.ProviderDuckDuckGo
	case strings.Contains(lower, "searxng"):
		return asset.ProviderSearXNG
	case lower == "drive":
		return asset.ProviderDrive
	case strings.Contains(lower, "google-slides"), strings.Contains(lower, "google-flow"):
		return asset.ProviderGoogleSlides
	case lower == "upload":
		return asset.ProviderUpload
	default:
		return asset.ProviderUnknown
	}
}
