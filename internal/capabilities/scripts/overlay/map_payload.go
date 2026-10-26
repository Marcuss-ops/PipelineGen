// Package overlay — map_payload.go owns the pure map wire-payload
// projections the render queue's dynamic-map path sends to the Chronon
// renderer: the per-pin spoken timing block, the planner's MapGroup identity,
// and the item duration projection. Every helper is a pure map[string]any
// transformation — no renderer, no clock, no I/O — so the wire contract is
// testable in isolation from the subprocess path.
package overlay

import (
	"math"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MapPinTimingPayload projects the item's per-pin spoken timing (set by the
// planner in map_pin_timing) onto the renderer wire: one entry per pin, in pin
// order, with clip-relative seconds. The renderer activates each place at its
// spoken start; it never invents timing.
func MapPinTimingPayload(params map[string]any, pins []map[string]any) []map[string]any {
	if params == nil {
		return nil
	}
	timingByID := map[string]map[string]any{}
	switch raw := params["map_pin_timing"].(type) {
	case map[string]map[string]any:
		for id, entry := range raw {
			timingByID[id] = entry
		}
	case map[string]any:
		for id, entry := range raw {
			if typed, ok := entry.(map[string]any); ok {
				timingByID[id] = typed
			}
		}
	}
	out := make([]map[string]any, 0, len(timingByID))
	for _, pin := range pins {
		id, _ := pin["id"].(string)
		entry, ok := timingByID[id]
		if !ok {
			continue
		}
		startUS, hasStart := mapTimingInt64(entry, "start_us")
		durationUS, hasDuration := mapTimingInt64(entry, "duration_us")
		if !hasStart || !hasDuration {
			continue
		}
		out = append(out, map[string]any{
			"id":         id,
			"start_s":    float64(startUS) / 1_000_000,
			"duration_s": float64(durationUS) / 1_000_000,
		})
	}
	return out
}

// MustFloat coerces a JSON-decoded number to float64; anything else is zero.
func MustFloat(value any) float64 {
	if f, ok := value.(float64); ok {
		return f
	}
	return 0
}

func mapTimingInt64(entry map[string]any, key string) (int64, bool) {
	switch value := entry[key].(type) {
	case int64:
		return value, true
	case float64:
		return int64(value), true
	case int:
		return int64(value), true
	}
	return 0, false
}

// mapNativePinKey mirrors the native renderer's place identity: one stop per
// (lat, lon) rounded to 1e-4 degrees, first occurrence wins. The framing is
// computed over exactly the places the renderer draws.
func mapNativePinKey(lat, lon float64) [2]int64 {
	return [2]int64{int64(math.Round(lat * 1e4)), int64(math.Round(lon * 1e4))}
}

// MapNativeCameraFraming projects the single-anchor native camera framing for
// the certified base flyover. The anchor is the mean of the renderer-drawn
// unique places (one place: the place itself — the renderer's own default);
// the zoom interval is grounded data whenever the planner grounded one
// (single-place fly-to: the plate→LOD zooms) and a deterministic fit of the
// pin bounding box otherwise (a multi-place map must keep every place in
// frame — nothing is dropped). A single place with no grounded camera move
// leaves the zooms unset so the renderer runs its certified product dive.
// sendZooms=false tells the caller to omit both keys (never invented values).
func MapNativeCameraFraming(pins []map[string]any, frameWidth, frameHeight int, hasCameraMove bool, moveStartZoom, moveEndZoom float64) (anchorLat, anchorLon, startZoom, endZoom float64, sendZooms bool) {
	unique := make([]struct{ lat, lon float64 }, 0, len(pins))
	seen := map[[2]int64]bool{}
	for _, pin := range pins {
		lat, okLat := MapPinFloat(pin, "latitude")
		lon, okLon := MapPinFloat(pin, "longitude")
		if !okLat || !okLon {
			continue
		}
		key := mapNativePinKey(lat, lon)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, struct{ lat, lon float64 }{lat: lat, lon: lon})
	}
	if len(unique) == 0 {
		return 0, 0, 0, 0, false
	}
	sumLat, sumLon := 0.0, 0.0
	for _, p := range unique {
		sumLat += p.lat
		sumLon += p.lon
	}
	anchorLat, anchorLon = sumLat/float64(len(unique)), sumLon/float64(len(unique))
	if len(unique) == 1 {
		if !hasCameraMove {
			return anchorLat, anchorLon, 0, 0, false
		}
		return anchorLat, anchorLon, moveStartZoom, moveEndZoom, true
	}
	end := mapNativeFitZoom(unique, anchorLat, anchorLon, frameWidth, frameHeight)
	start := math.Max(2, end-2.5)
	return anchorLat, anchorLon, start, end, true
}

