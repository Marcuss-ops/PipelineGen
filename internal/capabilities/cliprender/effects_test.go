package cliprender

import (
	"strings"
	"testing"
)

// TestValidateClipEffectsRejectsInvisibleEffect pins the P3 no-op guard: a
// declared effect whose defining parameter is missing renders nothing because
// the wire default is zero, so the payload is rejected instead of being
// shipped as decoration that draws no pixels.
func TestValidateClipEffectsRejectsInvisibleEffect(t *testing.T) {
	cases := []struct {
		name    string
		effect  ClipEffectSpec
		missing string
	}{
		{"glitch without amount", ClipEffectSpec{Type: "glitch"}, "amount"},
		{"scanlines without amount", ClipEffectSpec{Type: "scanlines", Wavelength: 0.05}, "amount"},
		{"chromatic_aberration without amount", ClipEffectSpec{Type: "chromatic_aberration"}, "amount"},
		{"vignette without amount", ClipEffectSpec{Type: "vignette", Radius: 0.5}, "amount"},
		{"gaussian_blur without radius", ClipEffectSpec{Type: "gaussian_blur"}, "radius"},
		{"noise without amount", ClipEffectSpec{Type: "noise", Size: 1}, "amount"},
		{"sharpen without amount", ClipEffectSpec{Type: "sharpen"}, "amount"},
		{"banded_grain without intensity", ClipEffectSpec{Type: "banded_grain"}, "intensity"},
		{"glow without radius and intensity", ClipEffectSpec{Type: "glow"}, "radius/intensity"},
		{"bloom without radius and intensity", ClipEffectSpec{Type: "bloom", Threshold: 0.6}, "radius/intensity"},
		{"lens_distortion without curvature and scale", ClipEffectSpec{Type: "lens_distortion"}, "curvature/scale"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateClipEffects([]ClipEffectSpec{tc.effect}, nil)
			if err == nil {
				t.Fatalf("expected %s to be rejected as a no-op", tc.effect.Type)
			}
			if !strings.Contains(err.Error(), "effects[0]."+tc.missing) {
				t.Fatalf("error should name %s, got %v", tc.missing, err)
			}
			if !strings.Contains(err.Error(), tc.effect.Type) {
				t.Fatalf("error should name the effect type, got %v", err)
			}
		})
	}
}

// TestValidateClipEffectsAcceptsVisibleEffect is the other half of the
// contract: supplying the defining parameter (or one member of a required-any
// group) keeps the historical bounds checks, so the guard never rejects a look
// the renderer can actually draw.
func TestValidateClipEffectsAcceptsVisibleEffect(t *testing.T) {
	effects := []ClipEffectSpec{
		{Type: "glitch", Amount: 0.4},
		{Type: "scanlines", Amount: 0.1, Wavelength: 0.05},
		{Type: "mosaic", BlockSize: 12},
		{Type: "chromatic_aberration", Amount: 6},
		{Type: "halftone", Size: 8, Amount: 0.7},
		{Type: "vignette", Amount: 0.6, Radius: 0.4},
		{Type: "gaussian_blur", Radius: 4},
		{Type: "noise", Amount: 0.1, Size: 1},
		{Type: "glow", Intensity: 1.2},
		{Type: "bloom", Radius: 20},
		{Type: "lens_distortion", Scale: 1.1},
	}
	// The catalog has more visible types than MaxClipEffects allows in one
	// payload, so validate them in two batches.
	if err := ValidateClipEffects(effects, nil); err != nil {
		t.Fatalf("visible effects should validate, got %v", err)
	}
	if err := ValidateClipEffects([]ClipEffectSpec{
		{Type: "sharpen", Amount: 0.8},
		{Type: "banded_grain", Intensity: 0.5},
	}, nil); err != nil {
		t.Fatalf("visible effects should validate, got %v", err)
	}
}
