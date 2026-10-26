package overlays

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

func testPlate() MapPlate {
	return MapPlate{
		ID: "p1", License: "l", Attribution: "a", Center: MapCenter{Latitude: 48.85, Longitude: 2.35},
		Zoom: 10, Width: 1920, Height: 1080,
		Window: geodesy.CenteredOn(48.85, 2.35, 10, 1920, 1080),
		Asset:  OverlayAssetRef{AssetID: "a1"},
	}
}

func TestMapPinTimingInvariantHolds(t *testing.T) {
	plate := testPlate()
	places := []MapCandidate{
		{EntityID: "a", Label: "A", Latitude: 48.85, Longitude: 2.35, StartUS: 0, DurationUS: 1_000_000, Score: 1},
		{EntityID: "b", Label: "B", Latitude: 48.86, Longitude: 2.36, StartUS: 2_000_000, DurationUS: 1_000_000, Score: 1},
	}
	_, timing, _, dur, _, ok := mapPinsFor(plate, places)
	if !ok {
		t.Fatal("expected pins")
	}
	if !ValidateMapPinTiming(timing, dur) {
		t.Fatal("pin timing must satisfy start+duration<=map duration")
	}
}

func TestMapPinTimingOverflowFailsClosed(t *testing.T) {
	bad := map[string]map[string]any{"p": {"start_us": int64(7_000_000), "duration_us": int64(2_000_000)}}
	if ValidateMapPinTiming(bad, 8_000_000) {
		t.Fatal("pin escaping map window must fail")
	}
}

func TestTimelineAdmissionCollision(t *testing.T) {
	items := []OverlayItem{
		{ID: "m1", Kind: "map", StartUS: 0, DurationUS: 8_000_000, Map: &MapOverlay{}},
		{ID: "t1", Kind: "text_phrase", StartUS: 5_000_000, DurationUS: 3_000_000},
	}
	if err := AdmitOverlayTimeline(items, 24, 1); err == nil {
		t.Fatal("8s map + phrase at 5s must collide")
	}
	ok := []OverlayItem{
		{ID: "m1", Kind: "map", StartUS: 0, DurationUS: 8_000_000, Map: &MapOverlay{}},
		{ID: "t1", Kind: "text_phrase", StartUS: 8_000_000, DurationUS: 3_000_000},
	}
	// Phrase-phrase overlaps stay allowed (text layers, not map collisions).
	phrases := []OverlayItem{
		{ID: "p1", Kind: "text_phrase", StartUS: 0, DurationUS: 5_000_000},
		{ID: "p2", Kind: "text_phrase", StartUS: 2_000_000, DurationUS: 5_000_000},
	}
	if err := AdmitOverlayTimeline(phrases, 24, 1); err != nil {
		t.Fatalf("phrase-phrase overlap must stay allowed, got %v", err)
	}
	if err := AdmitOverlayTimeline(ok, 24, 1); err != nil {
		t.Fatalf("adjacent windows must pass, got %v", err)
	}
}

func TestSelectMapCompositionFromRegistry(t *testing.T) {
	// The registry certifies the catalog; the ported subset decides
	// servability. The selection is deterministic and only ever records a
	// composition the single dynamic renderer serves (empty = the certified
	// base flyover while no composed family has landed its parity gate).
	got := SelectMapComposition("seed", "scene", "item-1", 1)
	got2 := SelectMapComposition("seed", "scene", "item-1", 1)
	if got != got2 {
		t.Fatal("selection must be deterministic")
	}
	if !servableComposition(got) {
		t.Fatalf("selection %q is not servable by the single dynamic renderer", got)
	}
	for _, id := range PortedMapCompositions() {
		if id == got && !AnimationSelectable(id, "map.composition") {
			t.Fatalf("servable composition %q must stay registry-certified", id)
		}
	}
}
