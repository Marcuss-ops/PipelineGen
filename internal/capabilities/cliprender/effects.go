package cliprender

import (
	"fmt"
	"math"
)

const MaxClipEffects = 12

func ValidateClipEffects(effects []ClipEffectSpec, grid *ClipGridSpec) error {
	if len(effects) > MaxClipEffects {
		return fmt.Errorf("effects cannot exceed %d entries", MaxClipEffects)
	}
	valid := func(value, min, max float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
	}
	for i, e := range effects {
		bad := func(field, bounds string) error { return fmt.Errorf("effects[%d].%s must be %s", i, field, bounds) }
		// ClipEffectSpec is intentionally a flat transport struct, but each
		// effect has a closed parameter set. Reject values that the mapper
		// would otherwise omit silently.
		unused := map[string]bool{
			"amount": e.Amount != 0, "radius": e.Radius != 0, "softness": e.Softness != 0,
			"block_size": e.BlockSize != 0, "size": e.Size != 0, "angle": e.Angle != 0,
			"intensity": e.Intensity != 0, "amplitude": e.Amplitude != 0,
			"wavelength": e.Wavelength != 0, "seed": e.Seed != 0, "animated": e.Animated,
		}
		allowed := map[string]bool{}
		switch e.Type {
		case "glitch":
			allowed["amount"], allowed["seed"] = true, true
		case "scanlines":
			allowed["amount"], allowed["wavelength"] = true, true
		case "mosaic":
			allowed["block_size"] = true
		case "chromatic_aberration":
			allowed["amount"] = true
		case "halftone":
			allowed["size"], allowed["angle"], allowed["amount"] = true, true, true
		case "vignette":
			allowed["amount"], allowed["radius"], allowed["softness"] = true, true, true
		case "gaussian_blur":
			allowed["radius"] = true
		case "noise":
			allowed["amount"], allowed["size"], allowed["seed"], allowed["animated"] = true, true, true, true
		}
		for field, supplied := range unused {
			if supplied && !allowed[field] {
				return bad(field, "omitted for effect type "+e.Type)
			}
		}
		switch e.Type {
		case "glitch", "scanlines", "mosaic", "chromatic_aberration", "halftone", "vignette", "gaussian_blur", "noise":
		default:
			return fmt.Errorf("effects[%d].type %q is unsupported", i, e.Type)
		}
		switch e.Type {
		case "glitch":
			if !valid(e.Amount, 0, 1) {
				return bad("amount", "in [0,1]")
			}
		case "scanlines":
			if !valid(e.Amount, 0, .25) {
				return bad("amount", "in [0,0.25]")
			}
			if !valid(e.Wavelength, .002, 1) {
				return bad("wavelength", "in [0.002,1]")
			}
		case "mosaic":
			if e.BlockSize < 1 || e.BlockSize > 512 {
				return bad("block_size", "in [1,512]")
			}
		case "chromatic_aberration":
			if !valid(e.Amount, -128, 128) {
				return bad("amount", "in [-128,128]")
			}
		case "halftone":
			if e.Size < 1 || e.Size > 128 {
				return bad("size", "in [1,128]")
			}
			if !valid(e.Angle, -3600, 3600) {
				return bad("angle", "finite and in [-3600,3600]")
			}
			if !valid(e.Amount, 0, 1) {
				return bad("amount", "in [0,1]")
			}
		case "vignette":
			if !valid(e.Amount, 0, 1) || !valid(e.Radius, 0, 1) || !valid(e.Softness, 0, 1) {
				return bad("amount/radius/softness", "in [0,1]")
			}
		case "gaussian_blur":
			if !valid(e.Radius, 0, 256) {
				return bad("radius", "in [0,256]")
			}
		case "noise":
			if !valid(e.Amount, 0, 1) || e.Size < 1 || e.Size > 256 {
				return bad("amount/size", "within supported noise bounds")
			}
		default:
			return fmt.Errorf("effects[%d].type %q is unsupported", i, e.Type)
		}
	}
	if grid != nil {
		if !valid(grid.SpacingPX, 4, 512) {
			return fmt.Errorf("grid.spacing_px must be in [4,512]")
		}
		if !valid(grid.LineWidthPX, .25, 32) {
			return fmt.Errorf("grid.line_width_px must be in [0.25,32]")
		}
		if !valid(grid.Opacity, 0, 1) {
			return fmt.Errorf("grid.opacity must be in [0,1]")
		}
		if len(grid.Color) != 7 || grid.Color[0] != '#' {
			return fmt.Errorf("grid.color must be #RRGGBB")
		}
		for _, c := range grid.Color[1:] {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("grid.color must be #RRGGBB")
			}
		}
	}
	return nil
}
