// Package scriptgeneration — overlay_map_group_materialize_test.go pins the
// scene-level map grouping boundary: the Rust planner selects and ranks map
// candidates, but the Go materializer must lower a whole scene together or
// three nearby places become three unrelated plates.
package scriptgeneration

import (
	"encoding/json"
	"sort"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func semanticMapItem(t *testing.T, id, sceneID, label string, latitude, longitude float64, startUS int64) capmodel.OverlayItem {
	t.Helper()
	// Rust emits the presentation window (camera minimum) with the item, not
	// the raw spoken span; the materializer must preserve it.
	raw := map[string]any{
		"id": id, "scene_id": sceneID, "kind": "map", "template_id": "MAP",
		"start_ms": startUS / 1000, "end_ms": (startUS + 8_000_000) / 1000,
		"start_us": startUS, "duration_us": 8_000_000,
		"map": map[string]any{
			"entity_id": "place:" + id, "label": label, "scope": "city",
			"latitude": latitude, "longitude": longitude,
		},
	}
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var item capmodel.OverlayItem
	require.NoError(t, json.Unmarshal(encoded, &item))
	return item
}

func groupedMapCanvas(groupNearby bool, maxPins int) OverlayCanvasSpec {
	return OverlayCanvasSpec{
		Width: 640, Height: 360,
		MapPresentation: &scriptpkg.MapPresentation{
			Enabled: true, GroupNearbyPlaces: groupNearby,
			MaxPinsPerGroup: maxPins, AllowRoute: true,
		},
	}
}

// groupedSemanticMapItem builds the item the Rust planner now emits: one map
// item per decided group, with its pins and their spoken schedule in params.
func groupedSemanticMapItem(t *testing.T, id, sceneID string, pins []map[string]any) capmodel.OverlayItem {
	t.Helper()
	first := pins[0]
	raw := map[string]any{
		"id": id, "scene_id": sceneID, "kind": "map", "template_id": "MAP",
		"start_ms": 0, "end_ms": 8_000, "start_us": 0, "duration_us": 8_000_000,
		"map": map[string]any{
			"entity_id": first["entity_id"], "label": first["label"], "scope": "city",
			"latitude": first["latitude"], "longitude": first["longitude"],
		},
		"params": map[string]any{
			"priority":       0.5,
			"map_group_pins": pins,
		},
	}
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var item capmodel.OverlayItem
	require.NoError(t, json.Unmarshal(encoded, &item))
	return item
}

func groupPin(id, label string, latitude, longitude float64, startUS, durationUS int64) map[string]any {
	return map[string]any{
		"entity_id": id, "label": label, "scope": "city",
		"latitude": latitude, "longitude": longitude,
		"start_us": startUS, "duration_us": durationUS,
	}
}

// The Rust planner decides membership: one item already carries the three pins
// of a beat. Go must render exactly that group — one plate, three pins — and
// must not re-group or reassign items afterwards.
func TestMaterializeEditorialMapRendersTheRustDecidedGroup(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	item := groupedSemanticMapItem(t, "rust-map-1", "scene-1", []map[string]any{
		groupPin("place:new-orleans", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000, 2_000_000),
		groupPin("place:metairie", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 3_500_000, 2_000_000),
		groupPin("place:gretna", "Gretna", newOrleansLat-0.011, newOrleansLon-0.009, 6_000_000, 2_000_000),
	})
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{item}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 5), "seed"))
	require.Len(t, plan.Items, 1, "the Rust group is one map item")
	require.Equal(t, "rust-map-1", plan.Items[0].ID, "the group keeps the Rust item identity")
	require.Len(t, plan.Items[0].Map.Pins, 3, "every pin of the group must be drawn")
	require.Equal(t, "New Orleans", plan.Items[0].Map.Pins[0].Label)
	require.NotEmpty(t, plan.Items[0].Params["map_group"])
	require.NotEmpty(t, plan.Items[0].Params["map_pin_timing"], "the spoken schedule must reach the renderer")
	require.Equal(t, int64(8_000_000), plan.Items[0].DurationUS,
		"the Rust presentation window must reach the renderer unchanged")
	require.Equal(t, plan.Items[0].StartUS+plan.Items[0].DurationUS, plan.Items[0].EndMs*1000,
		"the millisecond projection must match the microsecond window")
}

