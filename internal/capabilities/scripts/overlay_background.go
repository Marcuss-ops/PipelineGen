package scriptgeneration

import (
	"context"
	"fmt"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ResolveOverlayBackground preserves the scripts capability API while
// delegating background policy to the dedicated overlay package.
func ResolveOverlayBackground(ctx context.Context, source OverlayBackgroundSource, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if source == nil {
		return capabilityoverlay.ResolveBackground(ctx, nil, src)
	}
	return capabilityoverlay.ResolveBackground(ctx, func(ctx context.Context, assetID string) (string, string, string, string, string, error) {
		asset, err := source.ResolveOverlayBackground(ctx, assetID)
		return asset.AssetID, asset.LocalPath, asset.URL, asset.SHA256, asset.MediaType, err
	}, src)
}

func (r *Runner) resolveOverlayBackground(ctx context.Context, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if r == nil {
		return nil, fmt.Errorf("visual background asset cannot be resolved: runner is nil")
	}
	return ResolveOverlayBackground(ctx, r.overlayBackgroundSource, src)
}
