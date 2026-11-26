package model

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

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
	// DynamicRendererOnly is PipelineGen planning state. It is stripped before
	// a plan can reach RenderingGen; the dynamic renderer owns tile rasterization.
	DynamicRendererOnly bool `json:"dynamic_renderer_only,omitempty"`

	// Semantic-plan candidate fields are present only in the Rust
	// editorial_plan.v2 result. The Producer lowers them through its certified
	// local map-plate resolver before rendering; they never enter the v1 wire.
	semanticEntityID  string
	semanticLabel     string
	semanticScope     string
	semanticLatitude  float64
	semanticLongitude float64
}

// UnmarshalJSON accepts the Rust planner's compact map candidate as well as a
// fully materialized renderer map. The semantic fields are retained only in
// memory so the Producer can attach a licensed, certified local raster.
func (m *MapOverlay) UnmarshalJSON(data []byte) error {
	type mapOverlayAlias MapOverlay
	var wire struct {
		*mapOverlayAlias
		EntityID  string  `json:"entity_id"`
		Label     string  `json:"label"`
		Scope     string  `json:"scope"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	wire.mapOverlayAlias = (*mapOverlayAlias)(m)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.semanticEntityID = wire.EntityID
	m.semanticLabel = wire.Label
	m.semanticScope = wire.Scope
	m.semanticLatitude = wire.Latitude
	m.semanticLongitude = wire.Longitude
	return nil
}

// SemanticCandidate returns the transient place carried by editorial_plan.v2.
func (m *MapOverlay) SemanticCandidate() (entityID, label, scope string, latitude, longitude float64, ok bool) {
	if m == nil || strings.TrimSpace(m.semanticEntityID) == "" || strings.TrimSpace(m.semanticLabel) == "" {
		return "", "", "", 0, 0, false
	}
	return m.semanticEntityID, m.semanticLabel, m.semanticScope, m.semanticLatitude, m.semanticLongitude, true
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
	move := m.CameraMove
	if move == nil {
		return m.validateCameraMovePresence(assets)
	}
	if err := m.validateCameraMoveLODSet(assets); err != nil {
		return err
	}
	if err := validateCameraMoveEndpoints(move, m.LODs); err != nil {
		return err
	}
	return m.validateMapLODs(move, canvasWidth, canvasHeight, assets)
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
	// maxMapPins, MaxMapRasterWidth and MaxMapRasterHeight mirror the published
	// overlay-plan.v1 bounds for the map block, so the producer cannot emit a
	// plan the worker's schema would reject.
	maxMapPins         = 128
	MaxMapRasterWidth  = 7680
	MaxMapRasterHeight = 4320
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
	var low, high float64
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
	if err := m.validateMapBaseFields(canvasWidth, canvasHeight); err != nil {
		return err
	}
	if err := m.validateCameraMove(canvasWidth, canvasHeight, assets); err != nil {
		return err
	}
	if err := m.validateMapBasemapCounts(assets); err != nil {
		return err
	}
	if err := m.validateMapBasemapAsset(assets); err != nil {
		return err
	}
	return m.validateMapPins()
}
