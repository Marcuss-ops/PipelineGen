package overlays

import (
	"encoding/json"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// MapPinTiming is one place's spoken schedule inside a map item, clip-relative
// to the item's start. The tags are the wire contract: a schedule stored in
// the item params and decoded again by the render queue must survive the round
// trip unchanged.
type MapPinTiming struct {
	StartUS    int64 `json:"start_us"`
	DurationUS int64 `json:"duration_us"`
}

// MapPinTimings is the per-pin spoken schedule of one map item, keyed by pin
// id. It is the only representation the planner, the short-tail merge and the
// renderer projection exchange: no caller does arithmetic on stringly-typed
// field lookups.
type MapPinTimings map[string]MapPinTiming

// Params returns the canonical item-params shape
// ({"<pin id>": {"start_us": n, "duration_us": n}}), so the typed schedule
// rides the overlay plan without a schema change.
func (t MapPinTimings) Params() map[string]any {
	if len(t) == 0 {
		return nil
	}
	out := make(map[string]any, len(t))
	for id, entry := range t {
		out[id] = map[string]any{"start_us": entry.StartUS, "duration_us": entry.DurationUS}
	}
	return out
}

// WithOffset returns the schedule shifted by offsetUS. A tail map merged into
// an earlier one keeps its own clip-relative schedule, moved to where it now
// starts.
func (t MapPinTimings) WithOffset(offsetUS int64) MapPinTimings {
	out := make(MapPinTimings, len(t))
	for id, entry := range t {
		out[id] = MapPinTiming{StartUS: entry.StartUS + offsetUS, DurationUS: entry.DurationUS}
	}
	return out
}

// Merge returns the union of two schedules, with other shifted by offsetUS.
// A colliding pin id keeps the earlier map's schedule: the pin that was
// already on that map keeps its spoken moment.
func (t MapPinTimings) Merge(other MapPinTimings, offsetUS int64) MapPinTimings {
	out := make(MapPinTimings, len(t)+len(other))
	for id, entry := range t {
		out[id] = entry
	}
	for id, entry := range other.WithOffset(offsetUS) {
		if _, exists := out[id]; exists {
			continue
		}
		out[id] = entry
	}
	return out
}

// Validate enforces the single timing invariant shared by PipelineGen
// (planner, pre-render admission) and RenderingGen (compile admission): every
// pin satisfies start_us >= 0, duration_us > 0 and
// start_us + duration_us <= mapDurationUS, with overflow-safe arithmetic and
// no silent clamping. A pin shown at a different moment than spoken is a
// correctness failure, so violations fail closed.
func (t MapPinTimings) Validate(mapDurationUS int64) bool {
	if mapDurationUS <= 0 {
		return false
	}
	for _, entry := range t {
		if entry.StartUS < 0 || entry.DurationUS <= 0 {
			return false
		}
		if entry.StartUS > mapDurationUS || entry.DurationUS > mapDurationUS ||
			entry.StartUS > mapDurationUS-entry.DurationUS {
			return false
		}
	}
	return true
}

// MapPinTimingsFromParams decodes the planner's map_pin_timing block from an
// overlay item's params. Both the in-process typed value and the generic maps
// of a JSON-decoded plan are accepted. ok is false when the block is present
// but is not a complete timing table: a malformed schedule must never be read
// as "no timing", or the merge and the renderer projection would silently drop
// pins that were actually spoken.
func MapPinTimingsFromParams(params map[string]any) (MapPinTimings, bool) {
	if params == nil {
		return nil, false
	}
	raw, present := params["map_pin_timing"]
	if !present {
		return nil, false
	}
	return mapPinTimingsFrom(raw)
}

// HasMapPinTiming reports whether the item declares a per-pin schedule at all.
func HasMapPinTiming(params map[string]any) bool {
	if params == nil {
		return false
	}
	_, present := params["map_pin_timing"]
	return present
}

func mapPinTimingsFrom(raw any) (MapPinTimings, bool) {
	switch typed := raw.(type) {
	case MapPinTimings:
		return typed, true
	case map[string]MapPinTiming:
		return MapPinTimings(typed), true
	case map[string]map[string]any:
		// The planner's original storage shape, still present in committed
		// plans and hand-built fixtures.
		out := make(MapPinTimings, len(typed))
		for id, value := range typed {
			entry, ok := mapPinTimingFrom(value)
			if !ok {
				return nil, false
			}
			out[id] = entry
		}
		return out, true
	case map[string]any:
		out := make(MapPinTimings, len(typed))
		for id, value := range typed {
			entry, ok := mapPinTimingFrom(value)
			if !ok {
				// A null entry carries no schedule: the item declared no
				// timing for that pin rather than an unreadable one.
				if value == nil {
					continue
				}
				return nil, false
			}
			out[id] = entry
		}
		return out, true
	default:
		return nil, false
	}
}

func mapPinTimingFrom(value any) (MapPinTiming, bool) {
	switch entry := value.(type) {
	case MapPinTiming:
		return entry, true
	case map[string]any:
		start, okStart := mapTimingInt64(entry, "start_us")
		duration, okDuration := mapTimingInt64(entry, "duration_us")
		if !okStart || !okDuration {
			return MapPinTiming{}, false
		}
		return MapPinTiming{StartUS: start, DurationUS: duration}, true
	default:
		return MapPinTiming{}, false
	}
}

// mapTimingInt64 reads one integer field of a timing entry. JSON decoding
// yields float64/json.Number, the planner writes int64: all three are the same
// number and none is guessed.
func mapTimingInt64(entry map[string]any, key string) (int64, bool) {
	switch value := entry[key].(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case float64:
		if value != float64(int64(value)) {
			return 0, false
		}
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// mapAreaGlowRadiusKM sizes the glow ring for the map's first pin.
func mapAreaGlowRadiusKM(pins []capmodel.MapOverlayPin) float64 {
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

// mapPinsFor projects the grounded places that fall inside the plate's window
// into deterministic pins plus the item's audio window. ok is false when no
// place lands inside the plate or no place carries a usable span.
func mapPinsFor(plate MapPlate, places []MapCandidate) (pins []capmodel.MapOverlayPin, pinTiming MapPinTimings, startUS, durationUS int64, score float64, ok bool) {
	// A multi-stop map is a journey, so its camera order follows the first
	// spoken occurrence rather than provider/entity extraction order.
	places = sortedMapPlacesSpokenOrder(places)
	seen := make(map[string]struct{}, len(places))
	accepted := make([]MapCandidate, 0, len(places))
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
		accepted = append(accepted, place)
		pins = append(pins, capmodel.MapOverlayPin{
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
	pinTiming = make(MapPinTimings, len(accepted))
	for _, place := range accepted {
		relStart := place.StartUS - firstStart
		// Overflow-safe invariant check before publishing: every pin must end
		// inside its own map window.
		if relStart < 0 || place.DurationUS > durationUS || relStart > durationUS-place.DurationUS {
			return nil, nil, 0, 0, 0, false
		}
		pinTiming[strings.TrimSpace(place.EntityID)] = MapPinTiming{
			StartUS:    relStart,
			DurationUS: place.DurationUS,
		}
	}
	if !pinTiming.Validate(durationUS) {
		return nil, nil, 0, 0, 0, false
	}
	return pins, pinTiming, firstStart, durationUS, score, true
}
