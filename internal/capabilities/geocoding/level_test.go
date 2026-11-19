package geocoding

import "testing"

// The scope↔level rule is the single authority shared by the provider adapter
// and the overlay planner. A region frame is drawn around its cities; every
// other level admits only its own scope.
func TestScopeFitsLevel(t *testing.T) {
	cases := []struct {
		scope, level string
		want         bool
	}{
		{"country", LevelCountry, true},
		{"city", LevelCountry, false},
		{"region", LevelCountry, false},
		{"city", LevelRegion, true},
		{"region", LevelRegion, true},
		{"country", LevelRegion, false},
		{"town", LevelMunicipality, true},
		{"municipality", LevelMunicipality, true},
		{"region", LevelMunicipality, false},
		{"continent", LevelContinent, true},
		{"country", LevelContinent, false},
		// No level keeps the historical unfiltered behaviour.
		{"city", "", true},
		{"", "", true},
		{"", LevelCountry, false},
	}
	for _, tc := range cases {
		if got := ScopeFitsLevel(tc.scope, tc.level); got != tc.want {
			t.Errorf("ScopeFitsLevel(%q, %q) = %v, want %v", tc.scope, tc.level, got, tc.want)
		}
	}
}

func TestNormalizeLevelFoldsPayloadSpellings(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"auto":         "",
		"country":      LevelCountry,
		"region":       LevelRegion,
		"MUNICIPALITY": LevelMunicipality,
		"city":         LevelMunicipality,
		"comune":       LevelMunicipality,
		"continent":    LevelContinent,
		"state":        LevelRegion,
	}
	for in, want := range cases {
		got, err := NormalizeLevel(in)
		if err != nil {
			t.Fatalf("NormalizeLevel(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeLevel(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := NormalizeLevel("galaxy"); err == nil {
		t.Fatal("an unsupported level must fail closed")
	}
}

// Only a coarse frame constrains the provider. A fine frame needs the coarser
// places as disambiguation context, so its lookup stays level-agnostic.
func TestConstraintLevelOnlyNarrowsCoarseFrames(t *testing.T) {
	cases := map[string]string{
		LevelCountry:      LevelCountry,
		LevelContinent:    LevelContinent,
		LevelRegion:       "",
		LevelMunicipality: "",
		"":                "",
		"auto":            "",
	}
	for in, want := range cases {
		got, err := ConstraintLevel(in)
		if err != nil {
			t.Fatalf("ConstraintLevel(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ConstraintLevel(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ConstraintLevel("galaxy"); err == nil {
		t.Fatal("an unsupported level must fail closed")
	}
}

func TestLevelConstrains(t *testing.T) {
	for _, level := range []string{LevelContinent, LevelCountry, LevelRegion, LevelMunicipality} {
		if !LevelConstrains(level) {
			t.Errorf("LevelConstrains(%q) = false", level)
		}
	}
	for _, level := range []string{"", "auto", "galaxy"} {
		if LevelConstrains(level) {
			t.Errorf("LevelConstrains(%q) = true, want no restriction", level)
		}
	}
}

func TestNormalizeRequestCarriesTheLevel(t *testing.T) {
	req, err := NormalizeRequest(Request{Query: "Italia", Level: "country"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Level != LevelCountry {
		t.Fatalf("level = %q, want %q", req.Level, LevelCountry)
	}
	if _, err := NormalizeRequest(Request{Query: "Italia", Level: "galaxy"}); err == nil {
		t.Fatal("an unsupported level must be rejected at the port boundary")
	}
}
