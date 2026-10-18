package overlay

import (
	"strings"
	"testing"
)

func TestExplicitEditorialOutOfAreaFailsInPlanner(t *testing.T) {
	pins := []map[string]any{{"latitude": 48.85, "longitude": 2.35}}
	input := MapCompositionInput{PinCount: 1, Pins: pins, DurationUS: 8_000_000}
	if err := AdmitMapComposition("ed_saudi_base", input, MapCompositionCertified()); err == nil || !strings.Contains(err.Error(), "geography") {
		t.Fatalf("explicit out-of-area must fail with geography error, got %v", err)
	}
	if _, _, err := ResolveMapCompositionFor("ed_saudi_base", "seed", "slot", input); err == nil {
		t.Fatal("ResolveMapCompositionFor explicit out-of-area must fail")
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
	if err1 != nil || err2 != nil || a != b || a == "" {
		t.Fatalf("same seed+slot must resolve identically, got %q %q %v %v", a, b, err1, err2)
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
