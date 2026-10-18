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
	if durationUS > MaxImageOverlayDurationMS*1000 {
		durationUS = MaxImageOverlayDurationMS * 1000
	}
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
		pinTiming[id] = map[string]any{
			"start_us":    place.StartUS - firstStart,
			"duration_us": place.DurationUS,
		}
	}
	return pins, pinTiming, firstStart, durationUS, score, true
}
