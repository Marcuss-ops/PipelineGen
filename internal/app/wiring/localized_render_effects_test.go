package wiring

import (
	"strings"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestLocalizedRenderRejectsInvalidEffectsBeforeEnqueue pins the early schema
// gate: an unknown field or an out-of-bounds value must fail while the request
// is still being planned, before subtitle/background resolution or a render
// enqueue can make the run pay for an incompatible effect.
func TestLocalizedRenderRejectsInvalidEffectsBeforeEnqueue(t *testing.T) {
	cases := []struct {
		name string
		in   scriptgeneration.LocalizedRenderInput
		want string
	}{
		{
			name: "unknown field for effect type",
			in: scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
				Effects: []scriptpkg.VideoClipEffectSpec{{Type: "glitch", Radius: 4}},
			}},
			want: "radius",
		},
		{
			name: "out of bounds amount",
			in: scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
				Effects: []scriptpkg.VideoClipEffectSpec{{Type: "glitch", Amount: 2}},
			}},
			want: "amount",
		},
		{
			name: "invalid grid scope",
			in: scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
				Grid: &scriptpkg.VideoClipGridSpec{Scope: "scene", SpacingPX: 32, LineWidthPX: 2, Color: "#ffffff", Opacity: 0.5},
			}},
			want: "grid.scope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLocalizedRenderEffects(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestLocalizedRenderAcceptsTheCertifiedEffectCatalog(t *testing.T) {
	in := scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
		Effects: []scriptpkg.VideoClipEffectSpec{{Type: "glitch", Amount: 0.5, Seed: 7}},
		Grid:    &scriptpkg.VideoClipGridSpec{Scope: "foreground", Style: "lines", SpacingPX: 32, LineWidthPX: 2, Color: "#112233", Opacity: 0.4},
	}}
	if err := validateLocalizedRenderEffects(in); err != nil {
		t.Fatalf("valid effects rejected: %v", err)
	}
}
