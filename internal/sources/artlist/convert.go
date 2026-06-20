package artlist

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// toDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *asset.Asset; this function exists for compatibility
// with existing call sites and will be removed in a follow-up cleanup.
func toDomain(m *asset.Asset) *asset.Asset {
	return m
}

// toDomainSlice converts a slice of asset.Asset to asset.Asset (passthrough).
func toDomainSlice(items []asset.Asset) []asset.Asset {
	out := make([]asset.Asset, len(items))
	copy(out, items)
	return out
}

// toDomainPtrSlice converts a slice of *asset.Asset to *asset.Asset (passthrough).
func toDomainPtrSlice(items []*asset.Asset) []*asset.Asset {
	out := make([]*asset.Asset, len(items))
	copy(out, items)
	return out
}
