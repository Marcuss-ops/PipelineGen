package overlays

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// planner_map_group.go owns the map-group presentation strategy and the
// narrative-proximity grouping helpers extracted from planner_map.go to keep
// that file inside the godlike/08 max_lines_per_file_strict cap (600). The
// split is a pure move: the functions are the ones mapItemsForScene called
// before, unchanged.

// mapTourMinimumSeconds mirrors the dynamic renderer's authored multi-stop
// route budget: 3s opening beat plus 4.5s per travelled leg. The planner uses
// it BEFORE rendering to decide whether a stop-by-stop tour fits the spoken
// span, so a short beat gets a declared multi-pin composition instead of a
// render-time failure or a silently reduced tour.
func mapTourMinimumSeconds(pinCount int) float64 {
	if pinCount <= 1 {
		return 3.0
	}
	return 3.0 + 4.5*float64(pinCount-1)
}

// mapGroupTourStrategy is the planner's editorial decision for one map group:
// "tour" when the spoken duration can carry a stop-by-stop camera route, and
// "multi_pin" — a declared alternative composition that keeps every place on
// screen — when it cannot. The renderer never changes this content decision.
func mapGroupTourStrategy(pinCount int, durationUS int64) string {
	if pinCount <= 1 {
		return "tour"
	}
	if float64(durationUS)/1_000_000 >= mapTourMinimumSeconds(pinCount) {
		return "tour"
	}
	return "multi_pin"
}

// mapPlacesForShortPresentation keeps the first occurrence of each grounded
// candidate as the map's timing anchor. Entity spans can cover repeat mentions
// over a whole scene; only a span longer than the map's ten-second ceiling is
// shortened to the readable pin slot, because that span cannot fit any single
// map item. Short repeats stay intact so a pin mentioned twice still covers
// both spoken moments. The camera remains on screen for its normal eight-
// second presentation.
func mapPlacesForShortPresentation(places []MapCandidate) []MapCandidate {
	shotPlaces := append([]MapCandidate(nil), places...)
	for i := range shotPlaces {
		if shotPlaces[i].DurationUS > mapMaxPresentationUS {
			shotPlaces[i].DurationUS = mapPinReadDurationUS
			shotPlaces[i].EndMs = (shotPlaces[i].StartUS + shotPlaces[i].DurationUS + 999) / 1000
		}
	}
	return shotPlaces
}

// sortedMapPlacesSpokenOrder is the ONE spoken-order sort shared by grouping,
// pin projection and route endpoints: entity id breaks ties so the order is
// total and deterministic.
func sortedMapPlacesSpokenOrder(places []MapCandidate) []MapCandidate {
	ordered := append([]MapCandidate(nil), places...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartUS != ordered[j].StartUS {
			return ordered[i].StartUS < ordered[j].StartUS
		}
		return ordered[i].EntityID < ordered[j].EntityID
	})
	return ordered
}

// splitMapPlacesByPresentationDuration applies the hard render ceiling: a
// chunk of places that spans more than maxDurationUS is split at the spoken
// starts. The boundary is taken from the actual grounded windows, never from
// an estimate or a scene label, and every place lands in exactly one chunk.
func splitMapPlacesByPresentationDuration(places []MapCandidate, maxDurationUS int64) [][]MapCandidate {
	if len(places) == 0 || maxDurationUS <= 0 {
		return nil
	}
	ordered := sortedMapPlacesSpokenOrder(places)
	chunks := make([][]MapCandidate, 0, len(ordered))
	current := make([]MapCandidate, 0, len(ordered))
	var startUS int64
	for _, place := range ordered {
		if len(current) == 0 {
			current = append(current, place)
			startUS = place.StartUS
			continue
		}
		endUS := place.StartUS + place.DurationUS
		if endUS-startUS > maxDurationUS {
			chunks = append(chunks, current)
			current = []MapCandidate{place}
			startUS = place.StartUS
			continue
		}
		current = append(current, place)
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

// MapGroupPinsFromParams decodes the group the Rust planner wrote into
// params.map_group_pins. Membership, spoken order and each pin's window are the
// planner's decision; this decoder only validates them into MapCandidates, so a
// malformed group fails closed instead of rendering a guessed place. An absent
// key means "no group declared" (a single Rust map candidate).
func MapGroupPinsFromParams(params map[string]any) ([]MapCandidate, error) {
	raw, ok := params["map_group_pins"].([]any)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	pins := make([]MapCandidate, 0, len(raw))
	for _, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("map_group_pins entry is not an object")
		}
		entityID := strings.TrimSpace(mapFieldString(fields, "entity_id"))
		candidate, valid := NewMapCandidate(
			entityID,
			mapFieldString(fields, "label"),
			mapFieldFloat(fields, "latitude"),
			mapFieldFloat(fields, "longitude"),
			mapFieldInt(fields, "start_us"),
			mapFieldInt(fields, "duration_us"),
			1,
			mapFieldString(fields, "scope"),
		)
		if !valid {
			return nil, fmt.Errorf("map_group_pins entry %q is not a grounded place", entityID)
		}
		pins = append(pins, candidate)
	}
	return pins, nil
}

