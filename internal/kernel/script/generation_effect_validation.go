package script

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/color"
)

const MaxClipEffects = 12

// VideoClipEffectSpec is a flat transport struct, but each effect type owns a
// closed parameter set. The tables below are the single source of truth for
// both halves of that contract: which fields a type accepts, and the bounds
// each accepted field must satisfy. A field that belongs to another effect is
// rejected instead of being silently dropped by the mapper.
type clipEffectField struct {
	name     string
	supplied func(VideoClipEffectSpec) bool
}

var clipEffectFields = []clipEffectField{
	{"amount", func(e VideoClipEffectSpec) bool { return e.Amount != 0 }},
	{"radius", func(e VideoClipEffectSpec) bool { return e.Radius != 0 }},
	{"softness", func(e VideoClipEffectSpec) bool { return e.Softness != 0 }},
	{"block_size", func(e VideoClipEffectSpec) bool { return e.BlockSize != 0 }},
	{"size", func(e VideoClipEffectSpec) bool { return e.Size != 0 }},
	{"angle", func(e VideoClipEffectSpec) bool { return e.Angle != 0 }},
	{"intensity", func(e VideoClipEffectSpec) bool { return e.Intensity != 0 }},
	{"amplitude", func(e VideoClipEffectSpec) bool { return e.Amplitude != 0 }},
	{"wavelength", func(e VideoClipEffectSpec) bool { return e.Wavelength != 0 }},
	{"seed", func(e VideoClipEffectSpec) bool { return e.Seed != 0 }},
	{"animated", func(e VideoClipEffectSpec) bool { return e.Animated }},
	{"curvature", func(e VideoClipEffectSpec) bool { return e.Curvature != 0 }},
	{"scale", func(e VideoClipEffectSpec) bool { return e.Scale != 0 }},
	{"threshold", func(e VideoClipEffectSpec) bool { return e.Threshold != 0 }},
	{"speed", func(e VideoClipEffectSpec) bool { return e.Speed != 0 }},
}

type clipEffectCheck struct {
	// name is the field (or field group) named in the error.
	name   string
	bounds string
	ok     func(VideoClipEffectSpec) bool
}

type clipEffectSpec struct {
	allowed map[string]struct{}
	checks  []clipEffectCheck
	// required names the parameters without which the type is a silent
	// no-op: the renderer maps a missing parameter to its zero default, and
	// a declared effect that draws nothing is a payload bug, not a look.
	required []string
	// requiredAny holds parameter groups where any one member supplies the
	// effect (radius OR intensity), so an unset sibling keeps its default.
	requiredAny [][]string
}

