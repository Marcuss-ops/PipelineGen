// Package geodesy — mercator.go is PipelineGen's Web Mercator (EPSG:3857)
// georeferencing: the exact projection the map pipeline certifies on its
// other two sides.
//
// The raster half is produced by Chronon3d/tools/cartography/map_cartography.py
// and the motion half is consumed by ChrononMotion3D's WebMercator.hpp. Both
// assert the SAME reference pixels (map_cartography.py --self-test and
// motion_geospatial.cpp), and this port asserts them again so a drift in any
// of the three breaks a test instead of a render: a plate is produced on one
// side and georeferenced on the other, and every pin, route point and window
// bound must land on the pixel the raster put there or the map lies about
// geography.
//
// The formula is the slippy-tile convention: a global pixel grid of
// 256 * 2^zoom tiles, +x east, +y south, latitudes clamped to the Mercator
// limit — identical operations in the same order as the Python reference so
// the float64 results agree bit for bit.
package geodesy

import (
	"fmt"
	"math"
)

// Point is one WGS84 coordinate in degrees. DisplayName preserves the
// geocoder's canonical label when available without changing lat/lon identity.
type Point struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	DisplayName string  `json:"display_name,omitempty"`
}

// Validate enforces finite WGS84 latitude/longitude bounds.
func (p Point) Validate() error {
	if math.IsNaN(p.Latitude) || math.IsInf(p.Latitude, 0) || p.Latitude < -90 || p.Latitude > 90 {
		return fmt.Errorf("latitude %v outside [-90,90]", p.Latitude)
	}
	if math.IsNaN(p.Longitude) || math.IsInf(p.Longitude, 0) || p.Longitude < -180 || p.Longitude > 180 {
		return fmt.Errorf("longitude %v outside [-180,180]", p.Longitude)
	}
	return nil
}

const (
	// MercatorLatitudeLimit is the latitude beyond which Mercator has no
	// finite image (the web map convention; atan(sinh(pi)) in degrees).
	MercatorLatitudeLimit = 85.05112877980659
	// MercatorTileSize is one tile edge in pixels at every zoom level.
	MercatorTileSize = 256.0
)

// LatLonToGlobalPixel converts WGS84 (lat, lon) to global Mercator pixel
// coordinates at the given zoom. Latitudes clamp to the Mercator limit and
// longitudes clamp to the antimeridian: a window is allowed to run past the
// pole and the date line, it never wraps.
func LatLonToGlobalPixel(lat, lon float64, zoom int) (x, y float64) {
	scale := MercatorTileSize * math.Pow(2, float64(zoom))
	if lon < -180 {
		lon = -180
	} else if lon > 180 {
		lon = 180
	}
	if lat < -MercatorLatitudeLimit {
		lat = -MercatorLatitudeLimit
	} else if lat > MercatorLatitudeLimit {
		lat = MercatorLatitudeLimit
	}
	x = (lon + 180.0) / 360.0 * scale
	latRad := lat * math.Pi / 180.0
	y = (1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) * 0.5 * scale
	return x, y
}

// GlobalPixelToLatLon is the inverse of LatLonToGlobalPixel.
func GlobalPixelToLatLon(x, y float64, zoom int) (lat, lon float64) {
	scale := MercatorTileSize * math.Pow(2, float64(zoom))
	lon = (x/scale)*360.0 - 180.0
	n := math.Pi - 2.0*math.Pi*(y/scale)
	lat = math.Atan(math.Sinh(n)) * 180.0 / math.Pi
	return lat, lon
}

// Window is the geography a raster basemap covers, and where that raster sits
// on the global pixel grid. TopLeft is the global pixel of the raster's own
// (0,0) corner; Width/Height are the raster's size in pixels.
//
// It mirrors ChrononMotion3D's MapWindow (centeredOn / toRaster / bounds), so
// the producer-side validation here and the motion-side placement there
// describe one window in one vocabulary.
type Window struct {
	Zoom               int
	TopLeftX, TopLeftY float64
	Width, Height      float64
}

// CenteredOn returns the window of the given raster size centred on a
// geographic point — the exact window map_cartography.py's window_origin
// builds (center - size/2).
func CenteredOn(lat, lon float64, zoom, width, height int) Window {
	cx, cy := LatLonToGlobalPixel(lat, lon, zoom)
	return Window{
		Zoom:     zoom,
		TopLeftX: cx - float64(width)/2.0,
		TopLeftY: cy - float64(height)/2.0,
		Width:    float64(width),
		Height:   float64(height),
	}
}

// ToRaster positions a geographic point inside the raster, in raster pixels
// (+x east, +y south, origin at the raster's top-left corner). This is
// map_cartography.py's GeoreferencedMap.to_screen.
func (w Window) ToRaster(lat, lon float64) (x, y float64) {
	gx, gy := LatLonToGlobalPixel(lat, lon, w.Zoom)
	return gx - w.TopLeftX, gy - w.TopLeftY
}

// Contains reports whether a geographic point falls INSIDE the raster window
// (inclusive of the edges). The overlay contract uses it fail-closed: a pin or
// route point outside the plate window is a producer bug, never a marker drawn
// off the plate.
func (w Window) Contains(lat, lon float64) bool {
	x, y := w.ToRaster(lat, lon)
	return x >= 0 && y >= 0 && x <= w.Width && y <= w.Height
}

// CenterLatLon returns the geography at the window's centre pixel (round-trip
// of CenteredOn).
func (w Window) CenterLatLon() (lat, lon float64) {
	return GlobalPixelToLatLon(w.TopLeftX+w.Width/2.0, w.TopLeftY+w.Height/2.0, w.Zoom)
}
