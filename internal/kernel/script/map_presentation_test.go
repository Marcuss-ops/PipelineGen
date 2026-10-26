package script

import "testing"

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
		Style: "chronon_editorial", Animation: "random", Camera: "auto",
		AllowRoute: true, SelectionMode: "auto",
	}
	if resolved != want {
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
		// The raster basemap families are demolition debt: the native map
		// renderer draws the editorial vector basemap only, so they fail
		// closed at submit — never a silent style swap, never a late job
		// failure inside the render queue.
		"unported raster style":  {Style: "satellite"},
		"unported roadmap style": {Style: "roadmap"},
		"unported terrain style": {Style: "terrain"},
	} {
		if _, err := spec.Normalize(); err == nil {
			t.Errorf("%s: expected fail-closed normalization", name)
		}
	}
	if _, err := (&MapPresentationSpec{SelectionMode: "exact", Animation: "pulse_marker", Style: "chronon_editorial"}).Normalize(); err != nil {
		t.Fatalf("exact explicit selection must normalize: %v", err)
	}
}
