package model

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/color"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// validateCameraMovePresence owns the no-camera branch. A static map has
// exactly one basemap asset; a dynamic-renderer-only map carries none.
func (m *MapOverlay) validateCameraMovePresence(assets []OverlayAssetRef) error {
	if m.CameraMove != nil {
		return nil
	}
	if len(m.LODs) != 0 {
		return fmt.Errorf("map LODs require camera_move")
	}
	if !m.DynamicRendererOnly && len(assets) != 1 {
		return fmt.Errorf("map requires exactly one basemap asset")
	}
	if m.DynamicRendererOnly && len(assets) != 0 {
		return fmt.Errorf("dynamic-renderer map must not carry unused basemap assets")
	}
	return nil
}

// validateCameraMoveLODSet owns the 2..8 LOD shape and its asset cardinality.
func (m *MapOverlay) validateCameraMoveLODSet(assets []OverlayAssetRef) error {
	if len(m.LODs) < 2 || len(m.LODs) > 8 ||
		(!m.DynamicRendererOnly && len(m.LODs) != len(assets)) ||
		(m.DynamicRendererOnly && len(assets) != 0) {
		return fmt.Errorf("camera map requires 2..8 LODs and one matching asset per LOD")
	}
	return nil
}

// validateCameraMoveEndpoints owns the geographic endpoints, the zoom interval,
// the tilt interval and the bearing bound. The zoom must increase and match the
// first and last LOD exactly.
func validateCameraMoveEndpoints(move *MapCameraMove, lods []MapOverlayLOD) error {
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
		math.Abs(move.StartZoom-float64(lods[0].Zoom)) > 1e-9 ||
		math.Abs(move.EndZoom-float64(lods[len(lods)-1].Zoom)) > 1e-9 {
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
	return nil
}

// validateMapLODs walks the LODs in declaration order. The checks are grouped
// exactly where they were in the original body: provenance/raster bounds,
// asset identity/order/duplicates, coverage, then the byte-level asset proof.
func (m *MapOverlay) validateMapLODs(move *MapCameraMove, canvasWidth, canvasHeight int, assets []OverlayAssetRef) error {
	seenAssets := make(map[string]struct{}, len(m.LODs))
	seenZooms := make(map[int]struct{}, len(m.LODs))
	for index, lod := range m.LODs {
		if err := m.validateMapLODHeader(index, lod); err != nil {
			return err
		}
		if err := m.validateMapLODOrder(index, lod, assets, seenAssets, seenZooms); err != nil {
			return err
		}
		if err := m.validateMapLODCoverage(index, lod, move, canvasWidth, canvasHeight); err != nil {
			return err
		}
		if m.DynamicRendererOnly {
			continue
		}
		if err := validateMapLODAsset(index, lod, assets); err != nil {
			return err
		}
	}
	return nil
}

// validateMapLODHeader owns provenance, attribution and the declared raster
// bounds of one LOD.
func (m *MapOverlay) validateMapLODHeader(index int, lod MapOverlayLOD) error {
	if (!m.DynamicRendererOnly && strings.TrimSpace(lod.AssetID) == "") || strings.TrimSpace(lod.SourceID) == "" ||
		strings.TrimSpace(lod.SourceLicense) == "" || !isSingleLineVisibleText(lod.Attribution) ||
		lod.SourceLicense != m.SourceLicense || lod.Attribution != m.Attribution {
		return fmt.Errorf("map LOD[%d] requires source provenance and attribution matching the basemap", index)
	}
	if lod.Zoom < 0 || lod.Zoom > maxCameraMapZoom {
		return fmt.Errorf("map LOD[%d] zoom must be in [0,%d]", index, maxCameraMapZoom)
	}
	if lod.Width < 1 || lod.Height < 1 || lod.Width > MaxMapRasterWidth || lod.Height > MaxMapRasterHeight {
		return fmt.Errorf("map LOD[%d] raster %dx%d must be positive and stay within %dx%d", index, lod.Width, lod.Height, MaxMapRasterWidth, MaxMapRasterHeight)
	}
	return nil
}

// validateMapLODOrder owns the asset_refs position, ascending zoom, duplicate
// asset and duplicate zoom checks. The maps are populated here so the caller
// sees the same "first duplicate wins" semantics as before.
func (m *MapOverlay) validateMapLODOrder(index int, lod MapOverlayLOD, assets []OverlayAssetRef, seenAssets map[string]struct{}, seenZooms map[int]struct{}) error {
	if !m.DynamicRendererOnly && (index >= len(assets) || assets[index].AssetID != lod.AssetID) {
		return fmt.Errorf("map LOD[%d] asset must match asset_refs[%d]", index, index)
	}
	if index > 0 && lod.Zoom <= m.LODs[index-1].Zoom {
		return fmt.Errorf("map LODs must be in strictly ascending zoom order")
	}
	if !m.DynamicRendererOnly {
		if _, duplicate := seenAssets[lod.AssetID]; duplicate {
			return fmt.Errorf("map LOD[%d] duplicates asset_id %q", index, lod.AssetID)
		}
		seenAssets[lod.AssetID] = struct{}{}
	}
	if _, duplicate := seenZooms[lod.Zoom]; duplicate {
		return fmt.Errorf("map LODs duplicate zoom %d", lod.Zoom)
	}
	seenZooms[lod.Zoom] = struct{}{}
	return nil
}

// validateMapLODCoverage owns the georeference proof: the LOD center, the first
// LOD matching the declared coarse basemap, the active camera viewport and
// every pin.
func (m *MapOverlay) validateMapLODCoverage(index int, lod MapOverlayLOD, move *MapCameraMove, canvasWidth, canvasHeight int) error {
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
	return nil
}

// validateMapLODAsset owns the content-addressed proof of one LOD raster: the
// declared asset must be a local image/png whose SHA-256 and dimensions match
// the file on disk.
func validateMapLODAsset(index int, lod MapOverlayLOD, assets []OverlayAssetRef) error {
	assetFound := false
	for _, asset := range assets {
		if asset.AssetID != lod.AssetID {
			continue
		}
		assetFound = true
		if !digest.IsSHA256(strings.TrimSpace(asset.SHA256)) || strings.ToLower(strings.TrimSpace(strings.SplitN(asset.MediaType, ";", 2)[0])) != "image/png" ||
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
		// The map's declared dimensions describe its projected world plane. A
		// 2:1 raster is also valid: it keeps camera bleed and georeferencing
		// while limiting the source image to 1920x1080.
		if (width != lod.Width || height != lod.Height) &&
			(width*2 != lod.Width || height*2 != lod.Height) {
			return fmt.Errorf("map LOD[%d] declares projected size %dx%d but raster is %dx%d (expected full or half resolution)", index, lod.Width, lod.Height, width, height)
		}
		break
	}
	if !assetFound {
		return fmt.Errorf("map LOD[%d] references undeclared asset %q", index, lod.AssetID)
	}
	return nil
}

// validateMapBaseFields owns the map block that does not depend on the LOD
// walk: provider, source provenance, attribution, canvas dimensions, zoom,
// center and motion.
func (m *MapOverlay) validateMapBaseFields(canvasWidth, canvasHeight int) error {
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
	if m.Width > MaxMapRasterWidth || m.Height > MaxMapRasterHeight {
		return fmt.Errorf("map raster %dx%d exceeds the published contract maximum %dx%d", m.Width, m.Height, MaxMapRasterWidth, MaxMapRasterHeight)
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
	return nil
}

// validateMapBasemapCounts owns the dynamic-renderer/asset cardinality checks
// that run after the camera declaration is validated.
func (m *MapOverlay) validateMapBasemapCounts(assets []OverlayAssetRef) error {
	if m.DynamicRendererOnly && len(assets) != 0 {
		return fmt.Errorf("dynamic-renderer map must not carry unused basemap assets")
	}
	if !m.DynamicRendererOnly && len(assets) == 0 {
		return fmt.Errorf("map requires at least one basemap asset")
	}
	if !m.DynamicRendererOnly && m.CameraMove != nil && (len(m.LODs) == 0 || assets[0].AssetID != m.LODs[0].AssetID || m.Width != m.LODs[0].Width || m.Height != m.LODs[0].Height) {
		return fmt.Errorf("camera map base raster must match its first LOD")
	}
	return nil
}

// validateMapBasemapAsset owns the byte-level proof of the static basemap.
func (m *MapOverlay) validateMapBasemapAsset(assets []OverlayAssetRef) error {
	if m.DynamicRendererOnly {
		return nil
	}
	asset := assets[0]
	if strings.TrimSpace(asset.AssetID) == "" || !digest.IsSHA256(strings.TrimSpace(asset.SHA256)) || strings.ToLower(strings.TrimSpace(strings.SplitN(asset.MediaType, ";", 2)[0])) != "image/png" {
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
	return nil
}

// validateMapPins owns the pin contract: bounded count, unique single-line ids,
// visible labels, finite radius, hex color, valid geodesy and window coverage.
func (m *MapOverlay) validateMapPins() error {
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
		if !color.IsHexRGB(pin.Color) {
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
