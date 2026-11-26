package contract

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestNeedsGPUEffectCapabilityPreflight(t *testing.T) {
	cases := []struct {
		name  string
		image *scriptpkg.OverlayImageStyleSpec
		want  bool
	}{
		{name: "no style"},
		{name: "none sentinel", image: &scriptpkg.OverlayImageStyleSpec{Effects: []scriptpkg.ImageEffectSpec{{Type: "none"}}}},
		{name: "explicit effect", image: &scriptpkg.OverlayImageStyleSpec{Effects: []scriptpkg.ImageEffectSpec{{Type: "vignette"}}}, want: true},
		{name: "enabled shadow", image: &scriptpkg.OverlayImageStyleSpec{Shadow: &scriptpkg.OverlayShadowSpec{Enabled: true}}, want: true},
		{name: "disabled shadow", image: &scriptpkg.OverlayImageStyleSpec{Shadow: &scriptpkg.OverlayShadowSpec{Enabled: false}}},
		{name: "rounded mask", image: &scriptpkg.OverlayImageStyleSpec{Radius: floatPointer(12)}, want: true},
		{name: "glow", image: &scriptpkg.OverlayImageStyleSpec{Glow: &scriptpkg.OverlayImageGlowSpec{}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := GenerateRequest{}
			if tc.image != nil {
				request.OverlayStyle = &scriptpkg.OverlayStyleSpec{Image: tc.image}
			}
			if got := request.NeedsGPUEffectCapabilityPreflight(); got != tc.want {
				t.Fatalf("NeedsGPUEffectCapabilityPreflight() = %v, want %v", got, tc.want)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }
