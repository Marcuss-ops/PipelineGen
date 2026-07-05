// Package asset defines image provenance and provider taxonomy.
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
	ProviderWikipedia    ImageProvider = "wikipedia"
	ProviderDuckDuckGo   ImageProvider = "duckduckgo"
	ProviderSearXNG      ImageProvider = "searxng"
	ProviderDrive        ImageProvider = "drive"
	ProviderGoogleSlides ImageProvider = "google-slides"
	ProviderUpload       ImageProvider = "upload"
	ProviderUnknown      ImageProvider = "unknown"
)

func (p ImageProvider) IsValid() bool {
	switch p {
	case ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive,
		ProviderGoogleSlides, ProviderUpload, ProviderUnknown:
		return true
	default:
		return false
	}
}

func (p ImageProvider) IsGenerated() bool {
	return p == ProviderGoogleSlides
}

func (p ImageProvider) IsRetrieved() bool {
	switch p {
	case ProviderWikipedia, ProviderDuckDuckGo, ProviderSearXNG, ProviderDrive:
		return true
	default:
		return false
	}
}
