// Package asset — image provenance and provider taxonomy.
//
// ImageOrigin and ImageProvider classify how an image asset entered the
// system and which concrete source produced it. These live in the canonical
// kernel/asset core (2026-08-26 decomposition) because the core
// provider_registry.go references them; the image detail package imports
// them from here rather than forming an import cycle.

package asset

// ImageOrigin classifies how an image asset entered the system.
type ImageOrigin string

const (
	ImageOriginRetrieved ImageOrigin = "retrieved"
	ImageOriginGenerated ImageOrigin = "generated"
	ImageOriginUploaded  ImageOrigin = "uploaded"
)

func (o ImageOrigin) IsValid() bool {
	switch o {
	case ImageOriginRetrieved, ImageOriginGenerated, ImageOriginUploaded:
		return true
	default:
		return false
	}
}

// ImageProvider identifies the concrete source of an image. Google Slides is
// the only provider in the generated-image territory.
type ImageProvider string

const (
	ProviderWikipedia        ImageProvider = "wikipedia"
	ProviderWikimediaCommons ImageProvider = "wikimedia_commons"
	ProviderDuckDuckGo       ImageProvider = "duckduckgo"
	ProviderSearXNG          ImageProvider = "searxng"
	ProviderDrive            ImageProvider = "drive"
	ProviderGoogleSlides     ImageProvider = "google-slides"
	ProviderNVIDIA           ImageProvider = "nvidia"
	ProviderFlux             ImageProvider = "flux"
	ProviderUpload           ImageProvider = "upload"
	ProviderUnknown          ImageProvider = "unknown"
)

func (p ImageProvider) IsValid() bool {
	switch p {
	case ProviderWikipedia, ProviderWikimediaCommons, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive,
		ProviderGoogleSlides, ProviderNVIDIA, ProviderFlux, ProviderUpload, ProviderUnknown:
		return true
	default:
		return false
	}
}

func (p ImageProvider) IsGenerated() bool {
	switch p {
	case ProviderGoogleSlides, ProviderNVIDIA, ProviderFlux:
		return true
	default:
		return false
	}
}

func (p ImageProvider) IsRetrieved() bool {
	switch p {
	case ProviderWikipedia, ProviderWikimediaCommons, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive:
		return true
	default:
		return false
	}
}

func (o ImageOrigin) String() string   { return string(o) }
func (p ImageProvider) String() string { return string(p) }
