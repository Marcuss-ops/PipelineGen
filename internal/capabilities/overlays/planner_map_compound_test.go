package overlays

import "testing"

func TestDedupeMapCandidatesByCompoundPlaceMergesIntoCity(t *testing.T) {
	candidates := []MapCandidate{
		{EntityID: "ent-old-city", Label: "Città Vecchia di Gerusalemme", StartUS: 2_000_000, DurationUS: 1_500_000, Score: 0.4},
		{EntityID: "ent-jerusalem", Label: "Gerusalemme", StartUS: 0, DurationUS: 1_000_000, Score: 0.5},
		{EntityID: "ent-torino", Label: "Torino", StartUS: 4_000_000, DurationUS: 1_000_000, Score: 0.4},
	}
	got := dedupeMapCandidatesByCompoundPlace(candidates)
	if len(got) != 2 {
		t.Fatalf("dedupe returned %d candidates, want the city and Torino: %#v", len(got), got)
	}
	byLabel := map[string]MapCandidate{}
	for _, candidate := range got {
		byLabel[candidate.Label] = candidate
	}
	if _, exists := byLabel["Città Vecchia di Gerusalemme"]; exists {
		t.Fatal("compound description must not remain a second pin")
	}
	city, exists := byLabel["Gerusalemme"]
	if !exists {
		t.Fatalf("canonical city missing: %#v", got)
	}
	if city.EntityID != "ent-jerusalem" || city.Score != 0.5 {
		t.Errorf("canonical city identity changed: %#v", city)
	}
	if city.StartUS != 0 || city.DurationUS != 3_500_000 {
		t.Errorf("city window = %d+%dus, want the merged 0+3500000us", city.StartUS, city.DurationUS)
	}
}

func TestDedupeMapCandidatesByCompoundPlaceKeepsUnlinkedPlaces(t *testing.T) {
	candidates := []MapCandidate{
		{EntityID: "ent-new-york", Label: "New York", DurationUS: 1_000_000},
		{EntityID: "ent-york", Label: "York", DurationUS: 1_000_000},
		{EntityID: "ent-rome", Label: "Roma", DurationUS: 1_000_000},
	}
	got := dedupeMapCandidatesByCompoundPlace(candidates)
	if len(got) != len(candidates) {
		t.Fatalf("dedupe returned %d candidates, want %d: %#v", len(got), len(candidates), got)
	}
}
