// Package overlays — image_treatments.go owns the image-surface runtime
// treatments introduced with overlay_style.image.effects and .grid.
//
// Both blocks are wire-level producer intent: effects are the same bounded
// native stack clip.render selects, grid is the image counterpart of
// output.render.grid. This file is the single projection point, so the
// standalone item, its image layers and chain segments can never disagree
// about which keys the renderer receives.
package overlays

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ImageEffectParams projects the ordered image effect stack onto the
// renderer's item-level `effects` array. Only fields the selected effect
// actually accepts are emitted, and only when the payload supplied them: the
// renderer treats an absent key as "keep the native default", while a zero it
// does not accept (for example mosaic block_size 0) would fail admission.
func ImageEffectParams(effects []scriptpkg.ImageEffectSpec) []map[string]any {
	if len(effects) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(effects))
	for _, effect := range effects {
		typ := strings.TrimSpace(effect.Type)
		if typ == "" {
			continue
		}
		entry := map[string]any{"type": typ}
		switch typ {
		case "glitch":
			putNonZero(entry, "amount", effect.Amount)
			putNonZeroInt(entry, "seed", effect.Seed)
		case "scanlines":
			putNonZero(entry, "amount", effect.Amount)
			putNonZero(entry, "wavelength", effect.Wavelength)
		case "mosaic":
			if effect.BlockSize != 0 {
				entry["block_size"] = effect.BlockSize
			}
		case "chromatic_aberration":
			putNonZero(entry, "amount", effect.Amount)
		case "halftone":
			if effect.Size != 0 {
				entry["size"] = effect.Size
			}
			putNonZero(entry, "angle", effect.Angle)
			putNonZero(entry, "amount", effect.Amount)
		case "vignette":
			putNonZero(entry, "amount", effect.Amount)
			putNonZero(entry, "radius", effect.Radius)
			putNonZero(entry, "softness", effect.Softness)
		case "gaussian_blur":
			putNonZero(entry, "radius", effect.Radius)
		case "noise":
			putNonZero(entry, "amount", effect.Amount)
			if effect.Size != 0 {
				entry["size"] = effect.Size
			}
			putNonZeroInt(entry, "seed", effect.Seed)
			if effect.Animated {
				entry["animated"] = true
			}
		case "glow":
			putNonZero(entry, "radius", effect.Radius)
			putNonZero(entry, "intensity", effect.Intensity)
		case "bloom":
			putNonZero(entry, "radius", effect.Radius)
			putNonZero(entry, "intensity", effect.Intensity)
			putNonZero(entry, "threshold", effect.Threshold)
		case "lens_distortion":
			putNonZero(entry, "curvature", effect.Curvature)
			putNonZero(entry, "scale", effect.Scale)
		case "sharpen":
			putNonZero(entry, "amount", effect.Amount)
		case "banded_grain":
			putNonZero(entry, "intensity", effect.Intensity)
			putNonZero(entry, "speed", effect.Speed)
			putNonZeroInt(entry, "seed", effect.Seed)
		}
		out = append(out, entry)
	}
	return out
}

// ImageGridParams projects overlay_style.image.grid onto the renderer's
// item-level `grid` block. Unset keys stay absent so the renderer defaults
// apply; `enabled: false` is carried because it is an explicit disable.
func ImageGridParams(grid *scriptpkg.OverlayImageGridSpec) map[string]any {
	if grid == nil {
		return nil
	}
	params := map[string]any{}
	if grid.Enabled != nil {
		params["enabled"] = *grid.Enabled
	}
	if grid.Scope != "" {
		params["scope"] = grid.Scope
	}
	if grid.Style != "" {
		params["style"] = grid.Style
	}
	if grid.Color != "" {
		params["color"] = grid.Color
	}
	if grid.SpacingPX != nil {
		params["spacing_px"] = *grid.SpacingPX
	}
	if grid.LineWidthPX != nil {
		params["line_width_px"] = *grid.LineWidthPX
	}
	if grid.DotRadiusPX != nil {
		params["dot_radius_px"] = *grid.DotRadiusPX
	}
	if grid.Opacity != nil {
		params["opacity"] = *grid.Opacity
	}
	return params
}

// ImageTreatmentParams is the item-level treatment block: the keys that must
// ride the image item itself (and each chain segment) rather than its child
// layers, because the renderer decorates every base image layer from there.
func ImageTreatmentParams(style *scriptpkg.OverlayImageStyleSpec) map[string]any {
	if style == nil {
		return nil
	}
	params := map[string]any{}
	if effects := ImageEffectParams(style.Effects); len(effects) > 0 {
		params["effects"] = effects
	}
	if grid := ImageGridParams(style.Grid); len(grid) > 0 {
		params["grid"] = grid
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

// ApplyImageTreatments writes the treatment block onto one image item without
// touching its child layers: item-level effects decorate every photographic
// surface the item lowers, and the grid is bound to the item's own lifetime.
func ApplyImageTreatments(item *capmodel.OverlayItem, style *scriptpkg.OverlayImageStyleSpec) {
	if item == nil {
		return
	}
	treatments := ImageTreatmentParams(style)
	if len(treatments) == 0 {
		return
	}
	if item.Params == nil {
		item.Params = map[string]any{}
	}
	for key, value := range treatments {
		item.Params[key] = value
	}
	item.RenderKey = ""
}

// ValidateImageTreatments is the early ingress gate for the two blocks. The
// effect bounds come from cliprender's single catalog (the same validator the
// clip render boundary uses), so this function adds no second table to drift.
func ValidateImageTreatments(style *scriptpkg.OverlayImageStyleSpec) error {
	if style == nil {
		return nil
	}
	if effects := ImageEffectParams(style.Effects); len(effects) > 0 {
		specs := make([]cliprender.ClipEffectSpec, 0, len(effects))
		for _, effect := range style.Effects {
			// `none` is a wire-level reset sentinel, not an effect in the
			// cliprender catalog. OverlayStyle.Validate enforces that it is the
			// only entry and has no parameters; keep it in the projected payload
			// for the renderer, but do not send it through the effect bounds table.
			if strings.TrimSpace(effect.Type) == "none" {
				if len(style.Effects) != 1 {
					return fmt.Errorf("overlay_style.image.effects: type none cannot be combined with other effects")
				}
				continue
			}
			specs = append(specs, cliprender.ClipEffectSpec{
				Type: effect.Type, Amount: effect.Amount, Radius: effect.Radius, Softness: effect.Softness,
				BlockSize: effect.BlockSize, Size: effect.Size, Angle: effect.Angle, Intensity: effect.Intensity,
				Amplitude: effect.Amplitude, Wavelength: effect.Wavelength, Seed: effect.Seed, Animated: effect.Animated,
				Curvature: effect.Curvature, Scale: effect.Scale, Threshold: effect.Threshold, Speed: effect.Speed,
			})
		}
		if len(specs) == 0 {
			return nil
		}
		if err := cliprender.ValidateClipEffects(specs, nil); err != nil {
			return fmt.Errorf("overlay_style.image.effects: %w", err)
		}
	}
	return nil
}

func putNonZero(params map[string]any, key string, value float64) {
	if value != 0 {
		params[key] = value
	}
}

func putNonZeroInt(params map[string]any, key string, value uint32) {
	if value != 0 {
		params[key] = value
	}
}
