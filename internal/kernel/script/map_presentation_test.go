package script

import (
	"reflect"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

// TestMapPresentationNormalizeDefaults proves the unified contract resolves
// minimal intent into a complete, certified configuration.
func TestMapPresentationNormalizeDefaults(t *testing.T) {
	resolved, err := (&MapPresentationSpec{}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	want := MapPresentation{
		Enabled: true, Mode: "auto", GroupNearbyPlaces: true, MaxPinsPerGroup: 5,
		Style: "esri_topo", Animation: "random", Camera: "auto",
		AllowRoute: true, SelectionMode: "auto",
	}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("normalized default = %+v, want %+v", resolved, want)
	}
	disabled, err := (*MapPresentationSpec)(nil).Normalize()
	if err != nil || disabled.Enabled {
		t.Fatalf("absent spec must normalize to disabled, got %+v, %v", disabled, err)
	}
}

// TestMapPresentationNormalizeContract pins the example contract from the map
// audit: the system resolves the rest from the registry and the selector.
func TestMapPresentationNormalizeContract(t *testing.T) {
	resolved, err := (&MapPresentationSpec{
		Enabled:           boolPtr(true),
		Mode:              "auto",
		GroupNearbyPlaces: boolPtr(true),
		MaxPinsPerGroup:   5,
		Style:             "random",
		Animation:         "random",
		Camera:            "auto",
		AllowRoute:        boolPtr(true),
		SelectionMode:     "auto",
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Style != "random" || resolved.Animation != "random" || resolved.SelectionMode != "auto" {
		t.Fatalf("contract resolution drifted: %+v", resolved)
	}
}

// TestMapPresentationCarriesPreset proves the preset id survives resolution so
// the renderer boundary can re-apply the pair; the alias itself is expanded by
// the script builder before Normalize runs.
func TestMapPresentationCarriesPreset(t *testing.T) {
	resolved, err := (&MapPresentationSpec{
		Preset:    "italia_editoriale_voxel",
		Animation: "editorial_vox",
		Style:     "chronon_editorial",
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Preset != "italia_editoriale_voxel" || resolved.Animation != "editorial_vox" || resolved.Style != "chronon_editorial" {
		t.Fatalf("preset did not survive normalization: %+v", resolved)
	}
}

// TestMapPresentationNormalizeFailsClosed pins the invalid configurations the
// contract must reject instead of guessing.
func TestMapPresentationNormalizeFailsClosed(t *testing.T) {
	for name, spec := range map[string]*MapPresentationSpec{
		"unknown mode":            {Mode: "chaotic"},
		"unknown selection":       {SelectionMode: "maybe"},
		"unknown style":           {Style: "neon_cyber"},
		"pin cap above budget":    {MaxPinsPerGroup: 6},
		"pin cap zero-ish":        {MaxPinsPerGroup: -1},
		"exact without id":        {SelectionMode: "exact", Style: "chronon_editorial"},
		"exact with random style": {SelectionMode: "exact", Animation: "pulse_marker", Style: "random"},
		"manual without id":       {Mode: "manual"},
		"unported roadmap style":  {Style: "roadmap"},
		"unknown level":           {Level: "planet"},
	} {
		if _, err := spec.Normalize(); err == nil {
			t.Errorf("%s: expected fail-closed normalization", name)
		}
	}
	if _, err := (&MapPresentationSpec{SelectionMode: "exact", Animation: "pulse_marker", Style: "chronon_editorial"}).Normalize(); err != nil {
		t.Fatalf("exact explicit selection must normalize: %v", err)
	}
}

// TestMapPresentationNormalizeMapStyles pins the two supported looks and the
// cartographic levels; Classic is the editorial vector, ModernMap1 is the real
// Esri World Topo Map.
func TestMapPresentationNormalizeMapStyles(t *testing.T) {
	for _, tc := range []struct {
		style string
		want  string
	}{
		{"classic", "chronon_editorial"},
		{"chronon_editorial", "chronon_editorial"},
		{"modern_map_1", "esri_topo"},
		{"modern", "esri_topo"},
		{"topo", "esri_topo"},
		{"terrain", "esri_topo"},
		{"satellite", "esri_sat"},
	} {
		resolved, err := (&MapPresentationSpec{Style: tc.style}).Normalize()
		if err != nil {
			t.Fatalf("style %q: %v", tc.style, err)
		}
		if resolved.Style != tc.want {
			t.Fatalf("style %q resolved to %q, want %q", tc.style, resolved.Style, tc.want)
		}
	}
	for input, want := range map[string]string{
		"continent": "continent", "country": "country", "region": "region",
		"municipality": "municipality", "comune": "municipality", "": "",
	} {
		resolved, err := (&MapPresentationSpec{Level: input}).Normalize()
		if err != nil {
			t.Fatalf("level %q: %v", input, err)
		}
		if resolved.Level != want {
			t.Fatalf("level %q resolved to %q, want %q", input, resolved.Level, want)
		}
	}
}
