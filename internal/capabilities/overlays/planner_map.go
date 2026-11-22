package overlays

import (
	"log"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

// mapMotionIDs are the ONLY motions a map item may carry: the certified CENTERED
// image-motion pool, which the published overlay-plan.v1 map.motion_id enum also
// lists. They are referenced, never re-listed, so the producer cannot drift from
// the certified catalog and can never emit a motion the worker rejects. A map
// must stay on center (a panning or sliding basemap would move the geography
// away from the pins projected over it).
func mapMotionIDs() []string {
	return centeredImageMotionCandidates
}

// One certified marker treatment for every grounded place, so a map's pins are
// deterministic and never invented per item.
const (
	mapPinColor               = "#FF3B30"
	mapPinRadiusPX            = 10.0
	mapCameraDurationUS int64 = 8_000_000
	// mapMaxPresentationUS is the hard render ceiling for one map item. A
	// longer spoken span is segmented into consecutive bounded map items; a
	// map is never stretched across minutes of narration.
	mapMaxPresentationUS int64 = 10_000_000
	mapPinReadDurationUS int64 = 1_000_000
	mapCameraLeadUS      int64 = 2_000_000
)

// maxMapPinsPerGroup caps how many grounded places one static map shows. A
// plate with more places is split into consecutive map items in spoken order,
// so no place is dropped and each map stays readable.
const maxMapPinsPerGroup = 5

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
	Center        capmodel.MapCenter
	Zoom          int
	Width, Height int
	Asset         capmodel.OverlayAssetRef
	Window        geodesy.Window
	// RasterDeferred means this map is planned for the dynamic map renderer,
	// which fetches and certifies its own tiles. The descriptor still carries
	// the exact projected windows used by route validation, but no PNG is
	// generated or staged for this path.
	RasterDeferred bool
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

// mapPlansForScene resolves a scene's grounded place candidates against the
// run's certified plates. It fails closed at each step: a nil resolver, a
// candidate that no longer validates as grounded WGS84 with a real audio span,
// a duplicate stable entity id, or a point no certified plate covers produces
// no plan. The returned order is the caller's candidate order; mapItemsForScene
// imposes the final deterministic (plate id) order.
type FlyoverPlateResolver interface {
	ResolveFlyover(from, to capmodel.MapCenter, canvasWidth, canvasHeight int) (MapPlate, bool)
}

func mapPlansForScene(resolver PlateResolver, candidates []MapCandidate, canvasWidth, canvasHeight int, grouping MapGroupConfig) []MapPlan {
	log.Printf("map resolver trace: candidates=%v width=%d height=%d resolver_nil=%t", mapCandidateTrace(candidates), canvasWidth, canvasHeight, resolver == nil)
	if resolver == nil || len(candidates) == 0 {
		return nil
	}
	// A street name is ambiguous without a city: "via Pascoli" geocoded to
	// Treviso while the same scene explicitly named Garlasco and Pavia. When
	// the scene also contains an administrative place, never let the street
	// win the single-map plate (or a pin) over the town/province. A street-only
	// scene keeps its street map because there is no better candidate.
	candidates = preferAdministrativeMapCandidates(candidates)
	// The camera level also decides what may be labelled: a municipality plate
	// reads as a city frame, so a country or a body of water pinned inside it is
	// noise the viewer has to filter out. An empty level keeps every candidate.
	candidates = FilterMapCandidatesByLevel(candidates, grouping.Level)
	// A generated map is limited to one plate for this run. Use repeated,
	// grounded mentions as a relevance signal so a secondary place named once
	// (for example, the city where a document is held) cannot displace the
	// story's main location from that single map.
	mentionCount := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		if id := strings.TrimSpace(candidate.EntityID); id != "" {
			mentionCount[id]++
		}
	}
	valid := make([]MapCandidate, 0, len(candidates))
	seen := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		if count := mentionCount[strings.TrimSpace(candidate.EntityID)]; count > 1 {
			candidate.Score += float64(count-1) * 0.01
			if candidate.Score > 1 {
				candidate.Score = 1
			}
		}
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
		// Repeated mentions of one place are OCCURRENCES, not duplicates to
		// discard: the pin must stay active for every spoken mention, so the
		// merged candidate spans from the first start to the last end.
		if index, duplicate := seen[place.EntityID]; duplicate {
			mergeMapCandidateOccurrence(&valid[index], place)
			continue
		}
		seen[place.EntityID] = len(valid)
		valid = append(valid, place)
	}
	if len(valid) == 0 {
		return nil
	}
	// NER can surface the same country twice, once with its Italian article
	// ("La Francia" / "L'Italia") and once without. Keep the longer grounded
	// occurrence and drop the article variant before it becomes a second map
	// with a near-zero window.
	valid = dedupeMapCandidatesByArticle(valid)
	valid = dedupeMapCandidatesByCompoundPlace(valid)
	if flyovers, ok := resolver.(FlyoverPlateResolver); ok && !grouping.NoRoutes && len(valid) > 0 && canvasWidth > 0 && canvasHeight > 0 {
		from, to := routeEndpoints(valid, capmodel.MapCenter{Latitude: valid[0].Latitude, Longitude: valid[0].Longitude})
		if plate, ok := flyovers.ResolveFlyover(from, to, canvasWidth, canvasHeight); ok && strings.TrimSpace(plate.ID) != "" && len(plate.LODs) > 0 {
			log.Printf("map resolver trace: flyover resolved candidates=%v plate=%s lods=%d", mapCandidateTrace(valid), plate.ID, len(plate.LODs))
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
				log.Printf("map resolver trace: flyover covers all candidates=%v", mapCandidateTrace(valid))
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
			log.Printf("map resolver trace: flyover does not cover all candidates=%v", mapCandidateTrace(valid))
		}
	}
	plans := make([]MapPlan, 0, len(valid))
	for _, place := range valid {
		plate, ok := resolver.ResolvePlate(place.Latitude, place.Longitude)
		log.Printf("map resolver trace: place=%s entity=%s resolved=%t plate=%s", place.Label, place.EntityID, ok, plate.ID)
		if !ok || strings.TrimSpace(plate.ID) == "" {
			continue
		}
		plans = append(plans, MapPlan{Plate: plate, Place: place})
	}
	return plans
}

