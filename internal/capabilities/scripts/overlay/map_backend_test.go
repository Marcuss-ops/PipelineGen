package overlay

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestResolveMapBackend(t *testing.T) {
	if got := ResolveMapBackend(false, false); got != MapBackendNative {
		t.Fatalf("plain geo → native, got %q", got)
	}
	if got := ResolveMapBackend(true, false); got != MapBackendDynamic {
		t.Fatalf("camera → dynamic, got %q", got)
	}
	if got := ResolveMapBackend(false, true); got != MapBackendDynamic {
		t.Fatalf("label/composition → dynamic, got %q", got)
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
			Map: &capabilityoverlay.MapOverlay{Pins: []capabilityoverlay.MapOverlayPin{{ID: "p1"}}},
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
			if backend != MapBackendDynamic {
				t.Fatalf("map %s backend = %q, want dynamic (the single map renderer)", item.ID, backend)
			}
		}
	}
}

// TestResolveMapBackendForFailsClosedOnUncoveredRequirements proves an
// unmet requirement is a typed error, never a silent downgrade.
func TestResolveMapBackendForFailsClosedOnUncoveredRequirements(t *testing.T) {
	if _, _, err := ResolveMapBackendFor(MapRenderRequirements{StaticPlate: true, EditorialComposition: true}); err == nil {
		t.Fatal("editorial composition is not ported: resolution must fail closed")
	}
	if _, _, err := ResolveMapBackendFor(MapRenderRequirements{StaticPlate: true, DynamicCameraTour: true, PerPinTiming: true}); err == nil {
		t.Fatal("per-pin timing is not ported: resolution must fail closed")
	}
	if !NativeMapCapabilities().Satisfies(MapRenderRequirements{StaticPlate: true, CameraFlyOver: true}) {
		t.Fatal("native backend must cover certified plate + LOD camera fly-to")
	}
	if !DynamicMapCapabilities().Satisfies(MapRenderRequirements{StaticPlate: true, CameraFlyOver: true}) {
		t.Fatal("dynamic renderer must cover the ported base flyover")
	}
	if NativeMapCapabilities().Satisfies(MapRenderRequirements{StaticPlate: true, PerPinTiming: true}) {
		t.Fatal("native backend claims per-pin spoken timing it cannot render")
	}
	if DynamicMapCapabilities().Satisfies(MapRenderRequirements{StaticPlate: true, PerPinTiming: true}) {
		t.Fatal("the dynamic renderer claims per-pin spoken timing it cannot render")
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

// TestMapAnimationPlaceholdersDemandNoComposition pins the placeholder rule:
// empty, "random" and "auto" are draw knobs resolved at the render boundary,
// so they claim no composition capability. A CONCRETE id claims it and fails
// closed when nothing can draw it.
func TestMapAnimationPlaceholdersDemandNoComposition(t *testing.T) {
	for _, placeholder := range []string{"", "random", "RANDOM", "auto", " auto "} {
		item := capabilityoverlay.OverlayItem{ID: "m", Kind: "map", Map: &capabilityoverlay.MapOverlay{},
			Params: map[string]any{"map_animation": placeholder}}
		req := MapRenderRequirementsFor(item)
		if req.DynamicCameraTour || req.EditorialComposition {
			t.Fatalf("placeholder %q must claim no composition requirement, got %+v", placeholder, req)
		}
	}
	concrete := capabilityoverlay.OverlayItem{ID: "m", Kind: "map", Map: &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_animation": "pulse_marker"}}
	if req := MapRenderRequirementsFor(concrete); !req.DynamicCameraTour {
		t.Fatalf("concrete composition must claim the tour capability, got %+v", req)
	}
}

// TestNativeGalleryCompositionClaimsItsOwnCapability pins the classification
// half of the servability contract: a composition of the native documentary
// gallery (the ChrononPlugin map catalog projection) claims only its own
// capability and resolves to the single dynamic renderer — it is neither a
// camera tour nor an editorial scene, so it never rides their fail-closed
// paths.
func TestNativeGalleryCompositionClaimsItsOwnCapability(t *testing.T) {
	item := capabilityoverlay.OverlayItem{ID: "m", Kind: "map", Map: &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_animation": "thermal_heatmap_overlay"}}
	req := MapRenderRequirementsFor(item)
	if !req.NativeComposition || req.DynamicCameraTour || req.EditorialComposition {
		t.Fatalf("native gallery composition requirements = %+v", req)
	}
	backend, _, err := ResolveMapBackendFor(req)
	if err != nil || backend != MapBackendDynamic {
		t.Fatalf("native gallery composition must resolve to the dynamic renderer, got %q %v", backend, err)
	}
	// A composition the planner cannot feed (crime_scene_radar needs
	// crime_scene_radius_m) is not in the servable subset and keeps failing
	// closed with its typed error.
	dataBound := capabilityoverlay.OverlayItem{ID: "m2", Kind: "map", Map: &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_animation": "crime_scene_radar"}}
	if req := MapRenderRequirementsFor(dataBound); req.NativeComposition {
		t.Fatalf("data-bound composition must not claim the native gallery capability, got %+v", req)
	}
}

// TestPerPinTimingIsTheOneDeclinableRequirement pins the declared-degradation
// policy: grounded per-pin spoken timing data riding on every planned map is
// DECLINED (with the reason recorded) when timing is the only unmet
// requirement — the base flyover presents every place continuously. Any other
// unmet requirement keeps failing closed, and the decline never reaches a
// composition that cannot run.
func TestPerPinTimingIsTheOneDeclinableRequirement(t *testing.T) {
	timed := capabilityoverlay.OverlayItem{ID: "m", Kind: "map", Map: &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_pin_timing": map[string]map[string]any{}}}
	backend, reason, err := ResolveMapItemBackend(timed, false)
	if err != nil {
		t.Fatalf("timing-only maps must resolve through the declared decline: %v", err)
	}
	if backend != MapBackendDynamic {
		t.Fatalf("declined timing map backend = %q, want dynamic", backend)
	}
	if !strings.Contains(reason, "per-pin spoken timing declined") {
		t.Fatalf("decline must be declared in the reason, got %q", reason)
	}
	unportable := capabilityoverlay.OverlayItem{ID: "m2", Kind: "map", Map: &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_pin_timing": map[string]map[string]any{}, "map_animation": "region_zoom"}}
	if _, _, err := ResolveMapItemBackend(unportable, false); err == nil {
		t.Fatal("the decline must never cover an unportable composition")
	}
}
