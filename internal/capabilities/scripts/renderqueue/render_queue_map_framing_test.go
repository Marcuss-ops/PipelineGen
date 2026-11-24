package renderqueue

import (
	"testing"

	"github.com/stretchr/testify/require"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func mapPresentationForPins(t *testing.T, pins []map[string]any, level string) map[string]any {
	t.Helper()
	anchorLat, anchorLon, start, end, send := capoverlay.MapNativeCameraFraming(pins, 1920, 1080, false, 0, 0)
	require.True(t, send, "multi-place framing must send the fitted interval")
	require.Greater(t, end, 0.0)

	presentation := mapRenderPresentation{
		Pins: pins, AnchorLat: anchorLat, AnchorLon: anchorLon,
		StartZoom: start, EndZoom: end, SendZooms: true, MapLevel: level,
	}
	plan := capmodel.OverlayPlan{
		DurationMS: 9000, FPSNum: 24, FPSDen: 1,
		Items: []capmodel.OverlayItem{{
			ID: "map-1", Kind: "map", StartMs: 0, EndMs: 9000,
		}},
	}
	return presentation.payload(plan, 1920, 1080, "test", capoverlay.MapEncodeProfile{})
}

func TestMapLevelCannotCropMultiPinFit(t *testing.T) {
	pins := []map[string]any{
		{"id": "city:rome", "label": "Roma", "latitude": 41.9028, "longitude": 12.4964},
		{"id": "city:torino", "label": "Torino", "latitude": 45.0703, "longitude": 7.6869},
	}
	wire := mapPresentationForPins(t, pins, "municipality")

	_, _, start, end, _ := capoverlay.MapNativeCameraFraming(pins, 1920, 1080, false, 0, 0)
	require.Equal(t, end, wire["camera_end_zoom"], "municipality zoom would crop one of the two pins")
	require.Equal(t, start, wire["camera_start_zoom"])
	require.Equal(t, "municipality", wire["map_level"], "the requested level is still reported")
}

func TestMapLevelWiderThanMultiPinFitIsKept(t *testing.T) {
	pins := []map[string]any{
		{"id": "city:rome", "label": "Roma", "latitude": 41.9028, "longitude": 12.4964},
		{"id": "city:rome-north", "label": "Roma Nord", "latitude": 41.9200, "longitude": 12.4964},
	}
	wire := mapPresentationForPins(t, pins, "continent")

	require.Equal(t, 5.0, wire["camera_start_zoom"], "a wider level is compatible with the pin fit")
	require.Equal(t, 5.4, wire["camera_end_zoom"])
}
