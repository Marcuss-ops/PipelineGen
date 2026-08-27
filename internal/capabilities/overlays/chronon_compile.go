package overlays

// chronon_compile.go contains helper functions extracted from
// CompileChrononPlan to keep chronon.go under the 600-LOC strict gate.

import (
	"fmt"
	"strings"
)

// compileBackgroundLayer processes the plan's background into a ChrononLayer.
// Returns the layer and any assets it references.
func compileBackgroundLayer(plan OverlayPlan, maxEndUS int64, frameAtUS func(int64) int64) (ChrononLayer, []ChrononAsset) {
	bg := plan.Background
	if bg == nil {
		return ChrononLayer{}, nil
	}

	kind := strings.ToLower(strings.TrimSpace(bg.Kind))
	layer := ChrononLayer{
		ID:             "background",
		Type:           kind,
		BoxWidth:       plan.Width,
		BoxHeight:      plan.Height,
		Fit:            bg.Fit,
		StartFrame:     0,
		DurationFrames: frameAtUS(maxEndUS),
		Loop:           bg.Loop,
	}
	if layer.Fit == "" && kind != "color" {
		layer.Fit = "cover"
	}
	if bg.Opacity != nil {
		layer.Opacity = *bg.Opacity
	}
	if bg.Style != nil {
		layer.Style = bg.Style
	}

	var assets []ChrononAsset
	seenAssets := make(map[string]string)

	if kind == "color" {
		layer.Color = append([]float64(nil), bg.Color...)
	} else if len(bg.AssetRefs) > 0 {
		ref := bg.AssetRefs[0]
		logical := logicalAssetPath(ref.URL)
		if kind == "video" {
			layer.Source = logical
		} else {
			layer.Asset = logical
		}
		if ref.SHA256 != "" {
			seenAssets[ref.SHA256] = logical
			assets = append(assets, ChrononAsset{Hash: ref.SHA256, LogicalPath: logical})
		}
	}

	return layer, assets
}

