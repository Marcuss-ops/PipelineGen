package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// toAssetDomainSlice converts a slice of assets.Asset to assets.Asset (passthrough).
func toAssetDomainSlice(items []assets.Asset) []assets.Asset {
	out := make([]assets.Asset, len(items))
	copy(out, items)
	return out
}

// toAssetDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *assets.Asset; this function exists for compatibility.
func toAssetDomain(m *assets.Asset) *assets.Asset {
	return m
}
