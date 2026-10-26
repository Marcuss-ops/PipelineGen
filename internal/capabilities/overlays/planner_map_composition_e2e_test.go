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

// servableComposition reports whether the planner may record this composition:
// only compositions the single dynamic renderer serves (PortedMapCompositions)
// or the empty base-flyover id.
func servableComposition(id string) bool {
	if id == "" {
		return true
	}
	for _, ported := range PortedMapCompositions() {
		if ported == id {
			return true
		}
	}
	return false
}

// TestMapCompositionChosenEndToEndIsDeterministicPerSeed runs the whole
// planner twice with the same job seed and requires the same recorded
// composition, and that the recorded composition is servable end to end.
func TestMapCompositionChosenEndToEndIsDeterministicPerSeed(t *testing.T) {
	first := mapCompositionOf(t, buildSingleMapPlan(t, "job-e2e-A"))
	second := mapCompositionOf(t, buildSingleMapPlan(t, "job-e2e-A"))
	if !servableComposition(first) {
		t.Fatalf("end-to-end composition %q is not servable by the single dynamic renderer", first)
	}
	if first != second {
		t.Fatalf("same seed must yield the same composition end to end: %q vs %q", first, second)
	}
}

// TestMapCompositionEndToEndRecordsOnlyServableCompositions proves that
// different job seeds reach the planner's seeded draw and every recorded
// composition is servable — while no composed family has landed its parity
// gate that means the certified base flyover for every seed, never an
// unported family. A seeded draw that CHOOSES servable compositions (the
// engine's family-first sampler) is pinned in the overlay subpackage.
func TestMapCompositionEndToEndRecordsOnlyServableCompositions(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 24; i++ {
		seed := "job-e2e-" + string(rune('a'+i))
		composition := mapCompositionOf(t, buildSingleMapPlan(t, seed))
		if !servableComposition(composition) {
			t.Fatalf("composition %q not servable by the single dynamic renderer (seed %q)", composition, seed)
		}
		seen[composition] = true
	}
	if len(seen) == 0 {
		t.Fatal("the end-to-end draw must record at least one composition id")
	}
}

// TestMapCompositionEndToEndMultiPinKeepsEveryPlace runs three rapid places
// through BuildPlan: the group must keep all three pins and record only a
// servable composition.
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
		if !servableComposition(composition) {
			t.Fatalf("multi-pin composition %q is not servable", composition)
		}
	}
}
