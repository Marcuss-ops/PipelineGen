package overlays

import (
	"fmt"
	"testing"
)

// groupedPlaces builds count grounded places clustered near Rome, each spoken
// one second after the previous, so they all fall inside one certified plate.
func groupedPlaces(t *testing.T, count int) []MapCandidate {
	t.Helper()
	places := make([]MapCandidate, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("city:p%d", i)
		places = append(places, groundedCandidate(t, id, fmt.Sprintf("Place %d", i),
			romeLat+0.01*float64(i), romeLon+0.01*float64(i),
			int64(i)*1_000_000, 500_000))
	}
	return places
}

// TestStaticMapGroupSplitsAboveFivePinsWithoutDroppingPlaces proves that six
// places sharing one static plate become consecutive map items of at most five
// pins each, and that every grounded place is still shown exactly once.
func TestStaticMapGroupSplitsAboveFivePinsWithoutDroppingPlaces(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	places := groupedPlaces(t, 6)
	plans := mapPlansForScene(stubPlateResolver{plates: []MapPlate{plate}}, places, mapTestWidth, mapTestHeight, DefaultMapGroupConfig())
	items := mapItemsForScene("scene-1", plans, mapTestWidth, mapTestHeight, 0, 0, "", DefaultMapGroupConfig())

	if len(items) != 2 {
		t.Fatalf("six places on one plate must yield 2 map items, got %d", len(items))
	}
	shown := make(map[string]int)
	for _, item := range items {
		if item.Map == nil {
			t.Fatalf("item %q carries no map", item.ID)
		}
		if len(item.Map.Pins) > maxMapPinsPerGroup {
			t.Fatalf("item %q has %d pins, cap is %d", item.ID, len(item.Map.Pins), maxMapPinsPerGroup)
		}
		for _, pin := range item.Map.Pins {
			shown[pin.ID]++
		}
	}
	for _, place := range places {
		if shown[place.EntityID] != 1 {
			t.Fatalf("place %q shown %d times, want exactly 1", place.EntityID, shown[place.EntityID])
		}
	}
	if items[0].ID == items[1].ID {
		t.Fatalf("split map items must have distinct ids, both %q", items[0].ID)
	}
}

// TestStaticMapGroupKeepsFivePlacesInOneItem pins the boundary: five places stay
// a single x5 group.
func TestStaticMapGroupKeepsFivePlacesInOneItem(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	places := groupedPlaces(t, 5)
	plans := mapPlansForScene(stubPlateResolver{plates: []MapPlate{plate}}, places, mapTestWidth, mapTestHeight, DefaultMapGroupConfig())
	items := mapItemsForScene("scene-1", plans, mapTestWidth, mapTestHeight, 0, 0, "", DefaultMapGroupConfig())
	if len(items) != 1 || len(items[0].Map.Pins) != 5 {
		t.Fatalf("five places must form one x5 group, got %d items", len(items))
	}
}

// TestNarrativeProximityGroupsRapidPlacesIntoOneMapGroup is the "three cities
// pronounced in rapid succession" acceptance case: exactly ONE MapGroup x3,
// carrying its group identity.
func TestNarrativeProximityGroupsRapidPlacesIntoOneMapGroup(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	places := groupedPlaces(t, 3)
	plans := mapPlansForScene(stubPlateResolver{plates: []MapPlate{plate}}, places, mapTestWidth, mapTestHeight, DefaultMapGroupConfig())
	items := mapItemsForScene("scene-1", plans, mapTestWidth, mapTestHeight, 0, 0, "", DefaultMapGroupConfig())
	if len(items) != 1 || len(items[0].Map.Pins) != 3 {
		t.Fatalf("three rapid cities must form ONE MapGroup x3, got %d items / %d pins", len(items), len(items[0].Map.Pins))
	}
	group, ok := items[0].Params["map_group"].(map[string]any)
	if !ok || group["index"] != 1 || group["count"] != 1 || group["size"] != 3 {
		t.Fatalf("map_group identity missing or wrong: %+v", items[0].Params["map_group"])
	}
}