func mapFieldString(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func mapFieldFloat(fields map[string]any, key string) float64 {
	switch value := fields[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}

func mapFieldInt(fields map[string]any, key string) int64 {
	switch value := fields[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	}
	return 0
}

// ── plate groups and shot units: how a scene's places become map items ──

// mapPlateGroup is the set of grounded places one certified plate covers.
type mapPlateGroup struct {
	plate  MapPlate
	places []MapCandidate
}

// mapShotUnit is one rendered map shot: a group's plate with the places the
// presentation budget lets that shot carry.
type mapShotUnit struct {
	key    string
	plate  MapPlate
	places []MapCandidate
}

// mapPlateGroups folds the plans onto the plate they were resolved for. A plan
// without a plate id cannot be grouped, and a plate that is not the canvas
// size is not a map this scene can render.
func mapPlateGroups(plans []MapPlan, canvasWidth, canvasHeight int) (map[string]*mapPlateGroup, []string) {
	groups := make(map[string]*mapPlateGroup, len(plans))
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
			entry = &mapPlateGroup{plate: plan.Plate}
			groups[key] = entry
		}
		entry.places = append(entry.places, plan.Place)
	}
	if len(groups) == 0 {
		return nil, nil
	}
	// Deterministic group order: plate id ascending.
	order := make([]string, 0, len(groups))
	for key := range groups {
		order = append(order, key)
	}
	sort.Strings(order)
	return groups, order
}

// mapShotUnits splits every plate group into the shots the presentation budget
// allows, in deterministic plate order.
func mapShotUnits(groups map[string]*mapPlateGroup, order []string, grouping MapGroupConfig) []mapShotUnit {
	units := make([]mapShotUnit, 0, len(order))
	for _, key := range order {
		entry := groups[key]
		// A candidate can represent a place mentioned repeatedly across a
		// long scene. For a map shot, retain its first grounded mention and
		// bound the pin's active window; later repeats must not stretch the
		// basemap to minutes of narration. This applies to flyovers too: a
		// route may cover several scenes, but each rendered map item still
		// has the same ten-second budget as a static plate.
		// Membership is already decided: the Rust planner grouped these places
		// (narrative proximity, pin cap, readable floor) and the plate group
		// only says which places one certified raster can carry. The ceiling
		// stays here because it is a render limit, not an editorial choice.
		shotPlaces := mapPlacesForShortPresentation(entry.places)
		chunks := splitMapPlacesByPresentationDuration(shotPlaces, mapMaxPresentationUS)
		for index, chunk := range chunks {
			unitKey := key
			if index > 0 {
				unitKey = key + "-" + strconv.Itoa(index+1)
			}
			units = append(units, mapShotUnit{key: unitKey, plate: entry.plate, places: chunk})
		}
	}
	return units
}

