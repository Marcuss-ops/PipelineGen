package artlist

import (
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// toDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *assets.Asset; this function exists for compatibility
// with existing call sites and will be removed in a follow-up cleanup.
func toDomain(m *assets.Asset) *assets.Asset {
	return m
}

// toDomainSlice converts a slice of assets.Asset to assets.Asset (passthrough).
func toDomainSlice(items []assets.Asset) []assets.Asset {
	out := make([]assets.Asset, len(items))
	copy(out, items)
	return out
}

// toDomainPtrSlice converts a slice of *assets.Asset to *assets.Asset (passthrough).
func toDomainPtrSlice(items []*assets.Asset) []*assets.Asset {
	out := make([]*assets.Asset, len(items))
	copy(out, items)
	return out
}
