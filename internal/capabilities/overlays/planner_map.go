package overlays

import (
	"encoding/json"
	"log"
	"sort"
	"strconv"
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

func dedupeMapCandidatesByArticle(candidates []MapCandidate) []MapCandidate {
	out := make([]MapCandidate, 0, len(candidates))
	seen := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		key := mapCandidateArticleKey(candidate.Label)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(candidate.Label))
		}
		if index, duplicate := seen[key]; duplicate {
			if candidate.DurationUS > out[index].DurationUS ||
				(candidate.DurationUS == out[index].DurationUS && candidate.Score > out[index].Score) {
				out[index] = candidate
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, candidate)
	}
	return out
}

// mapCandidateArticleKey normalizes only a leading Italian article so a
// "La Francia" label collides with "Francia". It is deliberately narrower
// than general text normalization: it does not touch the rest of the name,
// so official article-bearing names that appear alone (La Spezia, L'Aquila)
// remain distinct candidates.
func mapCandidateArticleKey(label string) string {
	key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(label)), " "))
	for _, prefix := range []string{"l'", "l’"} {
		if strings.HasPrefix(key, prefix) {
			key = strings.TrimSpace(key[len(prefix):])
			break
		}
	}
	fields := strings.Fields(key)
	if len(fields) > 1 {
		switch fields[0] {
		case "la", "lo", "il", "le", "gli", "i", "un", "una", "uno":
			key = strings.Join(fields[1:], " ")
		}
	}
	return key
}

func preferAdministrativeMapCandidates(candidates []MapCandidate) []MapCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	hasAdministrative := false
	for _, candidate := range candidates {
		if !isStreetLevelMapLabel(candidate.Label) {
			hasAdministrative = true
			break
		}
	}
	if !hasAdministrative {
		return candidates
	}
	filtered := make([]MapCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !isStreetLevelMapLabel(candidate.Label) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func isStreetLevelMapLabel(label string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(label)))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "via", "viale", "piazza", "strada", "corso", "vicolo", "largo", "lungomare", "rotonda", "traversa":
		return true
	default:
		return false
	}
}

func mapCandidateTrace(candidates []MapCandidate) []string {
	trace := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		trace = append(trace, candidate.Label+"/"+candidate.EntityID)
	}
	return trace
}