// compileItemLayer processes a single OverlayItem into a ChrononLayer.
// Returns the layer, any assets it references, whether a font is needed,
// and any layout candidates for image layers.
func compileItemLayer(
	item OverlayItem,
	index int,
	plan OverlayPlan,
	maxEndUS int64,
	frameAtUS func(int64) int64,
	seenAssets map[string]string,
) (ChrononLayer, []ChrononAsset, bool, *imageLayoutCandidate, error) {
	spec, ok := templateRegistry[item.TemplateID]
	if !ok {
		return ChrononLayer{}, nil, false, nil, fmt.Errorf("overlay plan: template %q is not a chronon-compilable template", item.TemplateID)
	}
	if spec.Primitive == "" {
		return ChrononLayer{}, nil, false, nil, fmt.Errorf("overlay plan: template %q does not terminate in a canonical primitive (text/image/video/shape)", item.TemplateID)
	}
	if spec.Primitive == PrimitiveImage || spec.Primitive == PrimitiveVideo {
		if len(item.AssetRefs) == 0 {
			return ChrononLayer{}, nil, false, nil, fmt.Errorf("overlay plan: item %q (%s) requires an asset", item.ID, spec.Primitive)
		}
		if strings.TrimSpace(item.AssetRefs[0].URL) == "" && strings.TrimSpace(item.AssetRefs[0].SHA256) == "" {
			return ChrononLayer{}, nil, false, nil, fmt.Errorf("overlay plan: item %q (%s) requires a resolvable asset (url or content hash)", item.ID, spec.Primitive)
		}
	}

	startFrame, endFrame := itemFrameRange(item, frameAtUS)
	preset := strings.TrimSpace(item.PresetID)
	if preset == "" {
		preset = modernPresetFor(item, plan.PlanID)
	}
	presetDriven := preset != ""

	if spec.Primitive == PrimitiveText && !presetDriven {
		return ChrononLayer{}, nil, false, nil, fmt.Errorf("overlay plan: text template %q has no semantic_role → preset mapping", item.TemplateID)
	}

	layer := ChrononLayer{
		ID:             item.ID,
		Preset:         preset,
		StartFrame:     startFrame,
		DurationFrames: endFrame - startFrame,
	}

	if presetDriven {
		layer.Type = primitiveToLayerType(spec.Primitive)
	} else {
		layer.Type = spec.LayerType
		layer.Fit = spec.Fit
		layer.BoxWidth = spec.BoxWidth
		layer.BoxHeight = spec.BoxHeight
		layer.Position = spec.Position
		layer.BlendMode = spec.BlendMode
		layer.Opacity = spec.Opacity
		layer.Loop = spec.Loop
	}

	if layer.DurationFrames <= 0 {
		return ChrononLayer{}, nil, false, nil, fmt.Errorf("overlay plan: item %q compiles to a non-positive frame range", item.ID)
	}
	if strings.TrimSpace(item.Text) != "" {
		layer.Text = item.Text
	}

	needsFont := false
	if spec.Primitive == PrimitiveText {
		layer.FontAsset = &ChrononFontAsset{
			Asset:  CanonicalTextFontPath,
			Family: "DejaVu Sans",
			Weight: 700,
		}
		needsFont = true
	}
	if spec.FullCanvas {
		layer.BoxWidth = plan.Width
		layer.BoxHeight = plan.Height
		layer.StartFrame = 0
		layer.DurationFrames = frameAtUS(maxEndUS)
	}

	// Per-item params override template defaults.
	if v, ok := paramString(item.Params, "fit"); ok {
		layer.Fit = v
	}
	if v, ok := paramPosition(item.Params, "position"); ok {
		layer.Position = v
	}
	if v, ok := paramInt(item.Params, "box_width"); ok {
		layer.BoxWidth = v
	}
	if v, ok := paramInt(item.Params, "box_height"); ok {
		layer.BoxHeight = v
	}
	if v, ok := paramString(item.Params, "preset"); ok {
		layer.Preset = v
	}
	if v, ok := item.Params["style"].(map[string]any); ok {
		layer.Style = v
	}
	if v, ok := paramString(item.Params, "blend_mode"); ok {
		layer.BlendMode = v
	}
	if v, ok := paramFloatOpt(item.Params, "opacity"); ok {
		layer.Opacity = v
	}
	if v, ok := paramBool(item.Params, "loop"); ok {
		layer.Loop = v
	}

	var layoutCandidate *imageLayoutCandidate
	if pos, ok := paramString(item.Params, "position"); ok && spec.Primitive == PrimitiveImage && !spec.FullCanvas {
		layer.Position = nil
		layoutCandidate = &imageLayoutCandidate{
			layerIndex: index,
			slot:       semanticSlotFor(pos),
			boxW:       layer.BoxWidth,
			boxH:       layer.BoxHeight,
			priority:   paramFloat(item.Params, "priority"),
			startFrame: layer.StartFrame,
			endFrame:   layer.StartFrame + layer.DurationFrames,
		}
	}

	if anim, ok := paramAnimation(item.Params, "animation"); ok {
		layer.Animation = anim
	}
	if spec.Primitive == PrimitiveShape {
		if c, ok := paramColor(item.Params, "color"); ok {
			layer.Color = c
		} else {
			layer.Color = spec.Color
		}
	}

	var assets []ChrononAsset
	if len(item.AssetRefs) > 0 {
		ref := item.AssetRefs[0]
		logical := logicalAssetPath(ref.URL)
		if spec.Primitive == PrimitiveVideo {
			layer.Source = logical
		} else {
			layer.Asset = logical
		}
		if ref.SHA256 != "" {
			if _, dup := seenAssets[ref.SHA256]; !dup {
				seenAssets[ref.SHA256] = logical
				assets = append(assets, ChrononAsset{Hash: ref.SHA256, LogicalPath: logical})
			}
		}
	}

	return layer, assets, needsFont, layoutCandidate, nil
}

// itemFrameRange returns the item's [start, end) in integer frames.
func itemFrameRange(item OverlayItem, frameAtUS func(int64) int64) (int64, int64) {
	if item.DurationUS > 0 {
		return frameAtUS(item.StartUS), frameAtUS(item.StartUS + item.DurationUS)
	}
	// Convert ms to us and use the same frameAtUS for consistency.
	return frameAtUS(item.StartMs * 1000), frameAtUS(item.EndMs * 1000)
}