// TestNarrativeProximitySplitFollowsNarrativeBoundaries proves explicit group
// division follows the narration: two rapid clusters become two complete
// groups instead of one 5-pin chunk that splits a cluster apart.
func TestNarrativeProximitySplitFollowsNarrativeBoundaries(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	var places []MapCandidate
	for i := 0; i < 3; i++ {
		places = append(places, groundedCandidate(t, fmt.Sprintf("city:a%d", i), fmt.Sprintf("A %d", i),
			romeLat+0.01*float64(i), romeLon+0.01*float64(i), int64(i)*1_000_000, 500_000))
	}
	for i := 0; i < 3; i++ {
		places = append(places, groundedCandidate(t, fmt.Sprintf("city:b%d", i), fmt.Sprintf("B %d", i),
			romeLat+0.05+0.01*float64(i), romeLon+0.05+0.01*float64(i), 30_000_000+int64(i)*1_000_000, 500_000))
	}
	plans := mapPlansForScene(stubPlateResolver{plates: []MapPlate{plate}}, places, mapTestWidth, mapTestHeight, DefaultMapGroupConfig())
	items := mapItemsForScene("scene-1", plans, mapTestWidth, mapTestHeight, 0, 0, "", DefaultMapGroupConfig())
	if len(items) != 2 {
		t.Fatalf("two narrative clusters must yield 2 explicit groups, got %d", len(items))
	}
	for i, item := range items {
		if len(item.Map.Pins) != 3 {
			t.Fatalf("group %d has %d pins, want the complete 3-place cluster", i, len(item.Map.Pins))
		}
		group := item.Params["map_group"].(map[string]any)
		if group["index"] != i+1 || group["count"] != 2 {
			t.Fatalf("group %d identity = %+v", i, group)
		}
	}
}

// TestRepeatedPlaceOccurrencesPreserveAllSpokenMentions proves a place named
// twice keeps one pin that stays active across BOTH mentions (merged span),
// instead of keeping only the first occurrence.
func TestRepeatedPlaceOccurrencesPreserveAllSpokenMentions(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	places := []MapCandidate{
		groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 1_000_000),
		groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 4_000_000, 1_000_000),
	}
	plans := mapPlansForScene(stubPlateResolver{plates: []MapPlate{plate}}, places, mapTestWidth, mapTestHeight, DefaultMapGroupConfig())
	items := mapItemsForScene("scene-1", plans, mapTestWidth, mapTestHeight, 0, 0, "", DefaultMapGroupConfig())
	if len(items) != 1 || len(items[0].Map.Pins) != 1 {
		t.Fatalf("repeated mentions must stay one pin on one map: %d items", len(items))
	}
	item := items[0]
	if item.StartUS != 1_000_000 {
		t.Fatalf("map starts at %d, want the first mention (1000000)", item.StartUS)
	}
	timing := item.Params["map_pin_timing"].(map[string]map[string]any)
	rome := timing["city:rome"]
	if rome["start_us"] != int64(0) || rome["duration_us"] != int64(4_000_000) {
		t.Fatalf("pin timing must span both mentions 1s..5s, got %+v", rome)
	}
}

// TestMapGroupStrategyDeclaresTourOrMultiPin is the "insufficient duration for
// the tour" acceptance case: the planner declares a multi-pin composition
// BEFORE rendering, and never drops places.
func TestMapGroupStrategyDeclaresTourOrMultiPin(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	// Three rapid cities inside a short spoken span: no room for a stop-by-stop
	// tour (needs 3+4.5*2 s), so the group must declare multi_pin.
	var places []MapCandidate
	for i := 0; i < 3; i++ {
		places = append(places, groundedCandidate(t, fmt.Sprintf("city:p%d", i), fmt.Sprintf("P %d", i),
			romeLat+0.01*float64(i), romeLon+0.01*float64(i), int64(i)*300_000, 250_000))
	}
	plans := mapPlansForScene(stubPlateResolver{plates: []MapPlate{plate}}, places, mapTestWidth, mapTestHeight, DefaultMapGroupConfig())
	items := mapItemsForScene("scene-1", plans, mapTestWidth, mapTestHeight, 0, 0, "", DefaultMapGroupConfig())
	if len(items) != 1 {
		t.Fatalf("short beat must keep the whole group together, got %d items", len(items))
	}
	if got := items[0].Params["map_group_strategy"]; got != "multi_pin" {
		t.Fatalf("short beat strategy = %v, want declared multi_pin alternative", got)
	}
	if len(items[0].Map.Pins) != 3 {
		t.Fatalf("declared alternative must keep every place, got %d pins", len(items[0].Map.Pins))
	}
	// A span long enough for the authored tour keeps the tour strategy.
	if mapGroupTourStrategy(3, int64(mapTourMinimumSeconds(3)*1_000_000)) != "tour" {
		t.Fatal("sufficient duration must select the tour strategy")
	}
}
