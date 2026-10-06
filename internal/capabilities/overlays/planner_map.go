package overlays

import (
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// mapMotionIDs are the ONLY motions a map item may carry: the certified CENTERED
// image-motion pool, which the published overlay-plan.v1 map.motion_id enum also
// lists. They are referenced, never re-listed, so the producer cannot drift from
// the certified catalog and can never emit a motion the worker rejects. A map
// must stay on center (a panning or sliding basemap would move the geography
// away from the pins projected over it).
func mapMotionIDs() []string {
	return mapImageMotionCandidates
}

// One certified marker treatment for every grounded place, so a map's pins are
// deterministic and never invented per item.
const (
	mapPinColor               = "#FF3B30"
	mapPinRadiusPX            = 10.0
	mapCameraDurationUS int64 = 5_000_000
)

// MapPlate is the certified plate a map item is drawn from: the operator
// manifest's georeference and provenance plus the content-addressed basemap
// asset. It is the planner's view of a certified plate; the manifest remains
// the owner of provenance and of raster certification.
//
// Width/Height are the raster's own size, which is also the georeferenced
// window's size. A plate is only usable at the canvas it was authored for: the
// planner emits no map at all when they disagree, rather than stretching a
// plate and putting every pin on the wrong pixel.
type MapPlate struct {
	ID            string
	License       string
	Attribution   string
	Center        MapCenter
	Zoom          int
	Width, Height int
	Asset         OverlayAssetRef
	Window        geodesy.Window
	// LODs are complete, independently licensed rasters covering one camera
	// route. Empty means this remains a static single-plate map.
	LODs []MapPlate
}

// MapPlan is one GROUNDED place together with the certified plate that covers
// it. The planner groups plans by plate within a scene, merges their pins and
// emits at most one map item per plate — the same geography is never rendered
// twice for one scene. It is built ONLY by mapPlansForScene.
type MapPlan struct {
	Plate MapPlate
	Place MapCandidate
}

// PlateResolver resolves a grounded WGS84 point to the certified plate that
// covers it. It is the planner's only map source. An implementation must be
// offline and must fail closed (ok=false) when no certified plate covers the
// point, so no map is ever fabricated.
type PlateResolver interface {
	ResolvePlate(latitude, longitude float64) (MapPlate, bool)
}

// mapFadeHalfBandZoom is the planner's half-width (in zoom stops) of every LOD
// cross-fade — the exact mirror of the worker's mapLODFadeHalfBandZoom. The
// viewport coverage below certifies each LOD over precisely the interval where
// the worker will render it visible.
const mapFadeHalfBandZoom = 0.5

// flyoverActiveZoomRange mirrors the worker's mapLODActiveZoomRange: the zoom
// interval where LOD[index] is visible over the move.
func flyoverActiveZoomRange(index int, zooms []int, startZoom, endZoom float64) (float64, float64) {
	low, high := float64(zooms[index]), float64(zooms[index])
	if index == 0 {
		low = startZoom
	} else {
		low = (float64(zooms[index-1])+float64(zooms[index]))/2 - mapFadeHalfBandZoom
		if low < startZoom {
			low = startZoom
		}
	}
	if index == len(zooms)-1 {
		high = endZoom
	} else {
		high = (float64(zooms[index])+float64(zooms[index+1]))/2 + mapFadeHalfBandZoom
		if high > endZoom {
			high = endZoom
		}
	}
	return low, high
}

// mapPlansForScene resolves a scene's grounded place candidates against the
// run's certified plates. It fails closed at each step: a nil resolver, a
// candidate that no longer validates as grounded WGS84 with a real audio span,
// a duplicate stable entity id, or a point no certified plate covers produces
// no plan. The returned order is the caller's candidate order; mapItemsForScene
// imposes the final deterministic (plate id) order.
type FlyoverPlateResolver interface {
	ResolveFlyover(from, to MapCenter, canvasWidth, canvasHeight int) (MapPlate, bool)
}

func mapPlansForScene(resolver PlateResolver, candidates []MapCandidate, canvasWidth, canvasHeight int) []MapPlan {
	if resolver == nil || len(candidates) == 0 {
		return nil
	}
	valid := make([]MapCandidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		// Re-validate through the single candidate constructor so a candidate
		// built by hand (or one whose identity was emptied downstream) cannot
		// reach the planner as a map pinned at a guessed location.
		place, ok := NewMapCandidate(
			candidate.EntityID, candidate.Label,
			candidate.Latitude, candidate.Longitude,
			candidate.StartUS, candidate.DurationUS, candidate.Score, candidate.Scope,
		)
		if !ok {
			continue
		}
		if _, duplicate := seen[place.EntityID]; duplicate {
			continue
		}
		seen[place.EntityID] = struct{}{}
		valid = append(valid, place)
	}
	if len(valid) == 0 {
		return nil
	}
	if flyovers, ok := resolver.(FlyoverPlateResolver); ok && len(valid) > 0 && canvasWidth > 0 && canvasHeight > 0 {
		from, to := routeEndpoints(valid, MapCenter{Latitude: valid[0].Latitude, Longitude: valid[0].Longitude})
		if plate, ok := flyovers.ResolveFlyover(from, to, canvasWidth, canvasHeight); ok && strings.TrimSpace(plate.ID) != "" && len(plate.LODs) > 0 {
			plans := make([]MapPlan, 0, len(valid))
			allCovered := true
			lodWindows := make([]geodesy.Window, 0, len(plate.LODs)+1)
			lodWindows = append(lodWindows, plate.Window)
			for _, lod := range plate.LODs {
				lodWindows = append(lodWindows, geodesy.CenteredOn(lod.Center.Latitude, lod.Center.Longitude, lod.Zoom, lod.Width, lod.Height))
			}
			for _, place := range valid {
				for _, window := range lodWindows {
					if !window.Contains(place.Latitude, place.Longitude) {
						allCovered = false
						break
					}
				}
				if !allCovered {
					break
				}
			}
			// Mirror the worker's viewport contract per LOD: the route must
			// keep the full camera viewport inside every raster across the
			// zoom interval where that raster is visible. A route that fails
			// this here would fail the worker's fail-closed compile below and
			// void the whole plan — fall back to per-place plates instead.
			if allCovered {
				moveStartZoom := float64(plate.Zoom)
				moveEndZoom := float64(plate.LODs[len(plate.LODs)-1].Zoom)
				lodZooms := make([]int, 0, len(plate.LODs)+1)
				lodZooms = append(lodZooms, plate.Zoom)
				for _, lod := range plate.LODs {
					lodZooms = append(lodZooms, lod.Zoom)
				}
				for i, window := range lodWindows {
					low, high := flyoverActiveZoomRange(i, lodZooms, moveStartZoom, moveEndZoom)
					if !window.CoversMove(geodesy.Point{Latitude: from.Latitude, Longitude: from.Longitude},
						geodesy.Point{Latitude: to.Latitude, Longitude: to.Longitude},
						moveStartZoom, moveEndZoom, low, high, canvasWidth, canvasHeight) {
						allCovered = false
						break
					}
				}
			}
			if allCovered {
				for _, place := range valid {
					plans = append(plans, MapPlan{Plate: plate, Place: place})
				}
				return plans
			}
		}
	}
	plans := make([]MapPlan, 0, len(valid))
	for _, place := range valid {
		plate, ok := resolver.ResolvePlate(place.Latitude, place.Longitude)
		if !ok || strings.TrimSpace(plate.ID) == "" {
			continue
		}
		plans = append(plans, MapPlan{Plate: plate, Place: place})
	}
	return plans
}

// mapMotionAt returns the deterministic motion for the n-th emitted map. The
// small certified pool is rotated so consecutive maps in one run differ while
// the same input always produces the same choices.
func mapMotionAt(ordinal int) string {
	pool := mapMotionIDs()
	if len(pool) == 0 {
		return ""
	}
	if ordinal < 0 {
		ordinal = 0
	}
	return pool[ordinal%len(pool)]
}

// mapMotionForCenter picks the map-specific Chronon treatment for a grounded
// center. The geographic bounds choose a region recipe that ANCHORS the
// rotation, but the ordinal advances through the certified pool from that
// anchor, so a run of maps in the SAME region no longer renders the identical
// animation: consecutive maps differ while the region still leads for the
// first one. Map coverage, pins and coordinates remain owned by the certified
// plate; the bounds only choose a visual recipe.
func mapMotionForCenter(ordinal int, center MapCenter) string {
	lat, lon := center.Latitude, center.Longitude
	region := ""
	switch {
	case lat >= -44 && lat <= -10 && lon >= 112 && lon <= 154:
		region = "map_image_australia_sunset_drift"
	case lat >= -34 && lat <= 6 && lon >= -74 && lon <= -34:
		region = "map_image_brazil_glow_reveal"
	case lat >= 20 && lat <= 24 && lon >= 68 && lon <= 75:
		region = "map_image_gujarat_detail_push"
	case lat >= 6 && lat <= 35 && lon >= 67 && lon <= 98:
		region = "map_image_india_contour_draw"
	case lat >= 33 && lat <= 39 && lon >= 124 && lon <= 132:
		region = "map_image_korea_pin_focus"
	case lat >= 18 && lat <= 54 && lon >= 73 && lon <= 135:
		region = "map_image_china_slow_reveal"
	case lat >= 25 && lat <= 40 && lon >= 44 && lon <= 64:
		region = "map_image_iran_gold_focus"
	case lat >= 35 && lat <= 48 && lon >= 6 && lon <= 19:
		region = "map_image_italy_beacon_arrival"
	case lat >= 4 && lat <= 14 && lon >= 2 && lon <= 15:
		region = "map_image_nigeria_neon_bloom"
	case lat >= 24 && lat <= 50 && lon >= -125 && lon <= -66:
		region = "map_image_usa_sweep_in"
	}
	if region == "" {
		return mapMotionAt(ordinal)
	}
	pool := mapMotionIDs()
	if len(pool) == 0 {
		return region
	}
	start := 0
	for index, id := range pool {
		if id == region {
			start = index
			break
		}
	}
	if ordinal < 0 {
		ordinal = 0
	}
	return pool[(start+ordinal)%len(pool)]
}

// mapItemsForScene lowers a scene's resolved map plans to at most one overlay
// item per certified plate. It fails closed at every step: a plan without a
// plate or a place, a plate whose raster is not the canvas size, a place
// outside the plate's window, or an empty audio span produces no item — never
// a map pinned at a guessed location.
func mapItemsForScene(sceneID string, plans []MapPlan, canvasWidth, canvasHeight, ordinal int) []OverlayItem {
	if len(plans) == 0 || canvasWidth <= 0 || canvasHeight <= 0 {
		return nil
	}
	type group struct {
		plate  MapPlate
		places []MapCandidate
	}
	groups := make(map[string]*group, len(plans))
	for _, plan := range plans {
		key := strings.TrimSpace(plan.Plate.ID)
		if key == "" {
			continue
		}
		if !plateFitsCanvas(plan.Plate, canvasWidth, canvasHeight) {
			continue
		}
		entry, ok := groups[key]
		if !ok {
			entry = &group{plate: plan.Plate}
			groups[key] = entry
		}
		entry.places = append(entry.places, plan.Place)
	}
	if len(groups) == 0 {
		return nil
	}
	// Deterministic group order: plate id ascending.
	order := make([]string, 0, len(groups))
	for key := range groups {
		order = append(order, key)
	}
	sort.Strings(order)

	items := make([]OverlayItem, 0, len(order))
	for _, key := range order {
		entry := groups[key]
		pins, startUS, durationUS, score, ok := mapPinsFor(entry.plate, entry.places)
		if !ok {
			continue
		}
		mapAssets := []OverlayAssetRef{entry.plate.Asset}
		lods := []MapOverlayLOD{{
			AssetID:       entry.plate.Asset.AssetID,
			SourceID:      entry.plate.ID,
			SourceLicense: entry.plate.License,
			Attribution:   entry.plate.Attribution,
			Center:        entry.plate.Center,
			Zoom:          entry.plate.Zoom,
			Width:         entry.plate.Width,
			Height:        entry.plate.Height,
		}}
		for _, lod := range entry.plate.LODs {
			mapAssets = append(mapAssets, lod.Asset)
			lods = append(lods, MapOverlayLOD{
				AssetID:       lod.Asset.AssetID,
				SourceID:      lod.ID,
				SourceLicense: lod.License,
				Attribution:   lod.Attribution,
				Center:        lod.Center,
				Zoom:          lod.Zoom,
				Width:         lod.Width,
				Height:        lod.Height,
			})
		}
		mapOverlay := &MapOverlay{
			Provider: "local", SourceID: entry.plate.ID, SourceLicense: entry.plate.License,
			Center: entry.plate.Center, Zoom: entry.plate.Zoom,
			Width: entry.plate.Width, Height: entry.plate.Height,
			Attribution: entry.plate.Attribution, MotionID: mapMotionForCenter(ordinal, entry.plate.Center), Pins: pins,
			AreaGlowRadiusKM: mapAreaGlowRadiusKM(pins),
		}
		if len(lods) >= 2 {
			mapOverlay.LODs = lods
			from, to := routeEndpoints(entry.places, entry.plate.Center)
			mapOverlay.CameraMove = &MapCameraMove{
				From: from, To: to,
				StartZoom: float64(entry.plate.Zoom), EndZoom: float64(lods[len(lods)-1].Zoom),
			}
			// Give the camera route a full five seconds on screen. A map tied
			// only to the short spoken duration of one place rendered as a
			// brief flash, even though its camera move was designed as an
			// animation.
			durationUS = mapCameraDurationUS
		}
		items = append(items, OverlayItem{
			ID: itemID(sceneID, "map", entry.plate.ID), SceneID: sceneID,
			Kind: "map", TemplateID: "MAP",
			StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
			StartUS: startUS, DurationUS: durationUS,
			AssetRefs: mapAssets,
			Map:       mapOverlay,
			Params:    map[string]any{"priority": score},
		})
		ordinal++
	}
	return items
}

// routeEndpoints chooses the chronological first and last grounded locations;
// one grounded place intentionally becomes a stationary zoom-to, while a
// multi-place route follows spoken order. It never invents geography from
// spatial sorting or arithmetic averages.
func routeEndpoints(places []MapCandidate, fallback MapCenter) (MapCenter, MapCenter) {
	if len(places) == 0 {
		return fallback, fallback
	}
	ordered := append([]MapCandidate(nil), places...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartUS != ordered[j].StartUS {
			return ordered[i].StartUS < ordered[j].StartUS
		}
		return ordered[i].EntityID < ordered[j].EntityID
	})
	return MapCenter{Latitude: ordered[0].Latitude, Longitude: ordered[0].Longitude},
		MapCenter{Latitude: ordered[len(ordered)-1].Latitude, Longitude: ordered[len(ordered)-1].Longitude}
}

