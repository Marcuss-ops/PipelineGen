package overlay

import (
	"os"
	"path/filepath"
	"testing"
)

// writeADM3Fixture materializes a minimal ADM3-style catalog under dir and
// returns the catalog root (the directory ChrononPlugin would configure as
// CHRONON_MAPS_CATALOG_DIR). Rings are closed so the renderer's exact-match
// contract (first containing feature wins, in file order) is exercisable.
func writeADM3Fixture(t *testing.T, geojson string) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "catalog-root-"+t.Name())
	if err := os.MkdirAll(filepath.Join(root, "italy_admin"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, adm3CatalogRelPath), []byte(geojson), 0o644); err != nil {
		t.Fatalf("write fixture geojson: %v", err)
	}
	return root
}

const adm3FixtureGeoJSON = `{
  "type": "FeatureCollection",
  "features": [
    {
      "type": "Feature",
      "properties": {"shapeName": "Roma (RM)"},
      "geometry": {
        "type": "Polygon",
        "coordinates": [[[12.4, 41.85], [12.6, 41.85], [12.6, 41.95], [12.4, 41.95], [12.4, 41.85]]]
      }
    },
    {
      "type": "Feature",
      "properties": {"shapeName": "Milano (MI)"},
      "geometry": {
        "type": "MultiPolygon",
        "coordinates": [
          [[[9.15, 45.43], [9.30, 45.43], [9.30, 45.53], [9.15, 45.53], [9.15, 45.43]]],
          [[[9.10, 45.40], [9.12, 45.40], [9.12, 45.42], [9.10, 45.42], [9.10, 45.40]]]
        ]
      }
    },
    {
      "type": "Feature",
      "properties": {"shapeName": "  "},
      "geometry": {"type": "Polygon", "coordinates": [[[0, 0], [1, 0], [1, 1], [0, 0]]]}
    },
    {
      "type": "Feature",
      "properties": {"shapeName": "Mare (XX)"},
      "geometry": {"type": "Point", "coordinates": [12.5, 41.9]}
    }
  ]
}`

func TestResolveMapTerritoryFeatureMatchesRendererExactShapeName(t *testing.T) {
	root := writeADM3Fixture(t, adm3FixtureGeoJSON)

	name, ok := ResolveMapTerritoryFeature(root, 41.90, 12.49)
	if !ok {
		t.Fatal("point inside Roma must resolve")
	}
	if name != "Roma (RM)" {
		t.Fatalf("territory_feature = %q, want exact shapeName %q", name, "Roma (RM)")
	}

	name, ok = ResolveMapTerritoryFeature(root, 45.43, 9.15)
	if !ok || name != "Milano (MI)" {
		t.Fatalf("multipolygon resolve = (%q, %v), want Milano (MI)", name, ok)
	}
}

func TestResolveMapTerritoryFeatureUnresolvedOutsideEveryFeature(t *testing.T) {
	root := writeADM3Fixture(t, adm3FixtureGeoJSON)

	if _, ok := ResolveMapTerritoryFeature(root, 40.0, 18.0); ok {
		t.Fatal("point outside every ADM3 feature must stay unresolved")
	}
}

func TestResolveMapTerritoryFeatureFailClosedWithoutCatalog(t *testing.T) {
	if _, ok := ResolveMapTerritoryFeature("", 41.9, 12.49); ok {
		t.Fatal("unset catalog dir must fail closed")
	}
	if _, ok := ResolveMapTerritoryFeature(t.TempDir(), 41.9, 12.49); ok {
		t.Fatal("missing fixture file must fail closed")
	}
}

// A broken catalog never memoizes success OR a poisoned cache: a later valid
// load against the same root must still work (fresh process or same root is
// the renderer's reuse case).
func TestResolveMapTerritoryFeatureBrokenCatalogIsNotAMatch(t *testing.T) {
	root := writeADM3Fixture(t, "{not-json")

	if _, ok := ResolveMapTerritoryFeature(root, 41.90, 12.49); ok {
		t.Fatal("unparsable catalog must fail closed")
	}
}

func TestResolveMapTerritoryFeatureSkipsFeaturesWithoutPolygons(t *testing.T) {
	root := writeADM3Fixture(t, adm3FixtureGeoJSON)

	// The fixture's Point feature ("Mare (XX)") sits at the same center as
	// Roma: resolution must answer Roma, never a geometry type the PIP test
	// cannot honor.
	name, ok := ResolveMapTerritoryFeature(root, 41.90, 12.49)
	if !ok || name != "Roma (RM)" {
		t.Fatalf("got (%q, %v), want Roma (RM) — geometry-less features must be skipped", name, ok)
	}
}

func TestMapTerritoryOverlayCompositionsFailClosed(t *testing.T) {
	if !MapTerritoryOverlayRequired("territory_glow_reveal") {
		t.Fatal("territory_glow_reveal requires territory_feature on the payload")
	}
	if MapTerritoryOverlayRequired("area_glow_pulse") {
		t.Fatal("area_glow_pulse does not require the ADM3 territory")
	}
}