// mapItemForShotUnit renders one shot. ok is false when no pin survives the
// plate's window with a usable spoken span, and the shot then produces no item.
func mapItemForShotUnit(sceneID string, unit mapShotUnit, unitIndex, unitCount, ordinal int, resolver PlateResolver, canvasWidth, canvasHeight int, seed string) (capmodel.OverlayItem, bool) {
	plate := certifyUnitRoute(unit.plate, unit.places, resolver, canvasWidth, canvasHeight)
	pins, pinTiming, startUS, durationUS, score, ok := mapPinsFor(plate, unit.places)
	if !ok {
		return capmodel.OverlayItem{}, false
	}
	mapAssets, lods := mapPlateLadder(plate)
	// A camera map already owns its animation through CameraMove. Running
	// an image reveal/scale track on the same georeferenced plane compounds
	// both transforms and produces a distorted, double-exposed map during
	// the fly-to. Static plates still receive the centered image motion.
	motionID := ""
	if len(lods) < 2 {
		motionID = mapMotionForCenter(ordinal, plate.Center)
	}
	mapOverlay := &capmodel.MapOverlay{
		Provider: "local", SourceID: plate.ID, SourceLicense: plate.License,
		Center: plate.Center, Zoom: plate.Zoom,
		Width: plate.Width, Height: plate.Height,
		// The selected motion is restricted to centered scale/opacity
		// treatments, so the basemap and projected pins remain registered.
		Attribution: plate.Attribution, Pins: pins, MotionID: motionID,
		AreaGlowRadiusKM:    mapAreaGlowRadiusKM(pins),
		DynamicRendererOnly: plate.RasterDeferred,
	}
	if len(lods) >= 2 {
		mapOverlay.LODs = lods
		from, to := routeEndpoints(unit.places, plate.Center)
		mapOverlay.CameraMove = &capmodel.MapCameraMove{
			From: from, To: to,
			StartZoom: float64(plate.Zoom), EndZoom: float64(lods[len(lods)-1].Zoom),
		}
	}
	durationUS = mapShotDurationUS(durationUS, len(pins))
	// MapGroup identity + per-pin timing travel as free-form item params
	// (the same carrier as map_pin_extras), so no new field enters the
	// versioned overlay-plan.v1 map block without a schema migration.
	// The strategy is the planner's pre-render duration decision: a tour
	// only when the spoken span can carry it, otherwise a declared
	// multi-pin composition — never a silently dropped place.
	item := capmodel.OverlayItem{
		ID: itemID(sceneID, "map", unit.key), SceneID: sceneID,
		Kind: "map", TemplateID: "MAP",
		StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
		StartUS: startUS, DurationUS: durationUS,
		AssetRefs: mapAssets,
		Map:       mapOverlay,
		Params: map[string]any{
			"priority": score,
			"map_group": map[string]any{
				"index": unitIndex + 1, "count": unitCount, "size": len(pins),
			},
			"map_pin_timing":     pinTiming.Params(),
			"map_group_strategy": mapGroupTourStrategy(len(pins), durationUS),
		},
	}
	// Static plates get a seeded composition from the family that fits their
	// pin count. Camera flyovers keep their own CameraMove treatment.
	if len(lods) < 2 {
		if composition := SelectMapComposition(seed, sceneID, item.ID, len(pins)); composition != "" {
			item.Params["map_animation"] = composition
		}
	}
	return item, true
}

// mapPlateLadder projects the certified plate and its LODs onto the item's
// asset refs and overlay ladder.
func mapPlateLadder(plate MapPlate) ([]capmodel.OverlayAssetRef, []capmodel.MapOverlayLOD) {
	assets := make([]capmodel.OverlayAssetRef, 0, len(plate.LODs)+1)
	if plate.Asset.AssetID != "" {
		assets = append(assets, plate.Asset)
	}
	lods := []capmodel.MapOverlayLOD{{
		AssetID:       plate.Asset.AssetID,
		SourceID:      plate.ID,
		SourceLicense: plate.License,
		Attribution:   plate.Attribution,
		Center:        plate.Center,
		Zoom:          plate.Zoom,
		Width:         plate.Width,
		Height:        plate.Height,
	}}
	for _, lod := range plate.LODs {
		if lod.Asset.AssetID != "" {
			assets = append(assets, lod.Asset)
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
	return assets, lods
}

// mapShotDurationUS applies the map duration policy: the camera minimum, plus
// a readable one-second slot for every location after the camera has settled.
// A crowded place sequence grows the map overlay instead of piling every
// marker into the same few frames.
func mapShotDurationUS(spanUS int64, pinCount int) int64 {
	if spanUS < mapCameraDurationUS {
		spanUS = mapCameraDurationUS
	}
	if minimum := mapCameraLeadUS + int64(pinCount)*mapPinReadDurationUS; spanUS < minimum {
		spanUS = minimum
	}
	return spanUS
}
