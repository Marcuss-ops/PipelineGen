package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestApplyOverlayStyleToItemsCarriesImageGlow(t *testing.T) {
	radius, intensity := 24.0, 0.35
	items := []capmodel.OverlayItem{{
		Kind:      "image",
		AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset"}},
		Params:    map[string]any{},
		ImageLayers: []capmodel.OverlayImageLayer{{
			ID:     "portrait",
			Params: map[string]any{},
		}},
	}}
	applyPayloadOverlayStyleToItems(items, OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		Image: &scriptpkg.OverlayImageStyleSpec{Glow: &scriptpkg.OverlayImageGlowSpec{
			Radius: &radius, Intensity: &intensity, Color: "#FF3355",
		}},
	}})

	require.Equal(t, 24.0, items[0].Params["image_glow_radius"])
	require.Equal(t, 0.35, items[0].Params["image_glow_intensity"])
	require.Equal(t, "#FF3355", items[0].Params["image_glow_color"])
	require.NotContains(t, items[0].Params, "glow_radius")
	require.NotContains(t, items[0].Params, "glow_color")
	require.Equal(t, 24.0, items[0].ImageLayers[0].Params["image_glow_radius"])
	require.Equal(t, 0.35, items[0].ImageLayers[0].Params["image_glow_intensity"])
	require.Equal(t, "#FF3355", items[0].ImageLayers[0].Params["image_glow_color"])
}
