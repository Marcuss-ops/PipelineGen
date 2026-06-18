package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// toAssetDomainSlice converts a slice of asset.MediaAsset to asset.MediaAsset (passthrough).
func toAssetDomainSlice(items []asset.MediaAsset) []asset.MediaAsset {
	out := make([]asset.MediaAsset, len(items))
	copy(out, items)
	return out
}

// toAssetDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *asset.MediaAsset; this function exists for compatibility.
func toAssetDomain(m *asset.MediaAsset) *asset.MediaAsset {
	return m
}
