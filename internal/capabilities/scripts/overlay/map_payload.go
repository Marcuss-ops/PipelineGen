// Package overlay — map_payload.go owns the pure map wire-payload
// projections the render queue's dynamic-map path sends to the Chronon
// renderer: the per-pin spoken timing block, the planner's MapGroup identity,
// and the item duration projection. Every helper is a pure map[string]any
// transformation — no renderer, no clock, no I/O — so the wire contract is
// testable in isolation from the subprocess path.
package overlay

import (
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