func mapCandidateTrace(candidates []MapCandidate) []string {
	trace := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		trace = append(trace, candidate.Label+"/"+candidate.EntityID)
	}
	return trace
}

// mapMotionForCenter selects from only map-safe centered treatments. The
// plan-level ordinal rotates maps so adjacent plates vary; regional bounds no
// longer select deprecated country recipes for arbitrary local rasters.
func mapMotionForCenter(ordinal int, _ capmodel.MapCenter) string {
	return mapMotionForCenterWithLimit(ordinal, capmodel.MapCenter{}, len(mapMotionIDs()))
}

func mapMotionForCenterWithLimit(ordinal int, _ capmodel.MapCenter, limit int) string {
	pool := mapMotionIDs()
	if len(pool) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(pool) {
		limit = len(pool)
	}
	if ordinal < 0 {
		ordinal = 0
	}
	return pool[ordinal%limit]
}

// mapItemsForScene lowers a scene's resolved map plans to at most one overlay
// item per certified plate. It fails closed at every step: a plan without a
// plate or a place, a plate whose raster is not the canvas size, a place
// outside the plate's window, or an empty audio span produces no item — never
// a map pinned at a guessed location.
func mapItemsForScene(sceneID string, plans []MapPlan, resolver PlateResolver, canvasWidth, canvasHeight, ordinal, motionLimit int, seed string, grouping MapGroupConfig) []capmodel.OverlayItem {
	if len(plans) == 0 || canvasWidth <= 0 || canvasHeight <= 0 {
		return nil
	}
	plans = coalesceMapPlansForNarrativeGrouping(plans, grouping)
	groups, order := mapPlateGroups(plans, canvasWidth, canvasHeight)
	if len(order) == 0 {
		return nil
	}
	units := mapShotUnits(groups, order, grouping)
	items := make([]capmodel.OverlayItem, 0, len(units))
	for unitIndex, unit := range units {
		item, ok := mapItemForShotUnit(sceneID, unit, unitIndex, len(units), ordinal, resolver, canvasWidth, canvasHeight, seed)
		if !ok {
			continue
		}
		items = append(items, item)
		ordinal++
	}
	return items
}

// MaterializeMapPins lowers ONE Rust-decided map group onto the certified
// plate catalog. Membership, spoken order, the pin cap, the presentation
// ceiling and the readable floor are decided by the Rust planner and travel in
// params.map_group_pins / params.map_pin_timing; this function only attaches
// the certified plate and its raster. It fails closed when no certified plate
// covers the group: a group no single plate covers is split by plate, so a
// place is never dropped and no pin is ever placed on a guessed raster.
func MaterializeMapPins(sceneID string, pins []MapCandidate, resolver PlateResolver, canvasWidth, canvasHeight, ordinal, motionLimit int, seed string, grouping MapGroupConfig) []capmodel.OverlayItem {
	if len(pins) == 0 {
		return nil
	}
	plans := mapPlansForScene(resolver, pins, canvasWidth, canvasHeight, grouping)
	return mapItemsForScene(sceneID, plans, resolver, canvasWidth, canvasHeight, ordinal, motionLimit, seed, grouping)
}
