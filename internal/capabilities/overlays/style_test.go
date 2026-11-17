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

func TestStyleParamsProjectsPerTextCardBackground(t *testing.T) {
	opacity, radius := 1.0, 8.0
	got := StyleParams(&scriptpkg.OverlayStyleSpec{
		CardBackground: &scriptpkg.OverlayCardBackgroundSpec{Color: "#000000", Opacity: &opacity, Radius: &radius, Padding: []float64{20, 12}},
	})
	style := got["style"].(map[string]any)
	plate := style["background"].(map[string]any)
	require.Equal(t, "#000000", plate["color"])
	require.Equal(t, 1.0, plate["opacity"])
	require.Equal(t, 8.0, plate["radius"])
	require.Equal(t, []float64{20, 12}, plate["padding"])
}

func TestStyleParamsCarriesExplicitZeroDisables(t *testing.T) {
	zero := 0.0
	got := StyleParams(&scriptpkg.OverlayStyleSpec{
		GlowSize: &zero, GlowColor: "#000000", StrokeSize: &zero,
	})
	// Zero must travel: the renderer clears the preset glow/stroke on it.
	require.Equal(t, zero, got["glow_size"])
	require.Equal(t, "#000000", got["glow_color"])
	require.Equal(t, zero, got["stroke_size"])

	positive := 2.0
	got = StyleParams(&scriptpkg.OverlayStyleSpec{
		GlowSize: &positive, GlowColor: "#FFFFFF", StrokeSize: &positive,
	})
	require.Equal(t, positive, got["glow_size"])
	require.Equal(t, "#FFFFFF", got["glow_color"])
	require.Equal(t, positive, got["stroke_size"])

	// A color-only override is legal and recolors the preset glow without
	// inventing a radius.
	colorOnly := StyleParams(&scriptpkg.OverlayStyleSpec{GlowColor: "#FF3355"})
	require.NotContains(t, colorOnly, "glow_size")
	require.Equal(t, "#FF3355", colorOnly["glow_color"])
}

func TestStyleParamsProjectsImageGlow(t *testing.T) {
	radius, intensity := 24.0, 0.35
	style := &scriptpkg.OverlayImageStyleSpec{Glow: &scriptpkg.OverlayImageGlowSpec{
		Radius: &radius, Intensity: &intensity, Color: "#FF3355",
	}}
	got := StyleParams(&scriptpkg.OverlayStyleSpec{Image: style})
	require.Equal(t, 24.0, got["image_glow_radius"])
	require.Equal(t, 0.35, got["image_glow_intensity"])
	require.Equal(t, "#FF3355", got["image_glow_color"])

	item := capmodel.OverlayItem{
		Kind:        "image",
		Params:      map[string]any{},
		ImageLayers: []capmodel.OverlayImageLayer{{Params: map[string]any{}}},
	}
	ApplyImageStyle(&item, style)
	// The image_glow_ prefix survives the generic image_ strip so it can
	// never overwrite a captioned item's text glow_color.
	require.Equal(t, 24.0, item.Params["image_glow_radius"])
	require.Equal(t, 0.35, item.Params["image_glow_intensity"])
	require.Equal(t, "#FF3355", item.Params["image_glow_color"])
	require.Equal(t, 24.0, item.ImageLayers[0].Params["image_glow_radius"])
	require.True(t, IsRuntimeImageParam("image_glow_radius"))

	defaults := StyleParams(&scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Glow: &scriptpkg.OverlayImageGlowSpec{},
	}})
	require.Equal(t, 24.0, defaults["image_glow_radius"])
	require.Equal(t, 0.25, defaults["image_glow_intensity"])
	require.Equal(t, "#FFFFFF", defaults["image_glow_color"])

	zero := 0.0
	disabled := StyleParams(&scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Glow: &scriptpkg.OverlayImageGlowSpec{Radius: &zero},
	}})
	require.NotContains(t, disabled, "image_glow_radius")
}

func TestOverlayImageGlowValidation(t *testing.T) {
	validRadius, validIntensity := 24.0, 0.35
	valid := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Glow: &scriptpkg.OverlayImageGlowSpec{Radius: &validRadius, Intensity: &validIntensity, Color: "#FFFFFF"},
	}}
	require.NoError(t, valid.Validate())

	badRadius := 257.0
	valid.Image.Glow.Radius = &badRadius
	require.ErrorContains(t, valid.Validate(), "image.glow.radius")

	valid.Image.Glow.Radius = &validRadius
	badIntensity := 4.1
	valid.Image.Glow.Intensity = &badIntensity
	require.ErrorContains(t, valid.Validate(), "image.glow.intensity")

	valid.Image.Glow.Intensity = &validIntensity
	valid.Image.Glow.Color = "#GG0000"
	require.ErrorContains(t, valid.Validate(), "image.glow.color")
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