// When the Rust planner splits a beat at the pin cap it emits consecutive map
// items. Go materializes each one; it must not fuse them back together.
func TestMaterializeEditorialMapRendersRustChunks(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	first := groupedSemanticMapItem(t, "rust-map-1", "scene-1", []map[string]any{
		groupPin("place:new-orleans", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000, 2_000_000),
		groupPin("place:metairie", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 3_500_000, 2_000_000),
	})
	second := groupedSemanticMapItem(t, "rust-map-2", "scene-1", []map[string]any{
		groupPin("place:gretna", "Gretna", newOrleansLat-0.011, newOrleansLon-0.009, 6_000_000, 2_000_000),
	})
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{first, second}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(false, 2), "seed"))
	require.Len(t, plan.Items, 2, "the two Rust chunks stay two maps")
	totalPins := 0
	for _, item := range plan.Items {
		require.NotNil(t, item.Map)
		totalPins += len(item.Map.Pins)
	}
	require.Equal(t, 3, totalPins, "no place may be dropped by chunking")
}

// The Rust planner owns the presentation window. A single map arrives already
// extended to the camera minimum and Go must not re-time it.
func TestMaterializeEditorialMapKeepsTheRustPresentationWindow(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	item := semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000)
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{item}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 5), "seed"))
	require.Len(t, plan.Items, 1)
	require.Equal(t, item.StartUS, plan.Items[0].StartUS)
	require.Equal(t, item.DurationUS, plan.Items[0].DurationUS,
		"the Rust presentation window is final; Go only attaches the plate")
	require.Equal(t, item.EndMs, plan.Items[0].EndMs)
	require.Len(t, plan.Items[0].Map.Pins, 1)
}

// Go must not re-apply a tail clamp: the Rust resolver already bounded the map
// to the plan before sealing. Keeping the window here is what makes the floor
// single-owner even at the tail.
func TestMaterializeEditorialMapDoesNotRetimeTheRustWindowAtTheTail(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	item := semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000)
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{item}, DurationMS: 6_000}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 5), "seed"))
	require.Len(t, plan.Items, 1)
	require.Equal(t, item.DurationUS, plan.Items[0].DurationUS)
	require.Equal(t, item.EndMs, plan.Items[0].EndMs)
}

// Grouping returns maps in spoken order, which need not match the Rust item
// order. Binding by position swapped Firenze's plate onto Siena's item; the
// pin's entity id is the stable join.
func TestMaterializeEditorialMapBindsGroupsByPinEntityNotPosition(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		semanticMapItem(t, "rust-map-1", "scene-1", "Gretna", newOrleansLat-0.011, newOrleansLon-0.009, 6_000_000),
		semanticMapItem(t, "rust-map-2", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000),
		semanticMapItem(t, "rust-map-3", "scene-1", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 3_500_000),
	}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 1), "seed"))
	require.Len(t, plan.Items, 3)
	labels := make(map[string]string, len(plan.Items))
	for _, item := range plan.Items {
		require.NotNil(t, item.Map)
		require.Len(t, item.Map.Pins, 1)
		labels[item.ID] = item.Map.Pins[0].Label
	}
	require.Equal(t, "New Orleans", labels["rust-map-2"], "each Rust map keeps its own grounded place")
	require.Equal(t, "Metairie", labels["rust-map-3"])
	require.Equal(t, "Gretna", labels["rust-map-1"])
}

// Two map groups arrive with non-overlapping Rust windows. Materialization
// must leave both windows untouched: overlap and the readable floor are the
// Rust resolver's decision now, not a late Go clamp.
func TestMaterializeEditorialMapKeepsTheRustNonOverlappingWindows(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	first := semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000)
	second := semanticMapItem(t, "rust-map-2", "scene-1", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 9_000_000)
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{first, second}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 1), "seed"))
	require.Len(t, plan.Items, 2)
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].StartUS < plan.Items[j].StartUS })
	require.Equal(t, first.StartUS, plan.Items[0].StartUS)
	require.Equal(t, first.DurationUS, plan.Items[0].DurationUS)
	require.Equal(t, second.StartUS, plan.Items[1].StartUS)
	require.Equal(t, second.DurationUS, plan.Items[1].DurationUS)
	require.Equal(t, second.StartUS, plan.Items[0].StartUS+plan.Items[0].DurationUS)
}
