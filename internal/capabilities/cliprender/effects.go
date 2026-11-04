package cliprender

import (
	"fmt"
	"math"
)

const MaxClipEffects = 12

// ClipEffectSpec is a flat transport struct, but each effect type owns a
// closed parameter set. The tables below are the single source of truth for
// both halves of that contract: which fields a type accepts, and the bounds
// each accepted field must satisfy. A field that belongs to another effect is
// rejected instead of being silently dropped by the mapper.
type clipEffectField struct {
	name     string
	supplied func(ClipEffectSpec) bool
}

var clipEffectFields = []clipEffectField{
	{"amount", func(e ClipEffectSpec) bool { return e.Amount != 0 }},
	{"radius", func(e ClipEffectSpec) bool { return e.Radius != 0 }},
	{"softness", func(e ClipEffectSpec) bool { return e.Softness != 0 }},
	{"block_size", func(e ClipEffectSpec) bool { return e.BlockSize != 0 }},
	{"size", func(e ClipEffectSpec) bool { return e.Size != 0 }},
	{"angle", func(e ClipEffectSpec) bool { return e.Angle != 0 }},
	{"intensity", func(e ClipEffectSpec) bool { return e.Intensity != 0 }},
	{"amplitude", func(e ClipEffectSpec) bool { return e.Amplitude != 0 }},
	{"wavelength", func(e ClipEffectSpec) bool { return e.Wavelength != 0 }},
	{"seed", func(e ClipEffectSpec) bool { return e.Seed != 0 }},
	{"animated", func(e ClipEffectSpec) bool { return e.Animated }},
	{"curvature", func(e ClipEffectSpec) bool { return e.Curvature != 0 }},
	{"scale", func(e ClipEffectSpec) bool { return e.Scale != 0 }},
	{"threshold", func(e ClipEffectSpec) bool { return e.Threshold != 0 }},
	{"speed", func(e ClipEffectSpec) bool { return e.Speed != 0 }},
}

type clipEffectCheck struct {
	// name is the field (or field group) named in the error.
	name   string
	bounds string
	ok     func(ClipEffectSpec) bool
}

type clipEffectSpec struct {
	allowed map[string]struct{}
	checks  []clipEffectCheck
}

func newClipEffectSpec(fields []string, checks ...clipEffectCheck) clipEffectSpec {
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	return clipEffectSpec{allowed: allowed, checks: checks}
}

func (s clipEffectSpec) allows(field string) bool {
	_, ok := s.allowed[field]
	return ok
}

