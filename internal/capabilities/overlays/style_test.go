package overlays

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStyleParamsProjectsRuntimeTextAndImageValues(t *testing.T) {
	fontSize, imageWidth := 64.0, 320
	boxWidth, boxHeight := 840, 260
	got := StyleParams(&scriptpkg.OverlayStyleSpec{
		Color: []float64{1, 0.5, 0}, FontFamily: "inter",
		Size:  &scriptpkg.OverlaySizeSpec{FontSize: &fontSize, Width: &boxWidth, Height: &boxHeight},
		Image: &scriptpkg.OverlayImageStyleSpec{Width: &imageWidth},
	})
	require.Equal(t, "#FF8000", got["style"].(map[string]any)["fill"])
	require.Equal(t, 64.0, got["font_size_px"])
	require.Equal(t, boxWidth, got["width"])
	require.Equal(t, boxHeight, got["height"])
	require.Equal(t, "inter", got["font_family"])
	require.Equal(t, 320, got["image_width"])
}

func TestStyleParamsOmitsDisabledTextEffects(t *testing.T) {
	zero := 0.0
	got := StyleParams(&scriptpkg.OverlayStyleSpec{
		GlowSize: &zero, GlowColor: "#000000", StrokeSize: &zero,
	})
	require.NotContains(t, got, "glow_size")
	require.NotContains(t, got, "glow_color")
	require.NotContains(t, got, "stroke_size")

	positive := 2.0
	got = StyleParams(&scriptpkg.OverlayStyleSpec{
		GlowSize: &positive, GlowColor: "#FFFFFF", StrokeSize: &positive,
	})
	require.Equal(t, positive, got["glow_size"])
	require.Equal(t, "#FFFFFF", got["glow_color"])
	require.Equal(t, positive, got["stroke_size"])
}

func TestImageFrameAndEntityImageNormalization(t *testing.T) {
	radius := 12.0
	style := &scriptpkg.OverlayImageStyleSpec{Radius: &radius}
	item := capmodel.OverlayItem{
		Kind:        string(capmodel.KindEntityImage),
		Params:      map[string]any{"radius": 12.0, "position_y": 0.2},
		Frame:       ImageFrame(style),
		ImageLayers: []capmodel.OverlayImageLayer{{Params: map[string]any{"radius": 12.0, "position_y": 0.2}, Frame: ImageFrame(style)}},
	}
	ApplyImageStyle(&item, style)
	// Entity portraits keep the shared image frame (and its clip radius) while
	// the legacy radius override is stripped. The planner's centered hero
	// geometry and caption-safe vertical position survive normalization.
	require.NotNil(t, item.Frame)
	require.Equal(t, 12.0, item.Frame.ClipRadiusPX)
	require.NotContains(t, item.Params, "radius")
	require.Equal(t, 0.2, item.Params["position_y"])
	require.NotNil(t, item.ImageLayers[0].Frame)
	require.Equal(t, 12.0, item.ImageLayers[0].Frame.ClipRadiusPX)
	require.NotContains(t, item.ImageLayers[0].Params, "radius")
	require.Equal(t, 0.2, item.ImageLayers[0].Params["position_y"])
}

func TestImageFrameShadowStaysWithinNativeGPUBlurLimit(t *testing.T) {
	enabled := true
	largeBlur := 24.0
	frame := ImageFrame(&scriptpkg.OverlayImageStyleSpec{
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: enabled, Blur: &largeBlur},
	})
	require.NotNil(t, frame)
	require.NotNil(t, frame.Shadow)
	require.Equal(t, 10.0, frame.Shadow.BlurPX)
}

func TestStyleParamsProjectsCursorAndBoundedImage(t *testing.T) {
	width, blink := 6.0, true
	bounded := true
	got := StyleParams(&scriptpkg.OverlayStyleSpec{
		Cursor: &scriptpkg.OverlayCursorStyleSpec{Color: "#FF3355", Width: &width, Blink: &blink},
		Image:  &scriptpkg.OverlayImageStyleSpec{Bounded: &bounded, Fit: "cover"},
	})
	require.Equal(t, "#FF3355", got["cursor_color"])
	require.Equal(t, 6.0, got["cursor_width_px"])
	require.Equal(t, true, got["cursor_blink"])
	require.Equal(t, true, got["image_bounded"])
	require.Equal(t, "cover", got["image_fit"])
	require.True(t, IsRuntimeCursorParam("cursor_color"))
	require.True(t, IsRuntimeImageParam("image_bounded"))
	require.True(t, IsRuntimeImageParam("image_border_color"))
	require.True(t, IsRuntimeTextStyleParam("cursor_width_px"))
}

func TestOverlayKindPolicies(t *testing.T) {
	require.True(t, IsRuntimeTextStyleParam("font_size_px"))
	require.False(t, IsRuntimeTextStyleParam("image_width"))
	require.True(t, IsTextOverlayKind("text_phrase"))
	require.True(t, IsTextOverlayKind("number"))
	require.False(t, IsTextOverlayKind("entity_image"))
	require.True(t, IsEntityOverlayKind("entity_image"))
	require.False(t, IsEntityOverlayKind("text_phrase"))
	require.True(t, IsAssetImageOverlayItem(capmodel.OverlayItem{Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset"}}}))
	require.False(t, IsAssetImageOverlayItem(capmodel.OverlayItem{Kind: "image", Map: &capmodel.MapOverlay{}}))
}
