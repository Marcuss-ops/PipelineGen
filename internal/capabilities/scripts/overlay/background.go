// Package overlay owns overlay-specific policy used by the scripts capability.
package overlay

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BackgroundResolver resolves an asset into durable identity and a transient
// producer-local path. The tuple avoids a new cross-boundary identity/location
// carrier type.
type BackgroundResolver func(context.Context, string) (assetID, localPath, url, sha256, mediaType string, err error)

// ResolveBackground enriches an asset-id-only request before the timing-frozen
// plan is compiled. Color backgrounds need no resolver. Visual backgrounds
// must have a verified hash and a local or remote source, or resolution fails.
func ResolveBackground(ctx context.Context, resolve BackgroundResolver, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
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
	if resolve == nil {
		return nil, fmt.Errorf("visual background asset %q cannot be resolved: resolver is not wired", out.AssetID)
	}
	assetID, localPath, url, sha256, mediaType, err := resolve(ctx, out.AssetID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sha256) == "" {
		return nil, fmt.Errorf("visual background asset %q resolved without sha256", out.AssetID)
	}
	out.AssetID = assetID
	if out.AssetID == "" {
		out.AssetID = src.AssetID
	}
	out.LocalPath = localPath
	out.URL = url
	out.SHA256 = sha256
	if out.MediaType == "" {
		out.MediaType = mediaType
	}
	return &out, nil
}
