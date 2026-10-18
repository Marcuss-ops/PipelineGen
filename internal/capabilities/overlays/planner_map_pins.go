package overlays

import "strings"

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

func mapPinsFor(plate MapPlate, places []MapCandidate) (pins []MapOverlayPin, pinTiming map[string]map[string]any, startUS, durationUS int64, score float64, ok bool) {
	// A multi-stop map is a journey, so its camera order follows the first
	// spoken occurrence rather than provider/entity extraction order.
	places = sortedMapPlacesSpokenOrder(places)
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
		return nil, nil, 0, 0, 0, false
	}
	// Preserve the chronological spoken order established above. The worker
	// reveals these points one at a time, so sorting by entity id would make
	// the animation contradict the narration.
	durationUS = lastEnd - firstStart
	if durationUS <= 0 {
		return nil, nil, 0, 0, 0, false
	}
	// No MaxImageOverlayDurationMS truncation here: truncating the map window
	// while keeping the original per-pin spoken offsets breaks the invariant
	// pin.start_us + pin.duration_us <= map.duration_us whenever mentions are
	// seconds apart (or a merged repeat occurrence spans the scene). Maps own
	// their duration policy in mapItemsForScene (camera minimum + per-pin read
	// slots, rendered as an 8s shot downstream); truncating here would schedule
	// a pin beyond the end of its own map. Never clamp timestamps: a pin must
	// activate exactly when it is spoken or the item fails closed below.
	// Per-pin timing is the voiceover's own schedule, clip-relative to the
	// item's start: each place activates exactly when it is spoken. It is the
	// input the renderer's camera follows, never a guess.
	pinTiming = make(map[string]map[string]any, len(places))
	for _, place := range places {
		id := strings.TrimSpace(place.EntityID)
		if id == "" {
			continue
		}
		if _, duplicate := pinTiming[id]; duplicate {
			continue
		}
		if !plate.Window.Contains(place.Latitude, place.Longitude) || place.DurationUS <= 0 || place.StartUS < 0 {
			continue
		}
		relStart := place.StartUS - firstStart
		// Overflow-safe invariant check before publishing: every pin must end
		// inside its own map window.
		if relStart < 0 || place.DurationUS > durationUS || relStart > durationUS-place.DurationUS {
			return nil, nil, 0, 0, 0, false
		}
		pinTiming[id] = map[string]any{
			"start_us":    relStart,
			"duration_us": place.DurationUS,
		}
	}
	if !ValidateMapPinTiming(pinTiming, durationUS) {
		return nil, nil, 0, 0, 0, false
	}
	return pins, pinTiming, firstStart, durationUS, score, true
}

// ValidateMapPinTiming enforces the single timing invariant shared by
// PipelineGen (planner, pre-render admission) and RenderingGen (compile
// admission): every pin satisfies start_us >= 0, duration_us > 0 and
// start_us + duration_us <= mapDurationUS, with overflow-safe arithmetic and
// no silent clamping. A pin shown at a different moment than spoken is a
// correctness failure, so violations fail closed.
func ValidateMapPinTiming(pinTiming map[string]map[string]any, mapDurationUS int64) bool {
	if mapDurationUS <= 0 {
		return false
	}
	for _, entry := range pinTiming {
		start, okStart := mapPinTimingNumber(entry, "start_us")
		dur, okDur := mapPinTimingNumber(entry, "duration_us")
		if !okStart || !okDur || start < 0 || dur <= 0 {
			return false
		}
		if start > mapDurationUS || dur > mapDurationUS || start > mapDurationUS-dur {
			return false
		}
	}
	return true
}

func mapPinTimingNumber(entry map[string]any, key string) (int64, bool) {
	switch v := entry[key].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v != float64(int64(v)) {
			return 0, false
		}
		return int64(v), true
	}
	return 0, false
}
