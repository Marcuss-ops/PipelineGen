package overlay

import (
	"strings"
	"testing"
)

func TestExplicitEditorialOutOfAreaFailsInPlanner(t *testing.T) {
	pins := []map[string]any{{"latitude": 48.85, "longitude": 2.35}}
	input := MapCompositionInput{PinCount: 1, Pins: pins, DurationUS: 8_000_000}
	// The composed editorial scenes retired with the Python renderer, so an
	// explicit request is refused BY NAME at admission (the certified
	// vocabulary is the ChrononPlugin map catalog) — never silently redrawn as
	// something else and never accepted for a renderer that is gone.
	if err := AdmitMapComposition("ed_saudi_base", input, MapCompositionCertified()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("retired editorial composition must fail as unsupported, got %v", err)
	}
	if _, _, err := ResolveMapCompositionFor("ed_saudi_base", "seed", "slot", input); err == nil {
		t.Fatal("ResolveMapCompositionFor retired editorial composition must fail")
	}
	// The authored-geography gate stays guarded for any manifest-backed
	// composition id, so the bounds machinery is not silently dead code.
	if MapCompositionGeographyCompatible("ed_saudi_base", pins) {
		t.Fatal("pins outside the authored scene bounds must be excluded")
	}
	inside := []map[string]any{{"latitude": 25.0, "longitude": 45.0}}
	if !MapCompositionGeographyCompatible("ed_saudi_base", inside) {
		t.Fatal("pins inside the authored scene bounds must stay compatible")
	}
}

func TestExplicitWrongPinCountFails(t *testing.T) {
	input := MapCompositionInput{PinCount: 99, Pins: nil, DurationUS: 8_000_000}
	if _, _, err := ResolveMapCompositionFor("region_zoom", "s", "slot", input); err == nil {
		t.Fatal("explicit composition with impossible pin count must fail")
	}
}

func TestDeterministicCompositionStable(t *testing.T) {
	input := MapCompositionInput{PinCount: 1, DurationUS: 8_000_000}
	a, _, err1 := ResolveMapCompositionFor("random", "seed123", "item-1", input)
	b, _, err2 := ResolveMapCompositionFor("random", "seed123", "item-1", input)
	if err1 != nil || err2 != nil || a != b {
		t.Fatalf("same seed+slot must resolve identically, got %q %q %v %v", a, b, err1, err2)
	}
	// The random draw selects only compositions the executing renderer serves
	// (map_composition.go selection contract): a member of the native gallery
	// pool, or the certified base flyover (empty id) when the data admits no
	// gallery composition — never a family the native renderer cannot draw,
	// and never a failed map.
	if a != "" && !containsComposition(MapCompositionRandomPool(), a) {
		t.Fatalf("random draw %q is outside the servable pool %v", a, MapCompositionRandomPool())
	}
	// The same input with a strategy still resolves: either the same servable
	// composition, or the declared base-flyover fallback.
	withStrategy := input
	withStrategy.Strategy = "tour"
	selected, fallback, err := ResolveMapCompositionFor("random", "seed123", "item-1", withStrategy)
	if err != nil {
		t.Fatalf("strategy present must resolve, got %v", err)
	}
	if selected == "" && fallback == "" {
		t.Fatal("the base-flyover fallback must be declared when the draw falls back")
	}
	if selected != "" && !containsComposition(MapCompositionRandomPool(), selected) {
		t.Fatalf("strategy draw %q is outside the servable pool %v", selected, MapCompositionRandomPool())
	}
}

func containsComposition(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}

// TestDeterministicCompositionChoiceVariesAcrossSeeds pins the family-first
// sampler engine itself with a synthetic pool: different seeds roll different
// compositions while one seed always replays its pick. The pool the planner
// may draw from is the servable subset (MapCompositionRandomPool); this test
// keeps the draw engine covered whatever that subset currently holds.
func TestDeterministicCompositionChoiceVariesAcrossSeeds(t *testing.T) {
	pool := []string{"region_zoom", "pulse_marker", "spotlight", "multi_locations"}
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		seed := "seed-" + string(rune('a'+i%26)) + string(rune('0'+i%10))
		pick := DeterministicMapCompositionChoice(seed, "item-1", pool)
		if pick == "" {
			t.Fatalf("non-empty pool must draw a composition (seed %q)", seed)
		}
		if replay := DeterministicMapCompositionChoice(seed, "item-1", pool); replay != pick {
			t.Fatalf("seed %q must replay its pick: %q vs %q", seed, pick, replay)
		}
		seen[pick] = true
	}
	if len(seen) < 2 {
		t.Fatalf("different seeds should vary the composition, got %v", seen)
	}
}

func TestBackendPolicyExplicit(t *testing.T) {
	if MapExecutionPolicyFromEnv(false, "") != MapExecutionProductionDynamic {
		t.Fatal("default policy must be production_dynamic")
	}
	if MapExecutionPolicyFromEnv(true, "") != MapExecutionDiagnosticNative {
		t.Fatal("native env must select diagnostic_native")
	}
}

func TestGeoManifestConsistent(t *testing.T) {
	for id, entry := range MapGeoManifest {
		bounds, ok := MapEditorialSceneBounds[id]
		if !ok || bounds != entry.AcceptedPinBounds {
			t.Fatalf("manifest projection diverged for %q", id)
		}
	}
}