// clipEffectSpecs is the closed catalog of supported effect types. Checks run
// in declaration order, so the first violated bound produces the same error the
// hand-written validator produced.
var clipEffectSpecs = map[string]clipEffectSpec{
	"glitch": newClipEffectSpec([]string{"amount", "seed"},
		clipEffectCheck{"amount", "in [0,1]", func(e ClipEffectSpec) bool { return inRange(e.Amount, 0, 1) }},
	),
	"scanlines": newClipEffectSpec([]string{"amount", "wavelength"},
		clipEffectCheck{"amount", "in [0,0.25]", func(e ClipEffectSpec) bool { return inRange(e.Amount, 0, .25) }},
		clipEffectCheck{"wavelength", "in [0.002,1]", func(e ClipEffectSpec) bool { return inRange(e.Wavelength, .002, 1) }},
	),
	"mosaic": newClipEffectSpec([]string{"block_size"},
		clipEffectCheck{"block_size", "in [1,512]", func(e ClipEffectSpec) bool { return e.BlockSize >= 1 && e.BlockSize <= 512 }},
	),
	"chromatic_aberration": newClipEffectSpec([]string{"amount"},
		clipEffectCheck{"amount", "in [-128,128]", func(e ClipEffectSpec) bool { return inRange(e.Amount, -128, 128) }},
	),
	"halftone": newClipEffectSpec([]string{"size", "angle", "amount"},
		clipEffectCheck{"size", "in [1,128]", func(e ClipEffectSpec) bool { return e.Size >= 1 && e.Size <= 128 }},
		clipEffectCheck{"angle", "finite and in [-3600,3600]", func(e ClipEffectSpec) bool { return inRange(e.Angle, -3600, 3600) }},
		clipEffectCheck{"amount", "in [0,1]", func(e ClipEffectSpec) bool { return inRange(e.Amount, 0, 1) }},
	),
	"vignette": newClipEffectSpec([]string{"amount", "radius", "softness"},
		clipEffectCheck{"amount/radius/softness", "in [0,1]", func(e ClipEffectSpec) bool {
			return inRange(e.Amount, 0, 1) && inRange(e.Radius, 0, 1) && inRange(e.Softness, 0, 1)
		}},
	),
	"gaussian_blur": newClipEffectSpec([]string{"radius"},
		clipEffectCheck{"radius", "in [0,256]", func(e ClipEffectSpec) bool { return inRange(e.Radius, 0, 256) }},
	),
	"noise": newClipEffectSpec([]string{"amount", "size", "seed", "animated"},
		clipEffectCheck{"amount/size", "within supported noise bounds", func(e ClipEffectSpec) bool {
			return inRange(e.Amount, 0, 1) && e.Size >= 1 && e.Size <= 256
		}},
	),
	"glow": newClipEffectSpec([]string{"radius", "intensity"},
		clipEffectCheck{"radius/intensity", "radius in [0,256] and intensity in [0,4]", func(e ClipEffectSpec) bool {
			return inRange(e.Radius, 0, 256) && inRange(e.Intensity, 0, 4)
		}},
	),
	"bloom": newClipEffectSpec([]string{"radius", "intensity", "threshold"},
		clipEffectCheck{"radius/intensity", "radius in [0,256] and intensity in [0,4]", func(e ClipEffectSpec) bool {
			return inRange(e.Radius, 0, 256) && inRange(e.Intensity, 0, 4)
		}},
		clipEffectCheck{"threshold", "in [0,1]", func(e ClipEffectSpec) bool {
			return e.Threshold == 0 || inRange(e.Threshold, 0, 1)
		}},
	),
	"lens_distortion": newClipEffectSpec([]string{"curvature", "scale"},
		clipEffectCheck{"curvature", "in [-1,1]", func(e ClipEffectSpec) bool { return inRange(e.Curvature, -1, 1) }},
		clipEffectCheck{"scale", "in [0.1,4]", func(e ClipEffectSpec) bool {
			return e.Scale == 0 || inRange(e.Scale, .1, 4)
		}},
	),
	"sharpen": newClipEffectSpec([]string{"amount"},
		clipEffectCheck{"amount", "in [0,10]", func(e ClipEffectSpec) bool { return inRange(e.Amount, 0, 10) }},
	),
	"banded_grain": newClipEffectSpec([]string{"intensity", "speed", "seed"},
		clipEffectCheck{"intensity", "in [0,4]", func(e ClipEffectSpec) bool { return inRange(e.Intensity, 0, 4) }},
		clipEffectCheck{"speed", "in [-1000,1000]", func(e ClipEffectSpec) bool {
			return e.Speed == 0 || inRange(e.Speed, -1000, 1000)
		}},
	),
}

func inRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func ValidateClipEffects(effects []ClipEffectSpec, grid *ClipGridSpec) error {
	if len(effects) > MaxClipEffects {
		return fmt.Errorf("effects cannot exceed %d entries", MaxClipEffects)
	}
	for i, e := range effects {
		bad := func(field, bounds string) error {
			return fmt.Errorf("effects[%d].%s must be %s", i, field, bounds)
		}
		spec, supported := clipEffectSpecs[e.Type]
		// The unknown-field check runs before the supported-type check so the
		// historical error taxonomy is preserved: a known effect with a foreign
		// field reports the field, an unknown type reports the type.
		for _, field := range clipEffectFields {
			if !field.supplied(e) {
				continue
			}
			if !supported || !spec.allows(field.name) {
				return bad(field.name, "omitted for effect type "+e.Type)
			}
		}
		if !supported {
			return fmt.Errorf("effects[%d].type %q is unsupported", i, e.Type)
		}
		for _, check := range spec.checks {
			if !check.ok(e) {
				return bad(check.name, check.bounds)
			}
		}
	}
	if grid != nil {
		if grid.Style != "" && grid.Style != "lines" && grid.Style != "dots" && grid.Style != "dot_grid" {
			return fmt.Errorf("grid.style must be 'lines' or 'dots'")
		}
		if !inRange(grid.SpacingPX, 4, 512) {
			return fmt.Errorf("grid.spacing_px must be in [4,512]")
		}
		if grid.Style == "dots" || grid.Style == "dot_grid" {
			if grid.DotRadiusPX != 0 && !inRange(grid.DotRadiusPX, 0.5, 64) {
				return fmt.Errorf("grid.dot_radius_px must be in [0.5,64]")
			}
		} else {
			if !inRange(grid.LineWidthPX, .25, 32) {
				return fmt.Errorf("grid.line_width_px must be in [0.25,32]")
			}
		}
		if !inRange(grid.Opacity, 0, 1) {
			return fmt.Errorf("grid.opacity must be in [0,1]")
		}
		if len(grid.Color) != 7 || grid.Color[0] != '#' {
			return fmt.Errorf("grid.color must be #RRGGBB")
		}
		for _, c := range grid.Color[1:] {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return fmt.Errorf("grid.color must be #RRGGBB")
			}
		}
	}
	return nil
}
