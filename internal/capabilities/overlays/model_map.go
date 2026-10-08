package overlays

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// MapOverlay is the validated, renderer-visible geospatial declaration for a
// map item. The basemap itself is a content-addressed PNG in AssetRefs; this
// structure carries the exact georeference, grounded point markers, animation
// choice, and provider-required attribution that must appear in the render.
type MapOverlay struct {
	// Provider is deliberately restricted to "local": public tile fetchers
	// are not an allowed rendering-time asset source.
	Provider      string          `json:"provider"`
	SourceID      string          `json:"source_id"`
	SourceLicense string          `json:"source_license"`
	Center        MapCenter       `json:"center"`
	Zoom          int             `json:"zoom"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`
	Attribution   string          `json:"attribution"`
	MotionID      string          `json:"motion_id"`
	Pins          []MapOverlayPin `json:"pins"`
	// AreaGlowRadiusKM optionally draws a soft geographic ring around each
	// active pin in the dynamic-map renderer. Zero disables the area ring.
	AreaGlowRadiusKM float64 `json:"area_glow_radius_km,omitempty"`
	// LODs and CameraMove are optional together. With them, the worker places
	// each certified raster on the same projected world plane and flies the
	// native Chronon camera between the declared WGS84 endpoints.
	LODs       []MapOverlayLOD `json:"lods,omitempty"`
	CameraMove *MapCameraMove  `json:"camera_move,omitempty"`
}

