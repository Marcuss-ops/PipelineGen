package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// resolveOverlayBackground enriches an asset-id-only request before the
// timing-frozen plan is compiled. A color background needs no resolver. An
// image/video background with an asset id must resolve to a verified hash and
// local/remote source, otherwise the render would silently omit the layer.
func (r *Runner) resolveOverlayBackground(ctx context.Context, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if src == nil {
		return nil, nil
	}
	out := *src
	out.Color = append([]float64(nil), src.Color...)
	if src.Style != nil {
		style := *src.Style
		out.Style = &style
	}
	kind := strings.ToLower(strings.TrimSpace(out.Kind))
	if kind == "" || kind == "color" {
		return &out, nil
	}
	if strings.TrimSpace(out.AssetID) == "" && strings.TrimSpace(out.URL) == "" && strings.TrimSpace(out.LocalPath) == "" && strings.TrimSpace(out.SHA256) == "" {
		return nil, fmt.Errorf("visual background %q has no asset identity", out.Kind)
	}
	if strings.TrimSpace(out.SHA256) != "" && (strings.TrimSpace(out.URL) != "" || strings.TrimSpace(out.LocalPath) != "") {
		return &out, nil
	}
	if strings.TrimSpace(out.AssetID) == "" {
		return nil, fmt.Errorf("visual background %q requires asset_id when sha256/source is incomplete", out.Kind)
	}
	if r == nil || r.overlayBackgroundSource == nil {
		return nil, fmt.Errorf("visual background asset %q cannot be resolved: resolver is not wired", out.AssetID)
	}
	asset, err := r.overlayBackgroundSource.ResolveOverlayBackground(ctx, out.AssetID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(asset.SHA256) == "" {
		return nil, fmt.Errorf("visual background asset %q resolved without sha256", out.AssetID)
	}
	out.AssetID = asset.AssetID
	if out.AssetID == "" {
		out.AssetID = src.AssetID
	}
	out.LocalPath = asset.LocalPath
	out.URL = asset.URL
	out.SHA256 = asset.SHA256
	if out.MediaType == "" {
		out.MediaType = asset.MediaType
	}
	return &out, nil
}
