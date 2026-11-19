package overlays

import (
	"fmt"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// perPlacePlateStub answers every grounded point with its OWN plate, so a group
// the Rust planner declared can only be rendered by splitting it: the stub
// cannot cover one place with another place's raster.
type perPlacePlateStub struct{}

func (perPlacePlateStub) ResolvePlate(latitude, longitude float64) (MapPlate, bool) {
	return MapPlate{
		ID: fmt.Sprintf("plate-%.4f-%.4f", latitude, longitude), License: "stub",
		Attribution: "Stub", Center: capmodel.MapCenter{Latitude: latitude, Longitude: longitude},
		Zoom: 6, Width: 5760, Height: 3240,
		Window: geodesy.CenteredOn(latitude, longitude, 6, 5760, 3240),
	}, true
}

// TestMaterializeMapPinsSplitsAGroupNoSinglePlateCovers pins the fail-closed
// guarantee that survived the move of grouping to Rust: the planner may group
// any places it wants, but a pin is only ever drawn on a raster that actually
// covers it. A group with a distant place becomes two maps, never one map with
// a pin on unrelated geography — and no place is dropped.
func TestMaterializeMapPinsSplitsAGroupNoSinglePlateCovers(t *testing.T) {
	pins := []MapCandidate{
		testMapCandidate(t, "ent_siena", "Siena", 43.3185, 11.3316, 9_132_000, 3_295_100),
		testMapCandidate(t, "ent_far", "Far", -33.86, 151.21, 12_427_100, 942_115),
	}
	items := MaterializeMapPins("scene-b1", pins, perPlacePlateStub{}, 5760, 3240, 0, 0, "seed", MapGroupConfig{MaxPinsPerGroup: 3})
	if len(items) != 2 {
		t.Fatalf("materialized items = %d, want 2 (one per certified plate)", len(items))
	}
	seen := map[string]string{}
	for _, item := range items {
		if item.Map == nil {
			t.Fatalf("item %s has no map", item.ID)
		}
		if len(item.Map.Pins) != 1 {
			t.Fatalf("item %s pins = %d, want 1", item.ID, len(item.Map.Pins))
		}
		pin := item.Map.Pins[0]
		window := geodesy.CenteredOn(item.Map.Center.Latitude, item.Map.Center.Longitude, item.Map.Zoom, item.Map.Width, item.Map.Height)
		if !window.Contains(pin.Latitude, pin.Longitude) {
			t.Fatalf("pin %s is not covered by its own plate %s", pin.ID, item.Map.SourceID)
		}
		seen[pin.ID] = item.Map.SourceID
	}
	if seen["ent_siena"] == seen["ent_far"] {
		t.Fatalf("both pins share plate %q: a distant place must not land on another place's raster", seen["ent_siena"])
	}
}

// TestMaterializeMapPinsKeepsTheWholeGroup pins the ordinary case: when one
// plate covers every pin, the group the Rust planner declared is rendered as
// one map with all its pins in spoken order.
func TestMaterializeMapPinsKeepsTheWholeGroup(t *testing.T) {
	pins := []MapCandidate{
		testMapCandidate(t, "ent_roma", "Roma", 41.8933, 12.4829, 0, 2_000_000),
		testMapCandidate(t, "ent_viterbo", "Viterbo", 42.4207, 12.1077, 2_200_000, 2_000_000),
	}
	plate := MapPlate{
		ID: "centro-plate", License: "stub", Attribution: "Stub",
		Center: capmodel.MapCenter{Latitude: 42.15, Longitude: 12.3}, Zoom: 6, Width: 5760, Height: 3240,
		Window: geodesy.CenteredOn(42.15, 12.3, 6, 5760, 3240),
	}
	items := MaterializeMapPins("scene-b1", pins, fixedPlateStub{plate}, 5760, 3240, 0, 0, "seed", MapGroupConfig{MaxPinsPerGroup: 5})
	if len(items) != 1 {
		t.Fatalf("materialized items = %d, want 1", len(items))
	}
	if got := len(items[0].Map.Pins); got != 2 {
		t.Fatalf("pins = %d, want 2", got)
	}
	if items[0].Map.Pins[0].ID != "ent_roma" || items[0].Map.Pins[1].ID != "ent_viterbo" {
		t.Fatalf("pins = %v, want spoken order", items[0].Map.Pins)
	}
}

type fixedPlateStub struct{ plate MapPlate }

func (s fixedPlateStub) ResolvePlate(_, _ float64) (MapPlate, bool) { return s.plate, true }