func TestImageFrameShadowHonorsAuthoredBlurWithinNativeLimit(t *testing.T) {
	enabled := true
	authoredBlur := 24.0
	frame := ImageFrame(&scriptpkg.OverlayImageStyleSpec{
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: enabled, Blur: &authoredBlur},
	})
	require.NotNil(t, frame)
	require.NotNil(t, frame.Shadow)
	// A blur inside the certified ceiling is honored exactly, not reduced to
	// an internal smaller constant the payload never asked for.
	require.Equal(t, authoredBlur, frame.Shadow.BlurPX)

	// The flat image_shadow_blur key reaches frameFromImageParams and would
	// overwrite the Frame, so it carries the same authored value.
	params := StyleParams(&scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: enabled, Blur: &authoredBlur},
	}})
	require.Equal(t, authoredBlur, params["image_shadow_blur"])
}

// TestImageShadowBlurCeilingMatchesIngress pins that the projection ceiling is
// the one kernel validation enforces, so no admissible payload is clamped.
func TestImageShadowBlurCeilingMatchesIngress(t *testing.T) {
	enabled := true
	ceiling := scriptpkg.MaxImageShadowBlurPX
	if err := scriptpkg.ValidateImageStyle(&scriptpkg.OverlayImageStyleSpec{
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: enabled, Blur: &ceiling},
	}); err != nil {
		t.Fatalf("blur at the ceiling must be admitted: %v", err)
	}
	above := ceiling + 1
	if err := scriptpkg.ValidateImageStyle(&scriptpkg.OverlayImageStyleSpec{
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: enabled, Blur: &above},
	}); err == nil {
		t.Fatalf("blur above the ceiling (%g) must be rejected, not clamped", above)
	}
	frame := ImageFrame(&scriptpkg.OverlayImageStyleSpec{
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: enabled, Blur: &ceiling},
	})
	require.NotNil(t, frame)
	require.NotNil(t, frame.Shadow)
	require.Equal(t, maxImageFrameShadowBlurPX, frame.Shadow.BlurPX)
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

func TestImageBackdropProjectsOnlyForStandaloneImages(t *testing.T) {
	zoom, blur, spacing, opacity := 1.2, 24.0, 64.0, 0.12
	style := &scriptpkg.OverlayImageStyleSpec{Backdrop: &scriptpkg.OverlayImageBackdropSpec{
		Zoom: &zoom, BlurPX: &blur,
		Grid: &scriptpkg.OverlayImageBackdropGridSpec{Scope: "foreground", Color: "#7FA8C8", SpacingPX: &spacing, Opacity: &opacity},
	}}
	standalone := capmodel.OverlayItem{Kind: "image", Params: map[string]any{}}
	ApplyImageStyle(&standalone, style)
	backdrop, ok := standalone.Params["image_backdrop"].(map[string]any)
	require.True(t, ok, "standalone image must carry the backdrop block")
	require.Equal(t, 1.2, backdrop["zoom"])
	require.Equal(t, 24.0, backdrop["blur_px"])
	grid := backdrop["grid"].(map[string]any)
	require.Equal(t, "foreground", grid["scope"], "backdrop grid scope must reach the renderer")
	require.Equal(t, "#7FA8C8", grid["color"])
	require.Equal(t, 64.0, grid["spacing_px"])
	require.Equal(t, 0.12, grid["opacity"])
	require.NotContains(t, grid, "enabled", "nil enabled keeps the renderer default")

	composite := capmodel.OverlayItem{
		Kind:        "image",
		Params:      map[string]any{},
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "left", Params: map[string]any{}}},
	}
	ApplyImageStyle(&composite, style)
	require.NotContains(t, composite.Params, "image_backdrop")
	require.NotContains(t, composite.ImageLayers[0].Params, "image_backdrop")
}

func TestOverlayImageBackdropValidation(t *testing.T) {
	zoom, blur := 1.15, 18.0
	valid := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{Backdrop: &scriptpkg.OverlayImageBackdropSpec{
		Zoom: &zoom, BlurPX: &blur,
		Grid: &scriptpkg.OverlayImageBackdropGridSpec{Style: "dots", DotRadiusPX: floatPointer(2), Opacity: floatPointer(0.1)},
	}}}
	require.NoError(t, valid.Validate())

	badSpacing := 2.0
	valid.Image.Backdrop.Grid = &scriptpkg.OverlayImageBackdropGridSpec{SpacingPX: &badSpacing}
	require.ErrorContains(t, valid.Validate(), "spacing_px")

	lineWidth := 1.0
	valid.Image.Backdrop.Grid = &scriptpkg.OverlayImageBackdropGridSpec{Style: "dots", LineWidthPX: &lineWidth}
	require.ErrorContains(t, valid.Validate(), "line_width_px")

	// P3b: the backdrop grid owns the same scope vocabulary as the
	// foreground grid, so a forged scope fails instead of defaulting.
	valid.Image.Backdrop.Grid = &scriptpkg.OverlayImageBackdropGridSpec{Scope: "foreground_only"}
	require.ErrorContains(t, valid.Validate(), "grid.scope")

	dotRadius := 2.0
	valid.Image.Backdrop.Grid = &scriptpkg.OverlayImageBackdropGridSpec{Style: "lines", DotRadiusPX: &dotRadius}
	require.ErrorContains(t, valid.Validate(), "dot_radius_px")
}

func floatPointer(value float64) *float64 { return &value }

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
