package geodesy

import (
	"math"
	"testing"
)

// mercatorReference is the contract between this port and the projection's
// other two sides: map_cartography.py's MERCATOR_REFERENCE (asserted by
// --self-test) and ChrononMotion3D's tests/motion_geospatial.cpp. Each row is
// (lat, lon, zoom, global pixel X, global pixel Y).
var mercatorReference = []struct {
	lat, lon float64
	zoom     int
	x, y     float64
	name     string
}{
	{41.890210, 12.492231, 14, 2242697.0401450666, 1558716.8764844453, "Colosseo"},
	{29.86, -92.70, 8, 15892.48, 27067.93732941762, "Gulf coast"},
	{33.9598, -83.3768, 16, 4502967.491697778, 6704228.367772281, "Athens, GA"},
	{0.0, 0.0, 0, 128.0, 128.0, "the grid origin"},
}

// TestMercatorMatchesCertifiedReferencePixels pins the projection against the
// pixels the Python cartography tool and the C++ motion engine assert. A drift
// here moves every marker off the pixel its geography maps to.
func TestMercatorMatchesCertifiedReferencePixels(t *testing.T) {
	for _, row := range mercatorReference {
		x, y := LatLonToGlobalPixel(row.lat, row.lon, row.zoom)
		if math.Abs(x-row.x) > 1e-6 {
			t.Errorf("%s: global pixel X = %.12f, want %.12f", row.name, x, row.x)
		}
		if math.Abs(y-row.y) > 1e-6 {
			t.Errorf("%s: global pixel Y = %.12f, want %.12f", row.name, y, row.y)
		}
	}
}

// TestMercatorRoundTrip pins invertibility: a plate could not be georeferenced
// at all if the projection were not reversible to 1e-9 degrees.
func TestMercatorRoundTrip(t *testing.T) {
	for _, row := range mercatorReference {
		x, y := LatLonToGlobalPixel(row.lat, row.lon, row.zoom)
		lat, lon := GlobalPixelToLatLon(x, y, row.zoom)
		if math.Abs(lat-row.lat) > 1e-9 {
			t.Errorf("%s: round-trip latitude = %.12f, want %.12f", row.name, lat, row.lat)
		}
		if math.Abs(lon-row.lon) > 1e-9 {
			t.Errorf("%s: round-trip longitude = %.12f, want %.12f", row.name, lon, row.lon)
		}
	}
}

// TestMercatorDomainClamp pins the domain edges: clamped, not wrapped or
// rejected, exactly as the Python self-test asserts.
func TestMercatorDomainClamp(t *testing.T) {
	x1, y1 := LatLonToGlobalPixel(89.0, 0.0, 4)
	x2, y2 := LatLonToGlobalPixel(MercatorLatitudeLimit, 0.0, 4)
	if x1 != x2 || y1 != y2 {
		t.Errorf("a latitude past the pole must clamp to the Mercator limit: (%v,%v) != (%v,%v)", x1, y1, x2, y2)
	}
	x1, y1 = LatLonToGlobalPixel(0.0, 200.0, 4)
	x2, y2 = LatLonToGlobalPixel(0.0, 180.0, 4)
	if x1 != x2 || y1 != y2 {
		t.Errorf("a longitude past the antimeridian must clamp to 180: (%v,%v) != (%v,%v)", x1, y1, x2, y2)
	}
}

// TestCertifiedGulfCorridorWindow pins the screen-space pixels of the
// certified I-10 corridor window (center 29.86/-92.70, zoom 8, 1920×1080) —
// the reference generate_geospatial_tests.py asserts (Houston ~[474.0, 560.9],
// Baton Rouge ~[1235.4, 415.5], New Orleans ~[1445.7, 517.9]) and the values
// the plan documents as Houston→[474,561], New Orleans→[1446,518].
func TestCertifiedGulfCorridorWindow(t *testing.T) {
	w := CenteredOn(29.86, -92.70, 8, 1920, 1080)

	// A window centred on a point puts that point on the middle pixel: the
	// assumption every marker placement rests on.
	cx, cy := w.ToRaster(29.86, -92.70)
	if math.Abs(cx-960.0) > 0.01 || math.Abs(cy-540.0) > 0.01 {
		t.Errorf("window centre raster = (%.4f, %.4f), want (960, 540)", cx, cy)
	}

	cases := []struct {
		name         string
		lat, lon     float64
		wantX, wantY int
	}{
		{"Houston", 29.7604, -95.3698, 474, 561},
		{"New Orleans", 29.9654, -90.0321, 1446, 518},
	}
	for _, tc := range cases {
		x, y := w.ToRaster(tc.lat, tc.lon)
		if int(math.Round(x)) != tc.wantX || int(math.Round(y)) != tc.wantY {
			t.Errorf("%s raster pixel = (%.2f, %.2f), want rounded (%d, %d)", tc.name, x, y, tc.wantX, tc.wantY)
		}
		if !w.Contains(tc.lat, tc.lon) {
			t.Errorf("%s must fall inside the certified window", tc.name)
		}
	}

	// Baton Rouge keeps the python-side sub-pixel value.
	x, y := w.ToRaster(30.4515, -91.1871)
	if math.Abs(x-1235.4) > 0.1 || math.Abs(y-415.5) > 0.1 {
		t.Errorf("Baton Rouge raster = (%.2f, %.2f), want (~1235.4, ~415.5)", x, y)
	}
}

// TestWindowRejectsOutOfWindowPoints pins the fail-closed predicate the
// overlay contract uses: a route point outside the plate window must be
// detectable, never silently drawn off the plate.
func TestWindowRejectsOutOfWindowPoints(t *testing.T) {
	w := CenteredOn(29.86, -92.70, 8, 1920, 1080)
	if w.Contains(48.8566, 2.3522) { // Paris at a Gulf-coast window
		t.Error("Paris must not fall inside the Gulf-coast window")
	}
	if !w.Contains(29.7604, -95.3698) {
		t.Error("Houston must fall inside the Gulf-coast window")
	}
}
