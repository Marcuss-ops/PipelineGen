package adapters

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushRegistryImageSearcher adapts the shared registry to the image
// discovery port used by InternetImagesProcessor.
type VidRushRegistryImageSearcher struct {
	Registry *VidRushAssetProviderRegistry
}

func (s *VidRushRegistryImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	return s.Registry.Search(ctx, scriptpkg.VidRushProviderInternetImages, scriptports.VidRushSearchRequest{
		SegmentID: req.SegmentID, TextHash: req.TextHash, Text: req.Query, Query: req.Query, Limit: req.Limit,
	})
}
