package overlay

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestStyleParamsProjectsRuntimeTextAndImageValues(t *testing.T) {
	fontSize, imageWidth := 64.0, 320
	got := StyleParams(&scriptpkg.OverlayStyleSpec{
		Color: []float64{1, 0.5, 0}, FontFamily: "inter",
		Size:  &scriptpkg.OverlaySizeSpec{FontSize: &fontSize},
		Image: &scriptpkg.OverlayImageStyleSpec{Width: &imageWidth},
	})
	require.Equal(t, "#FF8000", got["style"].(map[string]any)["fill"])
	require.Equal(t, 64.0, got["font_size_px"])
	require.Equal(t, "inter", got["font_family"])
	require.Equal(t, 320, got["image_width"])
}

func TestImageFrameAndEntityImageNormalization(t *testing.T) {
	radius := 12.0
	style := &scriptpkg.OverlayImageStyleSpec{Radius: &radius}
	item := capabilityoverlay.OverlayItem{
		Kind:        string(capabilityoverlay.KindEntityImage),
		Params:      map[string]any{"radius": 12.0, "position_y": 0.2},
		Frame:       ImageFrame(style),
		ImageLayers: []capabilityoverlay.OverlayImageLayer{{Params: map[string]any{"radius": 12.0, "position_y": 0.2}, Frame: ImageFrame(style)}},
	}
	ApplyImageStyle(&item, style)
	require.Nil(t, item.Frame)
	require.NotContains(t, item.Params, "radius")
	require.NotContains(t, item.Params, "position_y")
	require.Nil(t, item.ImageLayers[0].Frame)
	require.NotContains(t, item.ImageLayers[0].Params, "radius")
	require.NotContains(t, item.ImageLayers[0].Params, "position_y")
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

func TestOverlayKindPolicies(t *testing.T) {
	require.True(t, IsRuntimeTextStyleParam("font_size_px"))
	require.False(t, IsRuntimeTextStyleParam("image_width"))
	require.True(t, IsTextOverlayKind("text_phrase"))
	require.True(t, IsTextOverlayKind("number"))
	require.False(t, IsTextOverlayKind("entity_image"))
	require.True(t, IsEntityOverlayKind("entity_image"))
	require.False(t, IsEntityOverlayKind("text_phrase"))
	require.True(t, IsImageOverlayItem(capabilityoverlay.OverlayItem{Kind: "image", AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset"}}}))
	require.False(t, IsImageOverlayItem(capabilityoverlay.OverlayItem{Kind: "image", Map: &capabilityoverlay.MapOverlay{}}))
}
