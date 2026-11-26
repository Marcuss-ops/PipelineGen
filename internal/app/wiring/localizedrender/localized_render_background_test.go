package localizedrender

// localized_render_background_test.go — output.render.background.blur_px/zoom
// were documented, declared in the schema doc, and never wired: the projection
// stopped at the mode so the treatment vanished between script.generate and
// the clip.render plan. These tests pin the early gate and the projection.

import (
	"strings"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestLocalizedRenderRejectsInvalidBackgroundBeforeEnqueue(t *testing.T) {
	cases := []struct {
		name string
		in   scriptgeneration.LocalizedRenderInput
		want string
	}{
		{
			name: "blur on mode=asset",
			in: scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
				Background: &scriptpkg.VideoBackgroundSpec{Mode: "asset", AssetID: "bg", BlurPX: 28},
			}},
			want: "only valid for mode=blur_source",
		},
		{
			name: "blur below the certified minimum",
			in: scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
				Background: &scriptpkg.VideoBackgroundSpec{Mode: "blur_source", BlurPX: 4},
			}},
			want: "blur_px",
		},
		{
			name: "zoom above the certified maximum",
			in: scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
				Background: &scriptpkg.VideoBackgroundSpec{Mode: "blur_source", Zoom: 11},
			}},
			want: "zoom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLocalizedRenderBackground(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestLocalizedRenderAcceptsBlurSourceTreatment(t *testing.T) {
	in := scriptgeneration.LocalizedRenderInput{Render: scriptpkg.VideoRenderSpec{
		Background: &scriptpkg.VideoBackgroundSpec{Mode: "blur_source", BlurPX: 28, Zoom: 1.1},
	}}
	if err := validateLocalizedRenderBackground(in); err != nil {
		t.Fatalf("certified blur_source treatment rejected: %v", err)
	}
	if err := validateLocalizedRenderBackground(scriptgeneration.LocalizedRenderInput{}); err != nil {
		t.Fatalf("absent background must stay valid: %v", err)
	}
}