// mergeMapCandidateOccurrence folds one spoken occurrence of an already-known
// place into its candidate: the pin spans from the earliest start to the last
// end, so a place named at the beginning and again at the end of a scene stays
// on screen for both mentions.
func mergeMapCandidateOccurrence(merged *MapCandidate, occurrence MapCandidate) {
	start := merged.StartUS
	end := merged.StartUS + merged.DurationUS
	if occurrence.StartUS < start {
		start = occurrence.StartUS
	}
	if occurrenceEnd := occurrence.StartUS + occurrence.DurationUS; occurrenceEnd > end {
		end = occurrenceEnd
	}
	merged.StartUS = start
	merged.DurationUS = end - start
	merged.StartMs = start / 1000
	merged.EndMs = (end + 999) / 1000
	if occurrence.Score > merged.Score {
		merged.Score = occurrence.Score
	}
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
func mapItemsForScene(sceneID string, plans []MapPlan, canvasWidth, canvasHeight, ordinal, motionLimit int, seed string, grouping MapGroupConfig) []capmodel.OverlayItem {
	if len(plans) == 0 || canvasWidth <= 0 || canvasHeight <= 0 {
		return nil
	}
	plans = coalesceMapPlansForNarrativeGrouping(plans, grouping)
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

	type unit struct {
		key    string
		plate  MapPlate
		places []MapCandidate
	}
	units := make([]unit, 0, len(order))
	for _, key := range order {
		entry := groups[key]
		// A candidate can represent a place mentioned repeatedly across a
		// long scene. For a map shot, retain its first grounded mention and
		// bound the pin's active window; later repeats must not stretch the
		// basemap to minutes of narration. This applies to flyovers too: a
		// route may cover several scenes, but each rendered map item still
		// has the same ten-second budget as a static plate.
		pinCap := grouping.Pins()
		shotPlaces := mapPlacesForShortPresentation(entry.places)
		var chunks [][]MapCandidate
		if len(entry.plate.LODs) >= 2 {
			// A flyover is a camera journey, not a reason to bypass the
			// presentation budget: chunk its stops by spoken order and then
			// split any chunk that still spans more than ten seconds.
			chunks = chunkMapPlaces(shotPlaces, pinCap)
		} else {
			// Narrative proximity groups STATIC map groups: places spoken in
			// quick succession become one MapGroup, split above the pin cap or
			// the presentation ceiling. The unified map_presentation contract
			// can narrow the pin cap or turn narrative grouping off entirely.
			chunks = groupMapPlacesByNarrativeProximity(shotPlaces, pinCap)
			if grouping.NoNarrativeGrouping {
				chunks = chunkMapPlaces(shotPlaces, pinCap)
			}
		}
		chunks = splitMapChunksByPresentationDuration(chunks, mapMaxPresentationUS)
		chunks = mergeShortFinalMapChunk(chunks, pinCap, mapMaxPresentationUS)
		for index, chunk := range chunks {
			unitKey := key
			if index > 0 {
				unitKey = key + "-" + strconv.Itoa(index+1)
			}
			units = append(units, unit{key: unitKey, plate: entry.plate, places: chunk})
		}
	}
	items := make([]capmodel.OverlayItem, 0, len(units))
	for unitIndex, u := range units {
		entry := &group{plate: u.plate, places: u.places}
		pins, pinTiming, startUS, durationUS, score, ok := mapPinsFor(entry.plate, entry.places)
		if !ok {
			continue
		}
		mapAssets := make([]capmodel.OverlayAssetRef, 0, len(entry.plate.LODs)+1)
		if entry.plate.Asset.AssetID != "" {
			mapAssets = append(mapAssets, entry.plate.Asset)
		}
		lods := []capmodel.MapOverlayLOD{{
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
			if lod.Asset.AssetID != "" {
				mapAssets = append(mapAssets, lod.Asset)
			}
			lods = append(lods, capmodel.MapOverlayLOD{
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
		// A camera map already owns its animation through CameraMove. Running
		// an image reveal/scale track on the same georeferenced plane compounds
		// both transforms and produces a distorted, double-exposed map during
		// the fly-to. Static plates still receive the centered image motion.
		motionID := ""
		if len(lods) < 2 {
			motionID = mapMotionForCenter(ordinal, entry.plate.Center)
		}
		mapOverlay := &capmodel.MapOverlay{
			Provider: "local", SourceID: entry.plate.ID, SourceLicense: entry.plate.License,
			Center: entry.plate.Center, Zoom: entry.plate.Zoom,
			Width: entry.plate.Width, Height: entry.plate.Height,
			// The selected motion is restricted to centered scale/opacity
			// treatments, so the basemap and projected pins remain registered.
			Attribution: entry.plate.Attribution, Pins: pins, MotionID: motionID,
			AreaGlowRadiusKM:    mapAreaGlowRadiusKM(pins),
			DynamicRendererOnly: entry.plate.RasterDeferred,
		}
		if len(lods) >= 2 {
			mapOverlay.LODs = lods
			from, to := routeEndpoints(entry.places, entry.plate.Center)
			mapOverlay.CameraMove = &capmodel.MapCameraMove{
				From: from, To: to,
				StartZoom: float64(entry.plate.Zoom), EndZoom: float64(lods[len(lods)-1].Zoom),
			}
		}
		// Every dynamic map gets the same minimum presentation duration,
		// including a stationary single-place fly-to with only one plate.
		// Otherwise its camera treatment would be cut off by a short mention.
		if durationUS < mapCameraDurationUS {
			durationUS = mapCameraDurationUS
		}
		// Reserve a readable one-second slot for every location after the
		// camera has settled. A crowded place sequence grows the map overlay
		// instead of piling every marker into the same few frames.
		minimumPinSequenceUS := mapCameraLeadUS + int64(len(pins))*mapPinReadDurationUS
		if durationUS < minimumPinSequenceUS {
			durationUS = minimumPinSequenceUS
		}
		// MapGroup identity + per-pin timing travel as free-form item params
		// (the same carrier as map_pin_extras), so no new field enters the
		// versioned overlay-plan.v1 map block without a schema migration.
		// The strategy is the planner's pre-render duration decision: a tour
		// only when the spoken span can carry it, otherwise a declared
		// multi-pin composition — never a silently dropped place.
		strategy := mapGroupTourStrategy(len(pins), durationUS)
		items = append(items, capmodel.OverlayItem{
			ID: itemID(sceneID, "map", u.key), SceneID: sceneID,
			Kind: "map", TemplateID: "MAP",
			StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
			StartUS: startUS, DurationUS: durationUS,
			AssetRefs: mapAssets,
			Map:       mapOverlay,
			Params: map[string]any{
				"priority": score,
				"map_group": map[string]any{
					"index": unitIndex + 1, "count": len(units), "size": len(pins),
				},
				"map_pin_timing":     pinTiming,
				"map_group_strategy": strategy,
			},
		})
		// Static plates get a seeded composition from the family that fits their
		// pin count. Camera flyovers keep their own CameraMove treatment.
		if len(lods) < 2 {
			id := items[len(items)-1].ID
			if composition := SelectMapComposition(seed, sceneID, id, len(pins)); composition != "" {
				items[len(items)-1].Params["map_animation"] = composition
			}
		}
		ordinal++
	}
	return items
}

// MaterializeMapCandidate resolves one Rust-selected, grounded place against
// the canonical map-plate catalog and lowers it to the renderer's certified
// local-raster/dynamic-map contract. Editorial timing and selection remain
// owned by the caller; this function only supplies map assets and provenance.
func MaterializeMapCandidate(sceneID string, candidate MapCandidate, resolver PlateResolver, canvasWidth, canvasHeight, ordinal, motionLimit int, seed string, grouping MapGroupConfig) (capmodel.OverlayItem, bool) {
	plans := mapPlansForScene(resolver, []MapCandidate{candidate}, canvasWidth, canvasHeight, grouping)
	items := mapItemsForScene(sceneID, plans, canvasWidth, canvasHeight, ordinal, motionLimit, seed, grouping)
	if len(items) == 0 {
		return capmodel.OverlayItem{}, false
	}
	return items[0], true
}

// MaterializeMapGroup lowers ALL grounded places of one scene together, so the
// certified grouping policy still applies when the editorial selection comes
// from the Rust planner. Materializing each Rust map item on its own resolves a
// private plate per place, which makes narrative grouping unreachable: three
// places named in quick succession became three separate maps (one per plate)
// instead of one map group with three pins. The returned items are in spoken
// order and already chunked by the pin cap and the presentation ceiling.
func MaterializeMapGroup(sceneID string, candidates []MapCandidate, resolver PlateResolver, canvasWidth, canvasHeight, ordinal, motionLimit int, seed string, grouping MapGroupConfig) []capmodel.OverlayItem {
	if len(candidates) == 0 {
		return nil
	}
	plans := mapPlansForScene(resolver, candidates, canvasWidth, canvasHeight, grouping)
	items := mapItemsForScene(sceneID, plans, canvasWidth, canvasHeight, ordinal, motionLimit, seed, grouping)
	return MergeShortMapTail(items, grouping.Pins(), MapMinimumReadableUS, seed)
}

// MapMinimumReadableUS is the shortest map presentation that is still a
// readable camera beat rather than a flash. A map that ends up shorter than
// this only because the next scene starts immediately after it is merged into
// the preceding map of the same scene, where its pin stays visible.
const MapMinimumReadableUS int64 = 2_500_000

// mergeShortMapTail folds a too-short final map of a scene into the preceding
// map when the previous certified plate covers the tail's pins and the pin cap
// allows it. It never drops a place: the pin, its label and its spoken timing
// move onto the earlier map. Maps with LOD routes are left untouched because
// merging would change the authored camera journey.
func MergeShortMapTail(items []capmodel.OverlayItem, maxPins int, minDurationUS int64, seed string) []capmodel.OverlayItem {
	if len(items) < 2 || minDurationUS <= 0 {
		return items
	}
	if maxPins <= 0 {
		maxPins = maxMapPinsPerGroup
	}
	byScene := make(map[string][]int)
	sceneOrder := make([]string, 0, len(items))
	for i := range items {
		if !strings.EqualFold(strings.TrimSpace(items[i].Kind), "map") {
			continue
		}
		sceneID := strings.TrimSpace(items[i].SceneID)
		if _, seen := byScene[sceneID]; !seen {
			sceneOrder = append(sceneOrder, sceneID)
		}
		byScene[sceneID] = append(byScene[sceneID], i)
	}
	dropped := make(map[int]struct{})
	for _, sceneID := range sceneOrder {
		indices := byScene[sceneID]
		sort.SliceStable(indices, func(a, b int) bool {
			left, right := items[indices[a]], items[indices[b]]
			if left.StartUS != right.StartUS {
				return left.StartUS < right.StartUS
			}
			return left.ID < right.ID
		})
		for len(indices) >= 2 {
			tailIndex := indices[len(indices)-1]
			previousIndex := indices[len(indices)-2]
			previous, tail := &items[previousIndex], &items[tailIndex]
			if tail.DurationUS <= 0 || tail.DurationUS >= minDurationUS {
				break
			}
			if !mergeableMapTail(*previous, *tail, maxPins) {
				break
			}
			mergeMapTail(previous, tail, seed)
			dropped[tailIndex] = struct{}{}
			indices = indices[:len(indices)-1]
		}
		renumberMapGroups(items, indices)
	}
	if len(dropped) == 0 {
		return items
	}
	kept := items[:0]
	for i := range items {
		if _, remove := dropped[i]; remove {
			continue
		}
		kept = append(kept, items[i])
	}
	return kept
}

// mergeableMapTail is the fail-closed precondition of a tail merge: both maps
// must be static, the earlier plate must cover every tail pin, and the merged
// map must stay within the certified pin cap.
func mergeableMapTail(previous, tail capmodel.OverlayItem, maxPins int) bool {
	if previous.Map == nil || tail.Map == nil {
		return false
	}
	if len(previous.Map.LODs) > 0 || len(tail.Map.LODs) > 0 {
		return false
	}
	if len(previous.Map.Pins) == 0 || len(tail.Map.Pins) == 0 {
		return false
	}
	if len(previous.Map.Pins)+len(tail.Map.Pins) > maxPins {
		return false
	}
	if previous.Map.Zoom <= 0 || previous.Map.Width <= 0 || previous.Map.Height <= 0 {
		return false
	}
	window := geodesy.CenteredOn(
		previous.Map.Center.Latitude, previous.Map.Center.Longitude,
		previous.Map.Zoom, previous.Map.Width, previous.Map.Height,
	)
	for _, pin := range tail.Map.Pins {
		if !window.Contains(pin.Latitude, pin.Longitude) {
			return false
		}
	}
	return true
}

func mergeMapTail(previous, tail *capmodel.OverlayItem, seed string) {
	offsetUS := tail.StartUS - previous.StartUS
	previous.Map.Pins = append(previous.Map.Pins, tail.Map.Pins...)
	if previous.Params == nil {
		previous.Params = make(map[string]any)
	}
	timing := make(map[string]map[string]any)
	for id, value := range mapPinTimingTable(previous.Params) {
		timing[id] = cloneMapPinTiming(value, 0)
	}
	for id, value := range mapPinTimingTable(tail.Params) {
		timing[id] = cloneMapPinTiming(value, offsetUS)
	}
	previous.Params["map_pin_timing"] = timing
	endUS := previous.StartUS + previous.DurationUS
	if tailEndUS := tail.StartUS + tail.DurationUS; tailEndUS > endUS {
		endUS = tailEndUS
	}
	previous.DurationUS = endUS - previous.StartUS
	previous.EndMs = (endUS + 999) / 1000
	pinCount := len(previous.Map.Pins)
	if group, ok := previous.Params["map_group"].(map[string]any); ok {
		group["size"] = pinCount
	}
	previous.Params["map_group_strategy"] = mapGroupTourStrategy(pinCount, previous.DurationUS)
	if composition := SelectMapComposition(seed, previous.SceneID, previous.ID, pinCount); composition != "" {
		previous.Params["map_animation"] = composition
	} else {
		delete(previous.Params, "map_animation")
	}
}

func mapPinTimingTable(params map[string]any) map[string]map[string]any {
	if params == nil {
		return nil
	}
	switch timing := params["map_pin_timing"].(type) {
	case map[string]map[string]any:
		return timing
	case map[string]any:
		out := make(map[string]map[string]any, len(timing))
		for id, value := range timing {
			if entry, ok := value.(map[string]any); ok {
				out[id] = entry
			}
		}
		return out
	default:
		return nil
	}
}

func cloneMapPinTiming(value map[string]any, offsetUS int64) map[string]any {
	clone := make(map[string]any, len(value))
	for key, field := range value {
		clone[key] = field
	}
	clone["start_us"] = mapPinTimingInt64(clone["start_us"]) + offsetUS
	return clone
}

func mapPinTimingInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func renumberMapGroups(items []capmodel.OverlayItem, indices []int) {
	count := len(indices)
	for position, index := range indices {
		if items[index].Params == nil {
			continue
		}
		if group, ok := items[index].Params["map_group"].(map[string]any); ok {
			group["index"] = position + 1
			group["count"] = count
		}
	}
}
