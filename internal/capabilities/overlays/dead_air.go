package overlays

// Package overlays — dead_air.go owns the dead-air detector (logic only).
//
// Density budgets answer "too much at once"; this answers the opposite:
// stretches of a scene where NO overlay is on screen while narration runs.
// A long naked stretch is a boredom risk — the planner should fill it with
// a recap, a timeline strip or a card, never leave it flat. This file only
// FINDS the gaps (deterministic, pure inputs); filling policy lives with
// the caller. Sorted output: scene order, then start time.

import (
	"sort"
	"strings"
)

// SceneWindow is one scene's canonical playback window.
type SceneWindow struct {
	SceneID string
	StartUS int64
	EndUS   int64
}

// DeadAirGap is one naked stretch inside a scene window.
type DeadAirGap struct {
	SceneID    string `json:"scene_id"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	DurationMS int64  `json:"duration_ms"`
}

// FindDeadAirGaps returns every sub-window of the given scenes covered by
// no overlay item for at least minGapUS. Items count by their canonical
// microsecond span (StartUSValue/EndUSValue); structural items without a
// scene id still cover wall-clock when they overlap (a background is not
// dead air). Invalid windows and non-positive gaps are never invented:
// a non-positive minGapUS disables detection (nil).
// Deterministic: same windows + items → same gaps.
func FindDeadAirGaps(windows []SceneWindow, items []OverlayItem, minGapUS int64) []DeadAirGap {
	if minGapUS <= 0 {
		return nil
	}
	var out []DeadAirGap
	for _, w := range windows {
		sceneID := strings.TrimSpace(w.SceneID)
		if sceneID == "" || w.EndUS <= w.StartUS {
			continue
		}
		type span struct{ start, end int64 }
		var covered []span
		for _, it := range items {
			s, e := it.StartUSValue(), it.EndUSValue()
			if e <= w.StartUS || s >= w.EndUS {
				continue
			}
			if s < w.StartUS {
				s = w.StartUS
			}
			if e > w.EndUS {
				e = w.EndUS
			}
			if e > s {
				covered = append(covered, span{s, e})
			}
		}
		sort.Slice(covered, func(i, j int) bool { return covered[i].start < covered[j].start })
		cursor := w.StartUS
		for _, c := range covered {
			if c.start > cursor {
				if c.start-cursor >= minGapUS {
					out = append(out, DeadAirGap{SceneID: sceneID, StartUS: cursor, EndUS: c.start, DurationMS: (c.start - cursor) / 1000})
				}
				cursor = c.end
			} else if c.end > cursor {
				cursor = c.end
			}
			if cursor >= w.EndUS {
				break
			}
		}
		if w.EndUS-cursor >= minGapUS {
			out = append(out, DeadAirGap{SceneID: sceneID, StartUS: cursor, EndUS: w.EndUS, DurationMS: (w.EndUS - cursor) / 1000})
		}
	}
	if out == nil {
		return []DeadAirGap{}
	}
	return out
}

// SceneCoverage returns the fraction of each scene window covered by at
// least one overlay item (0..1, rounded to 3 decimals). Scenes with invalid
// windows are absent from the map, never zero-filled.
func SceneCoverage(windows []SceneWindow, items []OverlayItem) map[string]float64 {
	out := map[string]float64{}
	for _, w := range windows {
		sceneID := strings.TrimSpace(w.SceneID)
		if sceneID == "" || w.EndUS <= w.StartUS {
			continue
		}
		gaps := FindDeadAirGaps([]SceneWindow{w}, items, 1)
		naked := int64(0)
		for _, g := range gaps {
			naked += g.EndUS - g.StartUS
		}
		covered := w.EndUS - w.StartUS - naked
		if covered < 0 {
			covered = 0
		}
		frac := float64(covered) / float64(w.EndUS-w.StartUS)
		out[sceneID] = float64(int(frac*1000+0.5)) / 1000
	}
	return out
}
