package wiring

import (
	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/assets"
	infradrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// Compatibility aliases keep the composition root stable while ownership of
// the resolver lives in wiring/assets. New code should import the leaf package.
type AssetDestResolver = assetswiring.DestResolver

var (
	ErrAssetDestResolverNotWired = assetswiring.ErrDestResolverNotWired
	ErrAssetDestParentRequired   = assetswiring.ErrDestParentRequired
	ErrAssetDestSubfolderFailed  = assetswiring.ErrDestSubfolderFailed
	ErrAssetDestEmptyFolderID    = assetswiring.ErrDestEmptyFolderID
)

func NewAssetDestResolver(admin infradrive.Admin) *AssetDestResolver {
	return assetswiring.NewDestResolver(admin)
}
