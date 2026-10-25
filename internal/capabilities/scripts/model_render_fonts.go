package scriptgeneration

import (
	"fmt"
	"strings"

	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// runtimeFontAssets stages one queue asset per payload-selected font family
// in the plan, resolved through the single
// caprender.CanonicalFontAssetsByFamily registry. Every supported family is
// staged — never a hardcoded subset — so the family a payload selects is the
// family the worker materializes. Assets are deduplicated and stably ordered.
func runtimeFontAssets(plan capoverlay.OverlayPlan) ([]RenderQueueAsset, error) {
	seen := map[string]struct{}{}
	var assets []RenderQueueAsset
	add := func(family string) error {
		family = strings.TrimSpace(family)
		if family == "" {
			return nil
		}
		if _, dup := seen[family]; dup {
			return nil
		}
		entry, ok := caprender.CanonicalFontAssetsByFamily[family]
		if !ok {
			return fmt.Errorf("unknown runtime font family %q", family)
		}
		seen[family] = struct{}{}
		assets = append(assets, NewRenderQueueAsset(
			kernelasset.Ref{AssetID: entry.Path, SHA256: entry.SHA256},
			entry.Path, ""))
		return nil
	}
	addParams := func(params map[string]any) error {
		if family, _ := params["font_family"].(string); family != "" {
			return add(family)
		}
		return nil
	}
	for _, item := range plan.Items {
		if err := addParams(item.Params); err != nil {
			return nil, fmt.Errorf("overlay item %q: %w", item.ID, err)
		}
		if family := strings.TrimSpace(item.CaptionFontFamily); family != "" {
			if err := add(family); err != nil {
				return nil, fmt.Errorf("overlay item %q caption: %w", item.ID, err)
			}
		}
		for _, child := range item.ImageLayers {
			if err := addParams(child.Params); err != nil {
				return nil, fmt.Errorf("overlay item %q image layer: %w", item.ID, err)
			}
			if family := strings.TrimSpace(child.CaptionFontFamily); family != "" {
				if err := add(family); err != nil {
					return nil, fmt.Errorf("overlay item %q image layer caption: %w", item.ID, err)
				}
			}
		}
		if !capoverlay.IsChainItemKind(item.Kind) {
			continue
		}
		segments, ok := capoverlay.DecodeChainSegments(item.Params)
		if !ok {
			continue // malformed chains are rejected by plan validation
		}
		for _, segment := range segments {
			if err := addParams(segment.Params); err != nil {
				return nil, fmt.Errorf("overlay item %q chain segment: %w", item.ID, err)
			}
			if err := addParams(segment.Style); err != nil {
				return nil, fmt.Errorf("overlay item %q chain segment style: %w", item.ID, err)
			}
			if family := strings.TrimSpace(segment.CaptionFontFamily); family != "" {
				if err := add(family); err != nil {
					return nil, fmt.Errorf("overlay item %q chain caption: %w", item.ID, err)
				}
			}
			for _, child := range segment.ImageLayers {
				if err := addParams(child.Params); err != nil {
					return nil, fmt.Errorf("overlay item %q chain image layer: %w", item.ID, err)
				}
				if family := strings.TrimSpace(child.CaptionFontFamily); family != "" {
					if err := add(family); err != nil {
						return nil, fmt.Errorf("overlay item %q chain image layer caption: %w", item.ID, err)
					}
				}
			}
		}
	}
	return assets, nil
}