// plateFitsCanvas reports whether the plate's certified raster is exactly the
// canvas the plan renders. A mismatch means the plate's georeference cannot be
// honoured, so the plate is unusable — never scaled.
func plateFitsCanvas(plate MapPlate, canvasWidth, canvasHeight int) bool {
	if len(plate.LODs) == 0 {
		return plate.Width == canvasWidth && plate.Height == canvasHeight
	}
	return plate.Width >= canvasWidth && plate.Height >= canvasHeight &&
		plate.Width <= maxMapRasterWidth && plate.Height <= maxMapRasterHeight
}

// mapPinsFor projects the grounded places that fall inside the plate's window
// into deterministic pins plus the item's audio window. ok is false when no
// place lands inside the plate or no place carries a usable span.
func mapAreaGlowRadiusKM(pins []MapOverlayPin) float64 {
	if len(pins) == 0 {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(pins[0].Scope)) {
	case "city":
		return 3.5
	case "region":
		return 30
	case "country", "nation":
		return 150
	case "continent":
		return 500
	default:
		return 3.5
	}
}

func mapPinsFor(plate MapPlate, places []MapCandidate) (pins []MapOverlayPin, startUS, durationUS int64, score float64, ok bool) {
	// A multi-stop map is a journey, so its camera order follows the first
	// spoken occurrence rather than provider/entity extraction order.
	places = append([]MapCandidate(nil), places...)
	sort.SliceStable(places, func(i, j int) bool {
		if places[i].StartUS != places[j].StartUS {
			return places[i].StartUS < places[j].StartUS
		}
		return places[i].EntityID < places[j].EntityID
	})
	seen := make(map[string]struct{}, len(places))
	var firstStart, lastEnd int64
	haveSpan := false
	for _, place := range places {
		id := strings.TrimSpace(place.EntityID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		if !plate.Window.Contains(place.Latitude, place.Longitude) {
			continue
		}
		if place.DurationUS <= 0 || place.StartUS < 0 {
			continue
		}
		seen[id] = struct{}{}
		pins = append(pins, MapOverlayPin{
			ID: id, Label: place.Label,
			Latitude: place.Latitude, Longitude: place.Longitude,
			Scope: place.Scope,
			Color: mapPinColor, RadiusPX: mapPinRadiusPX,
		})
		placeEnd := place.StartUS + place.DurationUS
		if !haveSpan || place.StartUS < firstStart {
			firstStart = place.StartUS
		}
		if !haveSpan || placeEnd > lastEnd {
			lastEnd = placeEnd
		}
		haveSpan = true
		if place.Score > score {
			score = place.Score
		}
	}
	if len(pins) == 0 {
		return nil, 0, 0, 0, false
	}
	// Deterministic pin order: the stable entity id ascending.
	sort.SliceStable(pins, func(i, j int) bool { return pins[i].ID < pins[j].ID })
	durationUS = lastEnd - firstStart
	if durationUS <= 0 {
		return nil, 0, 0, 0, false
	}
	if durationUS > MaxImageOverlayDurationMS*1000 {
		durationUS = MaxImageOverlayDurationMS * 1000
	}
	return pins, firstStart, durationUS, score, true
}