// mapNativeFitZoom is the largest zoom whose native frame keeps EVERY drawn
// place inside the 12% margin from the anchor. The native projection
// (geo_to_screen) is a plain slippy projection at 1:1: at zoom z the frame
// covers W×H world pixels of the 256·2^z world, so the fit bounds the worst
// place offset — not just the bounding box, which could clip an asymmetric
// cluster off the anchor-mean.
func mapNativeFitZoom(unique []struct{ lat, lon float64 }, anchorLat, anchorLon float64, frameWidth, frameHeight int) float64 {
	const (
		tileSize = 256.0
		margin   = 0.12
		minZoom  = 2.0
		maxZoom  = 17.8 // the certified product dive's end zoom
	)
	if frameWidth <= 0 || frameHeight <= 0 {
		return maxZoom
	}
	anchorX := (anchorLon + 180) / 360
	anchorY := mercatorYFraction(anchorLat)
	maxDX, maxDY := 0.0, 0.0
	for _, p := range unique {
		dx := math.Abs((p.lon+180)/360 - anchorX)
		dy := math.Abs(mercatorYFraction(p.lat) - anchorY)
		maxDX, maxDY = math.Max(maxDX, dx), math.Max(maxDY, dy)
	}
	scale := (1 - 2*margin) / 2 // margin per side around the anchor at frame centre
	limit := math.Inf(1)
	if maxDX > 0 {
		limit = math.Min(limit, float64(frameWidth)*scale/(tileSize*maxDX))
	}
	if maxDY > 0 {
		limit = math.Min(limit, float64(frameHeight)*scale/(tileSize*maxDY))
	}
	if math.IsInf(limit, 1) {
		return maxZoom
	}
	return math.Min(maxZoom, math.Max(minZoom, math.Log2(limit)))
}

// mercatorYFraction mirrors dynamic_tile_pyramid.latlon_to_global_px's y
// divided by the world size (lat clamped to the Mercator limit).
func mercatorYFraction(lat float64) float64 {
	lat = math.Max(math.Min(lat, 85.05112878), -85.05112878)
	rad := lat * math.Pi / 180
	return (1 - math.Log(math.Tan(rad)+1/math.Cos(rad))/math.Pi) * 0.5
}

// MapGroupPayload returns the planner's MapGroup identity (index/count/size).
func MapGroupPayload(params map[string]any) map[string]any {
	if group, ok := params["map_group"].(map[string]any); ok {
		return group
	}
	return nil
}

// MapGroupStrategyParam returns the planner's declared presentation strategy
// for the group ("tour" or "multi_pin").
func MapGroupStrategyParam(params map[string]any) string {
	strategy, _ := params["map_group_strategy"].(string)
	return strategy
}

// MapItemDurationMS projects the item's rendered window in milliseconds from
// its plan bounds, falling back to the caller's plan duration.
func MapItemDurationMS(item capabilityoverlay.OverlayItem, fallback int64) int64 {
	if item.StartMs >= 0 && item.EndMs > item.StartMs {
		return item.EndMs - item.StartMs
	}
	return fallback
}
