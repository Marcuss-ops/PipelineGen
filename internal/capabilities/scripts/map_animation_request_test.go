package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

func animationMapItem(pinCount int) capmodel.OverlayItem {
	pins := make([]capmodel.MapOverlayPin, 0, pinCount)
	for i := 0; i < pinCount; i++ {
		pins = append(pins, capmodel.MapOverlayPin{
			ID: "place-" + string(rune('a'+i)), Label: "Place", Scope: "city",
			Latitude: 41.9 + float64(i)*0.1, Longitude: 12.5 + float64(i)*0.1,
		})
	}
	return capmodel.OverlayItem{
		ID: "map-item", Kind: "map", DurationUS: 8_000_000,
		Map: &capmodel.MapOverlay{Pins: pins, AreaGlowRadiusKM: 3.5},
	}
}

func TestApplyRequestedMapAnimationOverridesSeededComposition(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{animationMapItem(2)}}
	require.NoError(t, applyRequestedMapAnimation(plan, "route_line"))
	require.Equal(t, "route_line", plan.Items[0].Params["map_animation"])
}

func TestApplyRequestedMapAnimationFailsClosedForIncompatiblePins(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{animationMapItem(1)}}
	err := applyRequestedMapAnimation(plan, "route_line")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot carry 1 pins")
}

func TestApplyRequestedMapAnimationLeavesRandomToTheSeededDraw(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{animationMapItem(2)}}
	require.NoError(t, applyRequestedMapAnimation(plan, "random"))
	require.NotContains(t, plan.Items[0].Params, "map_animation")
}