// MapOverlayLOD references one independently certified, locally supplied
// raster in an offline zoom pyramid. AssetID joins exactly one item asset ref.
type MapOverlayLOD struct {
	AssetID       string    `json:"asset_id"`
	SourceID      string    `json:"source_id"`
	SourceLicense string    `json:"source_license"`
	Attribution   string    `json:"attribution"`
	Center        MapCenter `json:"center"`
	Zoom          int       `json:"zoom"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
}

// MapCameraMove is a deterministic 2D Web-Mercator fly-to. The producer owns
// the geographic endpoints and zoom interval; the renderer derives native
// camera poses in projected world space.
type MapCameraMove struct {
	From         MapCenter `json:"from"`
	To           MapCenter `json:"to"`
	StartZoom    float64   `json:"start_zoom"`
	EndZoom      float64   `json:"end_zoom"`
	StartTiltDeg float64   `json:"start_tilt_deg"`
	EndTiltDeg   float64   `json:"end_tilt_deg"`
	BearingDeg   float64   `json:"bearing_deg"`
}

const maxCameraMapZoom = 18

func (m *MapOverlay) validateCameraMove(canvasWidth, canvasHeight int, assets []OverlayAssetRef) error {
	if m.CameraMove == nil {
		if len(m.LODs) != 0 {
			return fmt.Errorf("map LODs require camera_move")
		}
		if len(assets) != 1 {
			return fmt.Errorf("map requires exactly one basemap asset")
		}
		return nil
	}
	move := m.CameraMove
	if len(m.LODs) < 2 || len(m.LODs) > 8 || len(m.LODs) != len(assets) {
		return fmt.Errorf("camera map requires 2..8 LODs and one matching asset per LOD")
	}
	for name, point := range map[string]MapCenter{"camera from": move.From, "camera to": move.To} {
		if err := (geodesy.Point{Latitude: point.Latitude, Longitude: point.Longitude}).Validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	for name, zoom := range map[string]float64{"start_zoom": move.StartZoom, "end_zoom": move.EndZoom} {
		if math.IsNaN(zoom) || math.IsInf(zoom, 0) || zoom < 0 || zoom > maxCameraMapZoom {
			return fmt.Errorf("camera %s must be finite and in [0,%d]", name, maxCameraMapZoom)
		}
	}
	if move.EndZoom <= move.StartZoom ||
		math.Abs(move.StartZoom-float64(m.LODs[0].Zoom)) > 1e-9 ||
		math.Abs(move.EndZoom-float64(m.LODs[len(m.LODs)-1].Zoom)) > 1e-9 {
		return fmt.Errorf("camera zoom must increase and match the first and last map LOD")
	}

	for name, tilt := range map[string]float64{"start_tilt_deg": move.StartTiltDeg, "end_tilt_deg": move.EndTiltDeg} {
		if math.IsNaN(tilt) || math.IsInf(tilt, 0) || math.Abs(tilt) > 80 {
			return fmt.Errorf("camera %s must be finite and within ±80 degrees", name)
		}
	}
	if math.IsNaN(move.BearingDeg) || math.IsInf(move.BearingDeg, 0) || math.Abs(move.BearingDeg) > 360 {
		return fmt.Errorf("camera bearing_deg must be finite and within ±360 degrees")
	}
	seenAssets := make(map[string]struct{}, len(m.LODs))
	seenZooms := make(map[int]struct{}, len(m.LODs))
	for index, lod := range m.LODs {
		if strings.TrimSpace(lod.AssetID) == "" || strings.TrimSpace(lod.SourceID) == "" ||
			strings.TrimSpace(lod.SourceLicense) == "" || !isSingleLineVisibleText(lod.Attribution) ||
			lod.SourceLicense != m.SourceLicense || lod.Attribution != m.Attribution {
			return fmt.Errorf("map LOD[%d] requires source provenance and attribution matching the basemap", index)
		}
		if lod.Zoom < 0 || lod.Zoom > maxCameraMapZoom {
			return fmt.Errorf("map LOD[%d] zoom must be in [0,%d]", index, maxCameraMapZoom)
		}
		if lod.Width < 1 || lod.Height < 1 || lod.Width > maxMapRasterWidth || lod.Height > maxMapRasterHeight {
			return fmt.Errorf("map LOD[%d] raster %dx%d must be positive and stay within %dx%d", index, lod.Width, lod.Height, maxMapRasterWidth, maxMapRasterHeight)
		}
		if index >= len(assets) || assets[index].AssetID != lod.AssetID {
			return fmt.Errorf("map LOD[%d] asset must match asset_refs[%d]", index, index)
		}
		if index > 0 && lod.Zoom <= m.LODs[index-1].Zoom {
			return fmt.Errorf("map LODs must be in strictly ascending zoom order")
		}
		if _, duplicate := seenAssets[lod.AssetID]; duplicate {
			return fmt.Errorf("map LOD[%d] duplicates asset_id %q", index, lod.AssetID)
		}
		if _, duplicate := seenZooms[lod.Zoom]; duplicate {
			return fmt.Errorf("map LODs duplicate zoom %d", lod.Zoom)
		}
		seenAssets[lod.AssetID] = struct{}{}
		seenZooms[lod.Zoom] = struct{}{}
		if err := (geodesy.Point{Latitude: lod.Center.Latitude, Longitude: lod.Center.Longitude}).Validate(); err != nil {
			return fmt.Errorf("map LOD[%d] center: %w", index, err)
		}
		if index == 0 && (lod.Zoom != m.Zoom || lod.Center != m.Center || m.Width != lod.Width || m.Height != lod.Height) {
			return fmt.Errorf("first map LOD must match the declared coarse basemap")
		}
		lodWindow := geodesy.CenteredOn(lod.Center.Latitude, lod.Center.Longitude, lod.Zoom, lod.Width, lod.Height)
		lowZoom, highZoom := mapLODActiveZoomRange(index, m.LODs, move)
		if !mapLODWindowCoversMove(lodWindow, move, lowZoom, highZoom, canvasWidth, canvasHeight) {
			return fmt.Errorf("map LOD[%d] does not cover its active camera viewport and cross-fade interval", index)
		}
		for pinIndex, pin := range m.Pins {
			if !lodWindow.Contains(pin.Latitude, pin.Longitude) {
				return fmt.Errorf("map LOD[%d] does not cover pin[%d] %q", index, pinIndex, pin.ID)
			}
		}
		assetFound := false
		for _, asset := range assets {
			if asset.AssetID == lod.AssetID {
				assetFound = true
				if len(strings.TrimSpace(asset.SHA256)) != 64 || strings.Trim(strings.TrimSpace(asset.SHA256), "0123456789abcdefABCDEF") != "" || strings.ToLower(strings.TrimSpace(strings.SplitN(asset.MediaType, ";", 2)[0])) != "image/png" ||
					strings.TrimSpace(asset.LocalPath) == "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.URL)), "http://") ||
					strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.URL)), "https://") {
					return fmt.Errorf("map LOD[%d] must reference a verified local image/png asset", index)
				}
				actualHash, _, err := digest.SHA256File(asset.LocalPath)
				if err != nil {
					return fmt.Errorf("map LOD[%d] raster: %w", index, err)
				}
				if !strings.EqualFold(actualHash, asset.SHA256) {
					return fmt.Errorf("map LOD[%d] raster SHA-256 mismatch", index)
				}
				width, height, err := verifyPNGRasterDimensions(asset.LocalPath)
				if err != nil {
					return fmt.Errorf("map LOD[%d] raster integrity: %w", index, err)
				}
				// The map's declared dimensions describe its projected world
				// plane. A 2:1 raster is also valid: it keeps camera bleed and
				// georeferencing while limiting the source image to 1920x1080.
				if (width != lod.Width || height != lod.Height) &&
					(width*2 != lod.Width || height*2 != lod.Height) {
					return fmt.Errorf("map LOD[%d] declares projected size %dx%d but raster is %dx%d (expected full or half resolution)", index, lod.Width, lod.Height, width, height)
				}
				break
			}
		}
		if !assetFound {
			return fmt.Errorf("map LOD[%d] references undeclared asset %q", index, lod.AssetID)
		}
	}
	return nil
}

// MapCenter is the georeference anchor of a map item: a WGS84 point and
// nothing else. The published contract's `center` carries coordinates only —
// the worker's SemanticMapPoint and overlay-plan.v1 both pin exactly
// {latitude, longitude} and forbid additional properties, and the worker
// decodes with DisallowUnknownFields. A display label riding along on the
// geodesy enrichment type would therefore reject the WHOLE plan, not decorate
// the center, so the wire type deliberately cannot carry one.
type MapCenter struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// MapOverlayPin is a source-grounded location point projected over the map.
type MapOverlayPin struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Scope     string  `json:"scope,omitempty"`
	Color     string  `json:"color"`
	RadiusPX  float64 `json:"radius_px"`
}

// The bounds of the map contract's visible-text fields. Attribution and pin
// labels reach a rendered, user-visible surface, so they share one strict
// contract: valid UTF-8, no control characters (which would otherwise let a
// layout break or a terminal escape ride into the render) and a hard byte
// budget. maxMapAttributionBytes mirrors the published overlay-plan.v1 schema.
const (
	maxMapAttributionBytes = 512
	maxMapPinLabelBytes    = 256
	// maxMapPins, maxMapRasterWidth and maxMapRasterHeight mirror the published
	// overlay-plan.v1 bounds for the map block, so the producer cannot emit a
	// plan the worker's schema would reject.
	maxMapPins         = 128
	maxMapRasterWidth  = 7680
	maxMapRasterHeight = 4320
)

// pngSignature is the 8-byte magic every PNG stream begins with (RFC 2083).
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

func wrapMapPlaneDelta(value, worldSize float64) float64 {
	if value > worldSize/2 {
		value -= worldSize
	}
	if value < -worldSize/2 {
		value += worldSize
	}
	return value
}

func mapLODActiveZoomRange(index int, lods []MapOverlayLOD, move *MapCameraMove) (float64, float64) {
	low, high := float64(lods[index].Zoom), float64(lods[index].Zoom)
	if index == 0 {
		low = move.StartZoom
	} else {
		low = (float64(lods[index-1].Zoom)+float64(lods[index].Zoom))/2 - 0.25
		if low < move.StartZoom {
			low = move.StartZoom
		}
	}
	if index == len(lods)-1 {
		high = move.EndZoom
	} else {
		high = (float64(lods[index].Zoom)+float64(lods[index+1].Zoom))/2 + 0.25
		if high > move.EndZoom {
			high = move.EndZoom
		}
	}
	return low, high
}

func mapLODWindowCoversMove(window geodesy.Window, move *MapCameraMove, lowZoom, highZoom float64, canvasWidth, canvasHeight int) bool {
	fromX, fromY := geodesy.LatLonToGlobalPixel(move.From.Latitude, move.From.Longitude, window.Zoom)
	toX, toY := geodesy.LatLonToGlobalPixel(move.To.Latitude, move.To.Longitude, window.Zoom)
	if highZoom < lowZoom || move.EndZoom <= move.StartZoom || canvasWidth <= 0 || canvasHeight <= 0 {
		return false
	}
	deltaX := wrapMapPlaneDelta(toX-fromX, geodesy.MercatorTileSize*math.Pow(2, float64(window.Zoom)))
	zoomFactor := math.Pow(2, move.EndZoom-move.StartZoom)
	toTime := func(zoom float64) float64 {
		return math.Max(0, math.Min(1, (math.Pow(2, zoom-move.StartZoom)-1)/(zoomFactor-1)))
	}
	startT, endT := toTime(lowZoom), toTime(highZoom)
	viewportScale := 1 / math.Max(0.17, math.Cos(math.Pi/180*math.Max(math.Abs(move.StartTiltDeg), math.Abs(move.EndTiltDeg))))
	viewportHalfWidth := float64(canvasWidth) / 2 * viewportScale
	viewportHalfHeight := float64(canvasHeight) / 2 * viewportScale
	for step := 0; step <= 64; step++ {
		t := startT + (endT-startT)*float64(step)/64
		zoom := move.StartZoom + math.Log2(1+(zoomFactor-1)*t)
		x := fromX + deltaX*t - window.TopLeftX
		y := fromY + (toY-fromY)*t - window.TopLeftY
		zoomScale := math.Pow(2, float64(window.Zoom)-zoom)
		marginX := viewportHalfWidth * zoomScale
		marginY := viewportHalfHeight * zoomScale
		if x < marginX || y < marginY || x > window.Width-marginX || y > window.Height-marginY {
			return false
		}
	}
	return true
}

func (m *MapOverlay) Validate(canvasWidth, canvasHeight int, assets []OverlayAssetRef) error {
	if m == nil {
		return fmt.Errorf("map declaration is required")
	}
	if math.IsNaN(m.AreaGlowRadiusKM) || math.IsInf(m.AreaGlowRadiusKM, 0) || m.AreaGlowRadiusKM < 0 || m.AreaGlowRadiusKM > 1000 {
		return fmt.Errorf("map area_glow_radius_km must be finite and in [0,1000]")
	}
	provider := strings.ToLower(strings.TrimSpace(m.Provider))
	if provider != "local" {
		return fmt.Errorf("unsupported map provider %q: only explicitly supplied local rasters are accepted", m.Provider)
	}
	if strings.TrimSpace(m.SourceID) == "" || strings.TrimSpace(m.SourceLicense) == "" {
		return fmt.Errorf("local map requires source_id and source_license")
	}
	if len(m.Attribution) > maxMapAttributionBytes || !isSingleLineVisibleText(m.Attribution) {
		return fmt.Errorf("local map requires a single-line visible attribution of at most %d bytes", maxMapAttributionBytes)
	}
	if (m.CameraMove == nil && (m.Width != canvasWidth || m.Height != canvasHeight)) || m.Width <= 0 || m.Height <= 0 {
		return fmt.Errorf("map raster dimensions %dx%d must match canvas %dx%d (static) and be positive", m.Width, m.Height, canvasWidth, canvasHeight)
	}

	if m.Width > maxMapRasterWidth || m.Height > maxMapRasterHeight {
		return fmt.Errorf("map raster %dx%d exceeds the published contract maximum %dx%d", m.Width, m.Height, maxMapRasterWidth, maxMapRasterHeight)
	}
	if m.Zoom < 0 || m.Zoom > 22 {
		return fmt.Errorf("map zoom %d outside [0,22]", m.Zoom)
	}
	if err := (geodesy.Point{Latitude: m.Center.Latitude, Longitude: m.Center.Longitude}).Validate(); err != nil {
		return fmt.Errorf("invalid map center: %w", err)
	}
	// An empty motion is the certified stable runtime map treatment: the map
	// camera owns the movement while its georeferenced raster, pin and label
	// remain still relative to one another.
	if strings.TrimSpace(m.MotionID) != "" && !isAllowedMapMotion(m.MotionID) {
		return fmt.Errorf("unsupported or missing map motion %q", m.MotionID)
	}
	if err := m.validateCameraMove(canvasWidth, canvasHeight, assets); err != nil {
		return err
	}
	if len(assets) == 0 {
		return fmt.Errorf("map requires at least one basemap asset")
	}
	if m.CameraMove != nil && (len(m.LODs) == 0 || assets[0].AssetID != m.LODs[0].AssetID || m.Width != m.LODs[0].Width || m.Height != m.LODs[0].Height) {
		return fmt.Errorf("camera map base raster must match its first LOD")
	}
	asset := assets[0]
	if strings.TrimSpace(asset.AssetID) == "" || len(strings.TrimSpace(asset.SHA256)) != 64 || strings.Trim(strings.TrimSpace(asset.SHA256), "0123456789abcdefABCDEF") != "" || strings.ToLower(strings.TrimSpace(strings.SplitN(asset.MediaType, ";", 2)[0])) != "image/png" {
		return fmt.Errorf("map basemap requires a 64-hex content-addressed image/png asset")
	}
	if strings.TrimSpace(asset.LocalPath) == "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.URL)), "http://") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.URL)), "https://") {
		return fmt.Errorf("map basemap must be a verified local file; network references are forbidden")
	}
	actualHash, _, err := digest.SHA256File(asset.LocalPath)
	if err != nil {
		return fmt.Errorf("read local map raster: %w", err)
	}
	if !strings.EqualFold(actualHash, asset.SHA256) {
		return fmt.Errorf("local map raster SHA-256 mismatch: declared %s, got %s", asset.SHA256, actualHash)
	}
	if err := verifyPNGRaster(asset.LocalPath); err != nil {
		return fmt.Errorf("local map raster integrity: %w", err)
	}
	if len(m.Pins) > maxMapPins {
		return fmt.Errorf("map declares %d pins; the published contract allows at most %d", len(m.Pins), maxMapPins)
	}
	window := geodesy.CenteredOn(m.Center.Latitude, m.Center.Longitude, m.Zoom, m.Width, m.Height)
	seenPins := make(map[string]struct{}, len(m.Pins))
	for index, pin := range m.Pins {
		if strings.TrimSpace(pin.ID) != pin.ID || !isSingleLineVisibleText(pin.ID) {
			return fmt.Errorf("map pin[%d] id must be a non-blank trimmed single-line identifier", index)
		}
		if !isSingleLineVisibleText(pin.Label) || len(pin.Label) > maxMapPinLabelBytes {
			return fmt.Errorf("map pin[%d] label must be a single-line value of at most %d bytes", index, maxMapPinLabelBytes)
		}
		if _, exists := seenPins[pin.ID]; exists {
			return fmt.Errorf("map pin[%d] duplicates id %q", index, pin.ID)
		}
		seenPins[pin.ID] = struct{}{}
		if pin.RadiusPX <= 0 || pin.RadiusPX > 128 || math.IsNaN(pin.RadiusPX) || math.IsInf(pin.RadiusPX, 0) {
			return fmt.Errorf("map pin[%d] radius_px must be finite and in (0,128]", index)
		}
		if !isHexColor(pin.Color) {
			return fmt.Errorf("map pin[%d] color must be #RRGGBB", index)
		}
		point := geodesy.Point{Latitude: pin.Latitude, Longitude: pin.Longitude}
		if err := point.Validate(); err != nil {
			return fmt.Errorf("map pin[%d]: %w", index, err)
		}
		if !window.Contains(pin.Latitude, pin.Longitude) {
			return fmt.Errorf("map pin[%d] falls outside the basemap window", index)
		}
	}
	return nil
}

func isAllowedMapMotion(id string) bool {
	if id == "image_fade_reveal" || id == "image_focus_reveal" || id == "image_scale_reveal" {
		return true
	}
	for _, candidate := range mapImageMotionCandidates {
		if id == candidate {
			return true
		}
	}
	return false
}

// isSingleLineVisibleText reports whether value is non-blank valid UTF-8 with
// no control characters — the shape every value that reaches a rendered text
// surface must have. It is the single owner of the "single line, no control
// characters" rule shared by the visible attribution and the pin labels.
func isSingleLineVisibleText(value string) bool {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// verifyPNGRaster fails closed unless the content-addressed basemap is really
// a PNG. The declared media type is producer-supplied metadata; the plate the
// renderer paints and georeferences is the bytes on disk, so the bytes are
// what gets checked.
func verifyPNGRaster(path string) error {
	_, _, err := verifyPNGRasterDimensions(path)
	return err
}

func verifyPNGRasterDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	header := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(file, header); err != nil {
		return 0, 0, fmt.Errorf("not a PNG raster: %w", err)
	}
	if !bytes.Equal(header, pngSignature) {
		return 0, 0, fmt.Errorf("not a PNG raster: unexpected magic")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	if format != "png" || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, fmt.Errorf("invalid PNG raster configuration")
	}
	return config.Width, config.Height, nil
}

func isHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, digit := range value[1:] {
		switch {
		case digit >= '0' && digit <= '9', digit >= 'a' && digit <= 'f', digit >= 'A' && digit <= 'F':
		default:
			return false
		}
	}
	return true
}
