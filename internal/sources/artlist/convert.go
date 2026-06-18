package artlist

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// toDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *asset.MediaAsset; this function exists for compatibility
// with existing call sites and will be removed in a follow-up cleanup.
func toDomain(m *asset.MediaAsset) *asset.MediaAsset {
	return m
}

// toDomainSlice converts a slice of asset.MediaAsset to asset.MediaAsset (passthrough).
func toDomainSlice(items []asset.MediaAsset) []asset.MediaAsset {
	out := make([]asset.MediaAsset, len(items))
	copy(out, items)
	return out
}

// toDomainPtrSlice converts a slice of *asset.MediaAsset to *asset.MediaAsset (passthrough).
func toDomainPtrSlice(items []*asset.MediaAsset) []*asset.MediaAsset {
	out := make([]*asset.MediaAsset, len(items))
	copy(out, items)
	return out
}
