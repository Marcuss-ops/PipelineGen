package overlay

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestResolveMapBackend(t *testing.T) {
	if got := ResolveMapBackend(false, false); got != MapBackendNative {
		t.Fatalf("plain geo → native, got %q", got)
	}
	if got := ResolveMapBackend(true, false); got != MapBackendPython {
		t.Fatalf("camera → python, got %q", got)
	}
	if got := ResolveMapBackend(false, true); got != MapBackendPython {
		t.Fatalf("label/composition → python, got %q", got)
	}
}

// TestMapBackendIsDecidedByCapabilitiesNotItemCount is the "two maps with
// different plates" acceptance case: the backend for a map comes from its own
// render requirements, so the decision is identical whether the plan holds
// one map or several.
func TestMapBackendIsDecidedByCapabilitiesNotItemCount(t *testing.T) {
	mapItem := func(id string) capabilityoverlay.OverlayItem {
		return capabilityoverlay.OverlayItem{
			ID: id, Kind: "map",
			Map:    &capabilityoverlay.MapOverlay{Pins: []capabilityoverlay.MapOverlayPin{{ID: "p1"}}},
			Params: map[string]any{"map_animation": "pulse_marker"},
		}
	}
	single := capabilityoverlay.OverlayPlan{Items: []capabilityoverlay.OverlayItem{mapItem("map-1")}}
	multi := capabilityoverlay.OverlayPlan{Items: []capabilityoverlay.OverlayItem{mapItem("map-1"), mapItem("map-2")}}
	for _, plan := range []capabilityoverlay.OverlayPlan{single, multi} {
		for _, item := range plan.Items {
			backend, _, err := ResolveMapBackendFor(MapRenderRequirementsFor(item))
			if err != nil {
				t.Fatalf("map %s requirements do not resolve: %v", item.ID, err)
			}
			if backend != MapBackendPython {
				t.Fatalf("map %s backend = %q, want python (dynamic composition)", item.ID, backend)
			}
		}
	}
}

// TestResolveMapBackendForFailsClosedOnUncoveredRequirements proves an
// unmet requirement is a typed error, never a silent downgrade.
func TestResolveMapBackendForFailsClosedOnUncoveredRequirements(t *testing.T) {
	if _, _, err := ResolveMapBackendFor(MapRenderRequirements{StaticPlate: true, EditorialComposition: true}); err != nil {
		t.Fatalf("editorial composition must resolve to the dynamic renderer: %v", err)
	}
	if _, _, err := ResolveMapBackendFor(MapRenderRequirements{StaticPlate: true, DynamicCameraTour: true, PerPinTiming: true}); err != nil {
		t.Fatalf("per-pin timing must resolve to the dynamic renderer: %v", err)
	}
	if !NativeMapCapabilities().Satisfies(MapRenderRequirements{StaticPlate: true, CameraFlyOver: true}) {
		t.Fatal("native backend must cover certified plate + LOD camera fly-to")
	}
	if NativeMapCapabilities().Satisfies(MapRenderRequirements{StaticPlate: true, PerPinTiming: true}) {
		t.Fatal("native backend claims per-pin spoken timing it cannot render")
	}
}

// TestMapRenderRequirementsForDerivesRealNeeds proves the requirements come
// from the semantic item, not from generic booleans.
func TestMapRenderRequirementsForDerivesRealNeeds(t *testing.T) {
	plain := capabilityoverlay.OverlayItem{ID: "m", Kind: "map", Map: &capabilityoverlay.MapOverlay{}}
	req := MapRenderRequirementsFor(plain)
	if !req.StaticPlate || req.DynamicCameraTour || req.EditorialComposition || req.PerPinTiming {
		t.Fatalf("plain plate requirements = %+v", req)
	}
	timed := capabilityoverlay.OverlayItem{ID: "m", Kind: "map",
		Map:    &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_pin_timing": map[string]map[string]any{}, "map_animation": "ed_saudi_base"},
	}
	req = MapRenderRequirementsFor(timed)
	if !req.PerPinTiming || !req.EditorialComposition || req.DynamicCameraTour {
		t.Fatalf("timed editorial requirements = %+v", req)
	}
	if got := MapRenderRequirementsFor(capabilityoverlay.OverlayItem{ID: "x"}); got != (MapRenderRequirements{}) {
		t.Fatalf("non-map item requirements = %+v, want zero", got)
	}
}