func newVideoClipEffectSpec(fields []string, checks ...clipEffectCheck) clipEffectSpec {
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

// requiring pins the parameters a type needs to be visible.
func requiring(spec clipEffectSpec, fields ...string) clipEffectSpec {
	spec.required = fields
	return spec
}

// requiringAny pins parameter groups where one member is enough.
func requiringAny(spec clipEffectSpec, groups ...[]string) clipEffectSpec {
	spec.requiredAny = groups
	return spec
}

// clipEffectSpecs is the closed catalog of supported effect types. Checks run
// in declaration order, so the first violated bound produces the same error the
// hand-written validator produced.
var clipEffectSpecs = map[string]clipEffectSpec{
	"glitch": requiring(newVideoClipEffectSpec([]string{"amount", "seed"},
		clipEffectCheck{"amount", "in [0,1]", func(e VideoClipEffectSpec) bool { return inRange(e.Amount, 0, 1) }},
	), "amount"),
	"scanlines": requiring(newVideoClipEffectSpec([]string{"amount", "wavelength"},
		clipEffectCheck{"amount", "in [0,0.25]", func(e VideoClipEffectSpec) bool { return inRange(e.Amount, 0, .25) }},
		clipEffectCheck{"wavelength", "in [0.002,1]", func(e VideoClipEffectSpec) bool { return inRange(e.Wavelength, .002, 1) }},
	), "amount", "wavelength"),
	"mosaic": requiring(newVideoClipEffectSpec([]string{"block_size"},
		clipEffectCheck{"block_size", "in [1,512]", func(e VideoClipEffectSpec) bool { return e.BlockSize >= 1 && e.BlockSize <= 512 }},
	), "block_size"),
	"chromatic_aberration": requiring(newVideoClipEffectSpec([]string{"amount"},
		clipEffectCheck{"amount", "in [-128,128]", func(e VideoClipEffectSpec) bool { return inRange(e.Amount, -128, 128) }},
	), "amount"),
	"halftone": requiring(newVideoClipEffectSpec([]string{"size", "angle", "amount"},
		clipEffectCheck{"size", "in [1,128]", func(e VideoClipEffectSpec) bool { return e.Size >= 1 && e.Size <= 128 }},
		clipEffectCheck{"angle", "finite and in [-3600,3600]", func(e VideoClipEffectSpec) bool { return inRange(e.Angle, -3600, 3600) }},
		clipEffectCheck{"amount", "in [0,1]", func(e VideoClipEffectSpec) bool { return inRange(e.Amount, 0, 1) }},
	), "size", "amount"),
	"vignette": requiring(newVideoClipEffectSpec([]string{"amount", "radius", "softness"},
		clipEffectCheck{"amount/radius/softness", "in [0,1]", func(e VideoClipEffectSpec) bool {
			return inRange(e.Amount, 0, 1) && inRange(e.Radius, 0, 1) && inRange(e.Softness, 0, 1)
		}},
	), "amount"),
	"gaussian_blur": requiring(newVideoClipEffectSpec([]string{"radius"},
		clipEffectCheck{"radius", "in [0,256]", func(e VideoClipEffectSpec) bool { return inRange(e.Radius, 0, 256) }},
	), "radius"),
	"noise": requiring(newVideoClipEffectSpec([]string{"amount", "size", "seed", "animated"},
		clipEffectCheck{"amount/size", "within supported noise bounds", func(e VideoClipEffectSpec) bool {
			return inRange(e.Amount, 0, 1) && e.Size >= 1 && e.Size <= 256
		}},
	), "amount", "size"),
	"glow": requiringAny(newVideoClipEffectSpec([]string{"radius", "intensity"},
		clipEffectCheck{"radius/intensity", "radius in [0,256] and intensity in [0,4]", func(e VideoClipEffectSpec) bool {
			return inRange(e.Radius, 0, 256) && inRange(e.Intensity, 0, 4)
		}},
	), []string{"radius", "intensity"}),
	"bloom": requiringAny(newVideoClipEffectSpec([]string{"radius", "intensity", "threshold"},
		clipEffectCheck{"radius/intensity", "radius in [0,256] and intensity in [0,4]", func(e VideoClipEffectSpec) bool {
			return inRange(e.Radius, 0, 256) && inRange(e.Intensity, 0, 4)
		}},
		clipEffectCheck{"threshold", "in [0,1]", func(e VideoClipEffectSpec) bool {
			return e.Threshold == 0 || inRange(e.Threshold, 0, 1)
		}},
	), []string{"radius", "intensity"}),
	"lens_distortion": requiringAny(newVideoClipEffectSpec([]string{"curvature", "scale"},
		clipEffectCheck{"curvature", "in [-1,1]", func(e VideoClipEffectSpec) bool { return inRange(e.Curvature, -1, 1) }},
		clipEffectCheck{"scale", "in [0.1,4]", func(e VideoClipEffectSpec) bool {
			return e.Scale == 0 || inRange(e.Scale, .1, 4)
		}},
	), []string{"curvature", "scale"}),
	"sharpen": requiring(newVideoClipEffectSpec([]string{"amount"},
		clipEffectCheck{"amount", "in [0,10]", func(e VideoClipEffectSpec) bool { return inRange(e.Amount, 0, 10) }},
	), "amount"),
	// black_and_white is the archival/opening treatment. amount 1.0 is full
	// desaturation, 0.0 leaves the clip untouched; RenderingGen lowers it to
	// Chronon's native color.saturation catalog effect.
	"black_and_white": requiring(newVideoClipEffectSpec([]string{"amount"},
		clipEffectCheck{"amount", "in [0,1]", func(e VideoClipEffectSpec) bool { return inRange(e.Amount, 0, 1) }},
	), "amount"),
	"banded_grain": requiring(newVideoClipEffectSpec([]string{"intensity", "speed", "seed"},
		clipEffectCheck{"intensity", "in [0,4]", func(e VideoClipEffectSpec) bool { return inRange(e.Intensity, 0, 4) }},
		clipEffectCheck{"speed", "in [-1000,1000]", func(e VideoClipEffectSpec) bool {
			return e.Speed == 0 || inRange(e.Speed, -1000, 1000)
		}},
	), "intensity"),
}

// fieldSupplied reports whether the payload set the named parameter, using the
// same presence rules the accepted-field table publishes (an explicit zero is
// indistinguishable from an omitted key on this flat transport).
func fieldSupplied(e VideoClipEffectSpec, name string) bool {
	for _, field := range clipEffectFields {
		if field.name == name {
			return field.supplied(e)
		}
	}
	return false
}

func inRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func ValidateVideoClipEffects(effects []VideoClipEffectSpec, grid *VideoClipGridSpec) error {
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
		// A declared effect whose defining parameter is missing renders as a
		// silent no-op (the wire default is zero), so it is rejected here
		// rather than shipped as a look that draws nothing.
		for _, name := range spec.required {
			if !fieldSupplied(e, name) {
				return bad(name, "supplied (effect \""+e.Type+"\" needs it to be visible)")
			}
		}
		for _, group := range spec.requiredAny {
			oneSupplied := false
			for _, name := range group {
				if fieldSupplied(e, name) {
					oneSupplied = true
					break
				}
			}
			if !oneSupplied {
				return bad(strings.Join(group, "/"), "supplied (effect \""+e.Type+"\" needs one of them to be visible)")
			}
		}
		for _, check := range spec.checks {
			if !check.ok(e) {
				return bad(check.name, check.bounds)
			}
		}
	}
	if grid != nil {
		if grid.Scope != "" && grid.Scope != "full_canvas" && grid.Scope != "foreground" {
			return fmt.Errorf("grid.scope must be 'full_canvas' or 'foreground'")
		}
		if grid.Style != "" && grid.Style != "lines" && grid.Style != "dots" && grid.Style != "dot_grid" {
			return fmt.Errorf("grid.style must be 'lines', 'dots' or 'dot_grid'")
		}
		if !inRange(grid.SpacingPX, 4, 512) {
			return fmt.Errorf("grid.spacing_px must be in [4,512]")
		}
		if grid.Style == "dots" || grid.Style == "dot_grid" {
			if grid.DotRadiusPX != 0 && !inRange(grid.DotRadiusPX, 0.5, 64) {
				return fmt.Errorf("grid.dot_radius_px must be in [0.5,64]")
			}
			// A dot wider than half a cell merges with its neighbours, so the
			// declared grid stops being a grid. The lowerer enforces the same
			// relation against its effective step; reject it at the edge.
			if grid.DotRadiusPX > 0 && grid.DotRadiusPX > grid.SpacingPX/2 {
				return fmt.Errorf("grid.dot_radius_px must not exceed half of grid.spacing_px")
			}
		} else {
			if !inRange(grid.LineWidthPX, .25, 32) {
				return fmt.Errorf("grid.line_width_px must be in [0.25,32]")
			}
		}
		// opacity 0 is both an allowed value and the zero value of an omitted
		// field, so a declared grid without opacity drew nothing while the
		// request looked valid. A grid that draws no pixels is a payload bug.
		if !(grid.Opacity > 0 && grid.Opacity <= 1) {
			return fmt.Errorf("grid.opacity must be in (0,1]")
		}
		if !color.IsHexRGB(grid.Color) {
			return fmt.Errorf("grid.color must be #RRGGBB")
		}
	}
	return nil
}
