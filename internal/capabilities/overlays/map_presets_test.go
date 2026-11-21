package overlays

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMapPresetsMatchChrononPluginCatalog pins the Go registry to the single
// script-facing catalog of ChrononPlugin. The Go table exists because the
// kernel cannot read a sibling repository at runtime; this test is the guard
// that keeps the copy honest (same rule as the renderer-catalog test).
func TestMapPresetsMatchChrononPluginCatalog(t *testing.T) {
	source, ok := mapPresetsCatalogSourcePath()
	if !ok {
		t.Skip("ChrononPlugin map-presets catalog is not checked out beside refactored")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read ChrononPlugin map-presets catalog: %v", err)
	}
	var catalog struct {
		Entries []struct {
			ID              string `json:"id"`
			Title           string `json:"title"`
			Animation       string `json:"map_animation"`
			Basemap         string `json:"basemap"`
			DefaultMapPlace string `json:"default_map_place"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("parse ChrononPlugin map-presets catalog %s: %v", source, err)
	}
	presets := MapPresets()
	if len(presets) != len(catalog.Entries) {
		t.Fatalf("preset count drifted: Go has %d, catalog %s has %d", len(presets), source, len(catalog.Entries))
	}
	for i, entry := range catalog.Entries {
		want := MapPreset{
			ID: entry.ID, Title: entry.Title, Animation: entry.Animation,
			Basemap: entry.Basemap, DefaultPlace: entry.DefaultMapPlace,
		}
		if presets[i] != want {
			t.Fatalf("preset %d drifted: Go %+v, catalog %+v", i, presets[i], want)
		}
	}
}

// TestMapPresetsNamePortedCompositions proves every preset expands to a
// composition the single native renderer serves, so a preset can never ship an
// alias the execution boundary refuses.
func TestMapPresetsNamePortedCompositions(t *testing.T) {
	for _, preset := range MapPresets() {
		if !NativeMapCompositions(preset.Animation) {
			t.Errorf("preset %q names unported composition %q", preset.ID, preset.Animation)
		}
		if !NativeMapBasemaps()[preset.Basemap] {
			t.Errorf("preset %q names unservable basemap %q", preset.ID, preset.Basemap)
		}
	}
}

func mapPresetsCatalogSourcePath() (string, bool) {
	for _, candidate := range []string{
		filepath.Join("..", "..", "..", "..", "ChrononPlugin", "plugins", "maps", "map-presets-catalog.v1.json"),
		filepath.Join("..", "..", "..", "ChrononPlugin", "plugins", "maps", "map-presets-catalog.v1.json"),
		filepath.Join("ChrononPlugin", "plugins", "maps", "map-presets-catalog.v1.json"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
