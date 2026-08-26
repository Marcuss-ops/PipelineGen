// Package detail keeps compatibility aliases for image provenance types.
// The canonical definitions live in kernel/asset so core asset state and
// image details share one nominal type.
package detail

import asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

type ImageOrigin = asset.ImageOrigin

type ImageProvider = asset.ImageProvider

const (
	ImageOriginRetrieved = asset.ImageOriginRetrieved
	ImageOriginGenerated = asset.ImageOriginGenerated
	ImageOriginUploaded  = asset.ImageOriginUploaded

	ProviderWikipedia        = asset.ProviderWikipedia
	ProviderWikimediaCommons = asset.ProviderWikimediaCommons
	ProviderDuckDuckGo       = asset.ProviderDuckDuckGo
	ProviderSearXNG          = asset.ProviderSearXNG
	ProviderDrive            = asset.ProviderDrive
	ProviderGoogleSlides     = asset.ProviderGoogleSlides
	ProviderNVIDIA           = asset.ProviderNVIDIA
	ProviderFlux             = asset.ProviderFlux
	ProviderUpload           = asset.ProviderUpload
	ProviderUnknown          = asset.ProviderUnknown
)
