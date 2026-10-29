// Package overlay — map_territory.go resolves the ADM3 `territory_feature`
// value the native dynamic map renderer requires for the ported territory
// composition (territory_glow_reveal). ChrononPlugin reads the SAME catalog
// file (base + "/italy_admin/geoBoundaries_ITA_ADM3.simplified.geojson") and
// matches the payload value with an EXACT comparison against the feature's
// `shapeName` property — so this resolver must produce exactly that spelling
// from the same source. The pinned file is part of the renderer's certified
// data contract; a different dataset would fail at the boundary, never
// silently.
//
// Data contract (fail-closed, matching the map composition resolver):
//   - no catalog directory (CHRONON_MAPS_CATALOG_DIR unset/missing), an
//     unparsable catalog or a pin outside every ADM3 feature ⇒ the territory
//     is UNRESOLVED: the random draw excludes territory compositions (data
//     compatibility) and explicit requests fail with a precise reason.
//     No silent substitution ever ships a renderer payload the native
//     renderer would refuse.
package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MapTerritoryOverlayCompositions lists the compositions served BY the
// ported map producer that require `territory_feature` on the wire (the
// renderer's payload validation refuses them without it). The full renderer
// vocabulary also gates satellite_scan_reveal / territory_flood_fill /
// territory_extrusion on this field, but those compositions are not in the
// ported subset yet — adding one there must extend this list in the same
// change as PortedMapCompositions.
func MapTerritoryOverlayCompositions() []string {
	return []string{"territory_glow_reveal"}
}

// MapTerritoryOverlayRequired reports whether a composition requires the
// ADM3 territory data on the renderer payload.
func MapTerritoryOverlayRequired(id string) bool {
	for _, c := range MapTerritoryOverlayCompositions() {
		if c == id {
			return true
		}
	}
	return false
}

const adm3CatalogRelPath = "italy_admin/geoBoundaries_ITA_ADM3.simplified.geojson"

type adm3Catalog struct {
	features []adm3Feature
}

type adm3Feature struct {
	name     string
	polygons [][][][2]float64 // one entry per polygon: rings, outer ring first
}

type adm3GeoJSON struct {
	Features []adm3GeoFeature `json:"features"`
}

type adm3GeoFeature struct {
	Properties map[string]any  `json:"properties"`
	Geometry   adm3GeoGeometry `json:"geometry"`
}

type adm3GeoGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// geometryPolygons decodes `coordinates` according to the feature's declared
// type: a Polygon carries one ring list, a MultiPolygon carries one per
// constituent polygon. Any other geometry type or an unparsable shape
// contributes no polygons — the feature is then never matchable, which is
// the fail-closed behavior for geometry the point-in-polygon test cannot
// honor.
func geometryPolygons(geom adm3GeoGeometry) [][][][2]float64 {
	switch geom.Type {
	case "Polygon":
		var polygon [][][2]float64
		if err := json.Unmarshal(geom.Coordinates, &polygon); err != nil {
			return nil
		}
		return [][][][2]float64{polygon}
	case "MultiPolygon":
		var polygons [][][][2]float64
		if err := json.Unmarshal(geom.Coordinates, &polygons); err != nil {
			return nil
		}
		return polygons
	default:
		return nil
	}
}

// MapTerritoryCatalogDir reads the catalog root the renderer is configured
// for. Backpressure-style env override: CHRONON_MAPS_CATALOG_DIR, set by the
// systemd drop-in in production. Empty ⇒ unmapped.
func MapTerritoryCatalogDir() string {
	return strings.TrimSpace(os.Getenv("CHRONON_MAPS_CATALOG_DIR"))
}

// catalogCacheKey is the per-path cache of parsed ADM3 catalogs, so tests can
// point CHRONON_MAPS_CATALOG_DIR at a fixture without cross-contaminating a
// real load, and so a missing catalog never memoizes a failure into global
// state for later callers.
var (
	catalogCacheMu sync.Mutex
	catalogCache   = map[string]*adm3Catalog{}
)

func loadADM3Catalog(root string) (*adm3Catalog, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("adm3 catalog: CHRONON_MAPS_CATALOG_DIR is not set")
	}
	catalogCacheMu.Lock()
	defer catalogCacheMu.Unlock()
	if cached, ok := catalogCache[root]; ok {
		if cached == nil {
			return nil, fmt.Errorf("adm3 catalog %q failed to load earlier in this process", root)
		}
		return cached, nil
	}
	cat, err := parseADM3Catalog(filepath.Join(root, adm3CatalogRelPath))
	if err != nil {
		catalogCache[root] = nil
		return nil, err
	}
	catalogCache[root] = cat
	return cat, nil
}

func parseADM3Catalog(path string) (*adm3Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("adm3 catalog: %w", err)
	}
	var doc adm3GeoJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("adm3 catalog %s: %w", path, err)
	}
	cat := &adm3Catalog{features: make([]adm3Feature, 0, len(doc.Features))}
	for _, f := range doc.Features {
		name, _ := f.Properties["shapeName"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		feat := adm3Feature{name: name}
		feat.polygons = geometryPolygons(f.Geometry)
		if len(feat.polygons) == 0 {
			continue
		}
		cat.features = append(cat.features, feat)
	}
	if len(cat.features) == 0 {
		return nil, fmt.Errorf("adm3 catalog %s carries no usable ADM3 features", path)
	}
	return cat, nil
}

// ResolveMapTerritoryFeature answers WHICH ADM3 feature contains the point,
// producing the exact `shapeName` spelling the native renderer matches. It
// walks features sequentially and answers on the first containing outer ring,
// mirroring the renderer's GeoJSON scan; a pin inside two features would be
// ambiguous data, and both sides resolve it to the same first match only when
// their file ordering matches — it does, because both read the same file.
// ok=false is NOT an error to enrich: callers report it as data
// incompatibility (random exclusion / explicit precise failure).
func ResolveMapTerritoryFeature(root string, lat, lon float64) (string, bool) {
	cat, err := loadADM3Catalog(root)
	if err != nil {
		return "", false
	}
	for _, f := range cat.features {
		for _, polygon := range f.polygons {
			ring := outerRing(polygon)
			if len(ring) == 0 || !closedRing(ring) {
				continue
			}
			if pointInRing(lat, lon, ring) {
				return f.name, true
			}
		}
	}
	return "", false
}

// outerRing returns the first (outer) ring of a polygon, or nil when the
// polygon carries no rings.
func outerRing(polygon [][][2]float64) [][2]float64 {
	if len(polygon) == 0 {
		return nil
	}
	return polygon[0]
}

func closedRing(ring [][2]float64) bool {
	if len(ring) < 4 {
		return false
	}
	first, last := ring[0], ring[len(ring)-1]
	return first[0] == last[0] && first[1] == last[1]
}

// pointInRing is the standard even-odd ray-casting test; ADM3 simplified
// rings are planar lat/lon polygons, so the test is exact for this catalog.
func pointInRing(lat, lon float64, ring [][2]float64) bool {
	inside := false
	j := len(ring) - 1
	for i := 0; i < len(ring); i++ {
		yi, xi := ring[i][1], ring[i][0]
		yj, xj := ring[j][1], ring[j][0]
		if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
			inside = !inside
		}
		j = i
	}
	return inside
}
