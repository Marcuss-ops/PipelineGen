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
	raw := map[string]any{
		"id": id, "scene_id": sceneID, "kind": "map", "template_id": "MAP",
		"start_ms": startUS / 1000, "end_ms": (startUS + 2_000_000) / 1000,
		"start_us": startUS, "duration_us": 2_000_000,
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

// Three places named in quick succession share one certified flyover plate.
// Materializing each Rust item on its own produced three maps (one plate per
// place); the scene-level pass must produce ONE map group with three pins.
func TestMaterializeEditorialMapGroupsNearbyRustCandidates(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000),
		semanticMapItem(t, "rust-map-2", "scene-1", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 3_500_000),
		semanticMapItem(t, "rust-map-3", "scene-1", "Gretna", newOrleansLat-0.011, newOrleansLon-0.009, 6_000_000),
	}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 5), "seed"))
	require.Len(t, plan.Items, 1, "nearby places must fuse into one map group")
	require.Len(t, plan.Items[0].Map.Pins, 3, "every selected place must keep its pin")
	require.Equal(t, "rust-map-1", plan.Items[0].ID, "the group keeps the first Rust item identity")
	require.Equal(t, "New Orleans", plan.Items[0].Map.Pins[0].Label)
	require.NotEmpty(t, plan.Items[0].Params["map_group"])
	require.GreaterOrEqual(t, plan.Items[0].DurationUS, int64(8_000_000),
		"the merged group keeps the certified presentation window, not the 0.6 s spoken span")
	require.Equal(t, plan.Items[0].StartUS+plan.Items[0].DurationUS, plan.Items[0].EndMs*1000,
		"the millisecond projection must match the microsecond window")
}

// Grouping off is a chunking policy, not a return to one plate per place: the
// pin cap splits the same scene into consecutive groups and no place is lost.
func TestMaterializeEditorialMapChunksWhenGroupingIsOff(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000),
		semanticMapItem(t, "rust-map-2", "scene-1", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 3_500_000),
		semanticMapItem(t, "rust-map-3", "scene-1", "Gretna", newOrleansLat-0.011, newOrleansLon-0.009, 6_000_000),
	}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(false, 2), "seed"))
	require.Len(t, plan.Items, 2, "the pin cap splits the group into two consecutive chunks")
	totalPins := 0
	for _, item := range plan.Items {
		require.NotNil(t, item.Map)
		totalPins += len(item.Map.Pins)
	}
	require.Equal(t, 3, totalPins, "no place may be dropped by chunking")
}

// A single map keeps the Rust start but receives the certified presentation
// window: a spoken mention of 2 s cannot carry an 8 s camera move, and the
// renderer must not be handed a 0.6 s map flash.
func TestMaterializeEditorialMapSingleExtendsToPresentationWindow(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	item := semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000)
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{item}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 5), "seed"))
	require.Len(t, plan.Items, 1)
	require.Equal(t, item.StartUS, plan.Items[0].StartUS)
	require.GreaterOrEqual(t, plan.Items[0].DurationUS, int64(8_000_000),
		"the map presentation minimum must survive the Rust-owned selection")
	require.Len(t, plan.Items[0].Map.Pins, 1)
}

// The presentation window never outlives the plan: a mention near the tail is
// clamped to the plan extent instead of rendering past the video.
func TestMaterializeEditorialMapClampsPresentationToPlanExtent(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	item := semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000)
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{item}, DurationMS: 6_000}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 5), "seed"))
	require.Len(t, plan.Items, 1)
	require.Equal(t, int64(5_000_000), plan.Items[0].DurationUS)
	require.Equal(t, int64(6_000), plan.Items[0].EndMs)
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

// Two map groups in one scene must not composite their plates on the same
// frame: the earlier window ends where the later one starts.
func TestMaterializeEditorialMapWindowsDoNotOverlap(t *testing.T) {
	plates := plateStub{plates: newOrleansFlyoverPlates(t)}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		semanticMapItem(t, "rust-map-1", "scene-1", "New Orleans", newOrleansLat, newOrleansLon, 1_000_000),
		semanticMapItem(t, "rust-map-2", "scene-1", "Metairie", newOrleansLat+0.009, newOrleansLon+0.012, 5_000_000),
	}}

	require.NoError(t, materializeEditorialMapItems(plan, plates, groupedMapCanvas(true, 1), "seed"))
	require.Len(t, plan.Items, 2)
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].StartUS < plan.Items[j].StartUS })
	first, second := plan.Items[0], plan.Items[1]
	require.Equal(t, second.StartUS, first.StartUS+first.DurationUS)
	require.GreaterOrEqual(t, second.DurationUS, int64(8_000_000))
}
