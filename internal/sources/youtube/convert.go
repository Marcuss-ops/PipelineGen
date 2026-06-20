package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// toAssetDomainSlice converts a slice of asset.Asset to asset.Asset (passthrough).
func toAssetDomainSlice(items []asset.Asset) []asset.Asset {
	out := make([]asset.Asset, len(items))
	copy(out, items)
	return out
}

// toAssetDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *asset.Asset; this function exists for compatibility.
func toAssetDomain(m *asset.Asset) *asset.Asset {
	return m
}
