package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// TestImageTreatmentParamsProjectEffectsAndGrid pins the item-level
// projection: the effect stack and the grid ride the image item itself, never
// its child layers, and only supplied fields are emitted.
func TestImageTreatmentParamsProjectEffectsAndGrid(t *testing.T) {
	opacity, spacing := 0.22, 64.0
	style := &scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{
			{Type: "glitch", Amount: 0.4, Seed: 7},
			{Type: "noise", Amount: 0.06, Size: 1, Animated: true},
		},
		Grid: &scriptpkg.OverlayImageGridSpec{
			Scope: "foreground", Style: "dots", Color: "#7FA8C8",
			SpacingPX: &spacing, DotRadiusPX: floatPointer(2), Opacity: &opacity,
		},
	}
	item := capmodel.OverlayItem{
		Kind:        "image",
		Params:      map[string]any{},
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "child", Params: map[string]any{}}},
	}
	ApplyImageStyle(&item, style)

	effects, ok := item.Params["effects"].([]map[string]any)
	require.True(t, ok, "standalone image must carry the effect stack: %#v", item.Params)
	require.Len(t, effects, 2)
	require.Equal(t, "glitch", effects[0]["type"])
	require.Equal(t, 0.4, effects[0]["amount"])
	require.Equal(t, uint32(7), effects[0]["seed"])
	require.Equal(t, "noise", effects[1]["type"])
	require.Equal(t, true, effects[1]["animated"])
	// Unsupplied fields stay absent so the renderer defaults apply.
	require.NotContains(t, effects[1], "seed")

	grid, ok := item.Params["grid"].(map[string]any)
	require.True(t, ok, "standalone image must carry the grid: %#v", item.Params)
	require.Equal(t, "foreground", grid["scope"])
	require.Equal(t, "dots", grid["style"])
	require.Equal(t, 64.0, grid["spacing_px"])
	require.Equal(t, 2.0, grid["dot_radius_px"])
	require.Equal(t, 0.22, grid["opacity"])

	// Item-level treatments decorate every base image layer on the renderer
	// side; they must never be duplicated onto a child's own params.
	require.NotContains(t, item.ImageLayers[0].Params, "effects")
	require.NotContains(t, item.ImageLayers[0].Params, "grid")

	// Chain segments carry the same block in their own params.
	chainParams := ImageStyleParams(style)
	require.Contains(t, chainParams, "effects")
	require.Equal(t, "foreground", chainParams["grid"].(map[string]any)["scope"])
}

// TestImageGridExplicitDisableTravels: an explicit enabled=false is an
// instruction, not an absent value, and must reach the renderer.
func TestImageGridExplicitDisableTravels(t *testing.T) {
	disabled := false
	params := ImageGridParams(&scriptpkg.OverlayImageGridSpec{Enabled: &disabled})
	require.Equal(t, false, params["enabled"])
	require.Len(t, params, 1)
}

// TestValidateImageTreatmentsUsesClipEffectCatalog proves the early gate is
// wired to cliprender's single bound table instead of a second copy.
func TestValidateImageTreatmentsUsesClipEffectCatalog(t *testing.T) {
	require.NoError(t, ValidateImageTreatments(&scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Type: "glow", Radius: 120, Intensity: 0.4}},
	}))
	err := ValidateImageTreatments(&scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Type: "glow", Radius: 120, Intensity: 99}},
	})
	require.ErrorContains(t, err, "overlay_style.image.effects")
	require.ErrorContains(t, err, "radius in [0,256] and intensity in [0,4]")

	err = ValidateImageTreatments(&scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Type: "not_an_effect"}},
	})
	require.ErrorContains(t, err, "unsupported")

	// P3c: the image surface inherits the catalog's no-op guard, so an
	// effect declared without its defining parameter fails at ingress too.
	err = ValidateImageTreatments(&scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Type: "glitch"}},
	})
	require.ErrorContains(t, err, "effects[0].amount")
	require.NoError(t, ValidateImageTreatments(&scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Type: "glitch", Amount: 0.3}},
	}))
}

// TestImageTreatmentValidationRejectsAmbiguousStacks pins the kernel-side
// structural rules: errors carry the image.effects/grid path.
func TestImageTreatmentValidationRejectsAmbiguousStacks(t *testing.T) {
	tooMany := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{}}
	for i := 0; i <= scriptpkg.MaxImageEffects; i++ {
		tooMany.Image.Effects = append(tooMany.Image.Effects, scriptpkg.ImageEffectSpec{Type: "glow"})
	}
	require.ErrorContains(t, tooMany.Validate(), "cannot exceed")

	missingType := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Amount: 0.1}},
	}}
	require.ErrorContains(t, missingType.Validate(), "effects[0].type is required")

	noneMixed := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Effects: []scriptpkg.ImageEffectSpec{{Type: "none"}, {Type: "glow", Radius: 10}},
	}}
	require.ErrorContains(t, noneMixed.Validate(), "cannot be combined")

	badScope := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Grid: &scriptpkg.OverlayImageGridSpec{Scope: "foreground_only"},
	}}
	require.ErrorContains(t, badScope.Validate(), "grid.scope")

	badSpacing := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Grid: &scriptpkg.OverlayImageGridSpec{SpacingPX: floatPointer(2)},
	}}
	require.ErrorContains(t, badSpacing.Validate(), "grid.spacing_px")

	badDotOnLines := &scriptpkg.OverlayStyleSpec{Image: &scriptpkg.OverlayImageStyleSpec{
		Grid: &scriptpkg.OverlayImageGridSpec{DotRadiusPX: floatPointer(3)},
	}}
	require.ErrorContains(t, badDotOnLines.Validate(), "dot_radius_px is valid only for dot grids")
}
