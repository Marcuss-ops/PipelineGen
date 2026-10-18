package overlays

import "testing"

// buildSingleMapPlan runs the full planner (BuildPlan) for one grounded place
// covered by a certified plate, using the given job seed.
func buildSingleMapPlan(t *testing.T, seed string) OverlayPlan {
	t.Helper()
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	scene := SceneInput{
		ID:   "scene-1",
		Maps: []MapCandidate{groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 2_000_000)},
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video", MotionSeed: seed,
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}))
	if err != nil {
		t.Fatalf("build plan (seed %q): %v", seed, err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan for seed %q does not seal: %v", seed, err)
	}
	return plan
}

// mapCompositionOf returns the composition recorded on the single map item.
func mapCompositionOf(t *testing.T, plan OverlayPlan) string {
	t.Helper()
	var found string
	count := 0
	for _, item := range plan.Items {
		if item.Kind != "map" {
			continue
		}
		count++
		composition, _ := item.Params["map_animation"].(string)
		found = composition
	}
	if count != 1 {
		t.Fatalf("expected exactly one map item, got %d", count)
	}
	return found
}

// TestMapCompositionChosenEndToEndIsDeterministicPerSeed runs the whole planner
// twice with the same job seed and requires the same recorded composition, then
// checks that the chosen composition belongs to the certified set.
func TestMapCompositionChosenEndToEndIsDeterministicPerSeed(t *testing.T) {
	certified := map[string]bool{"region_zoom": true, "pulse_marker": true, "spotlight": true}

	first := mapCompositionOf(t, buildSingleMapPlan(t, "job-e2e-A"))
	second := mapCompositionOf(t, buildSingleMapPlan(t, "job-e2e-A"))
	if first == "" || !certified[first] {
		t.Fatalf("end-to-end composition %q is not a certified map composition", first)
	}
	if first != second {
		t.Fatalf("same seed must yield the same composition end to end: %q vs %q", first, second)
	}
}

// TestMapCompositionChosenEndToEndVariesAcrossSeeds proves that different job
// seeds reach the planner's seeded draw and produce more than one composition.
// Unified catalog: all three restrained compositions carry a single pin
// (Python MULTI_PIN_SYSTEMS is empty), so no single-pin exclusion applies.
func TestMapCompositionChosenEndToEndVariesAcrossSeeds(t *testing.T) {
	certified := map[string]bool{"region_zoom": true, "pulse_marker": true, "spotlight": true}
	seen := map[string]bool{}
	for i := 0; i < 24; i++ {
		seed := "job-e2e-" + string(rune('a'+i))
		composition := mapCompositionOf(t, buildSingleMapPlan(t, seed))
		if !certified[composition] {
			t.Fatalf("single-pin composition %q not in unified pool (seed %q)", composition, seed)
		}
		seen[composition] = true
	}
	if len(seen) < 2 {
		t.Fatalf("different job seeds should vary the composition end to end, got %v", seen)
	}
}

// TestMapCompositionEndToEndMultiPinKeepsEveryPlace runs three rapid places
// through BuildPlan: the group must keep all three pins and still record a
// certified composition chosen from the multi-pin-compatible families.
func TestMapCompositionEndToEndMultiPinKeepsEveryPlace(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	var places []MapCandidate
	for i := 0; i < 3; i++ {
		id := "city:p" + string(rune('0'+i))
		places = append(places, groundedCandidate(t, id, "P"+string(rune('0'+i)),
			romeLat+0.01*float64(i), romeLon+0.01*float64(i), int64(i)*300_000, 250_000))
	}
	scene := SceneInput{ID: "scene-1", Maps: places}
	plan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video", MotionSeed: "job-multi",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}))
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("multi-pin plan does not seal: %v", err)
	}
	for _, item := range plan.Items {
		if item.Kind != "map" {
			continue
		}
		if len(item.Map.Pins) != 3 {
			t.Fatalf("multi-pin map must keep all 3 places, got %d", len(item.Map.Pins))
		}
		composition, _ := item.Params["map_animation"].(string)
		if composition != "region_zoom" && composition != "pulse_marker" && composition != "spotlight" {
			t.Fatalf("multi-pin composition %q is not certified", composition)
		}
	}
}
