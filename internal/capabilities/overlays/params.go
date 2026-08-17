package overlays

import (
	"encoding/json"
	"strings"
)

func paramString(params map[string]any, key string) (string, bool) {
	v, ok := params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

// paramFloat reads a float override from item Params (e.g. the planner's
// "priority" score). Missing or unparsable values yield 0 (deterministic
// lowest priority).
func paramFloat(params map[string]any, key string) float64 {
	v, ok := params[key]
	if !ok {
		return 0
	}
	f, ok := toFloat(v)
	if !ok {
		return 0
	}
	return f
}

// paramFloatOpt reads a float override from item Params, reporting whether
// the key was present (unlike paramFloat, which silently defaults missing
// keys to 0). Used where an explicit override must win over a template
// default (e.g. LIGHT_LEAK opacity).
func paramFloatOpt(params map[string]any, key string) (float64, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	f, ok := toFloat(v)
	if !ok {
		return 0, false
	}
	return f, true
}

func paramInt(params map[string]any, key string) (int, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func paramPosition(params map[string]any, key string) ([]float64, bool) {
	v, ok := params[key]
	if !ok {
		return nil, false
	}
	raw, ok := v.([]any)
	if !ok || len(raw) < 2 || len(raw) > 3 {
		return nil, false
	}
	out := make([]float64, 0, len(raw))
	for _, e := range raw {
		f, ok := toFloat(e)
		if !ok {
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}

// paramAnimation reads a layer animation override from item Params, e.g.
// {"animation": {"preset": "fade_in"}}. The preset is required; an
// animation without one is ignored (fail-soft: the layer renders with its
// preset motions only).
func paramAnimation(params map[string]any, key string) (*ChrononLayerAnimation, bool) {
	v, ok := params[key]
	if !ok {
		return nil, false
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	preset, _ := raw["preset"].(string)
	if strings.TrimSpace(preset) == "" {
		return nil, false
	}
	out := &ChrononLayerAnimation{Preset: preset}
	if start, ok := paramInt64(raw, "start_frame"); ok {
		out.StartFrame = start
	}
	if duration, ok := paramInt64(raw, "duration_frames"); ok {
		out.DurationFrames = duration
	}
	if unit, ok := paramString(raw, "unit"); ok {
		out.Unit = unit
	}
	if enter, ok := paramWindow(raw, "enter"); ok {
		out.Enter = enter
	}
	if exit, ok := paramWindow(raw, "exit"); ok {
		out.Exit = exit
	}
	return out, true
}

// paramWindow reads a nested {"duration_frames": N} ramp window (enter/exit)
// from an animation params map. A missing or non-positive duration yields
// nil (fail-soft: the preset default window applies).
func paramWindow(params map[string]any, key string) (*ChrononAnimWindow, bool) {
	v, ok := params[key]
	if !ok {
		return nil, false
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	duration, ok := paramInt64(raw, "duration_frames")
	if !ok || duration <= 0 {
		return nil, false
	}
	return &ChrononAnimWindow{DurationFrames: duration}, true
}

// paramInt64 reads an int64 override from a nested params map (used by
// paramAnimation for the optional animation window).
func paramInt64(params map[string]any, key string) (int64, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

// paramBool reads a boolean override from item Params (e.g. LIGHT_LEAK
// "loop"). Missing or non-boolean values yield (false, false).
func paramBool(params map[string]any, key string) (bool, bool) {
	v, ok := params[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// paramColor reads an RGBA color override (exactly four floats) from item
// Params, e.g. {"color": [0.9, 0.1, 0.1, 0.8]}.
func paramColor(params map[string]any, key string) ([]float64, bool) {
	v, ok := params[key]
	if !ok {
		return nil, false
	}
	raw, ok := v.([]any)
	if !ok || len(raw) != 4 {
		return nil, false
	}
	out := make([]float64, 0, 4)
	for _, e := range raw {
		f, ok := toFloat(e)
		if !ok {
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
