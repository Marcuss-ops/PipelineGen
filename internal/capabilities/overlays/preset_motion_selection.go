// Package overlays — preset_motion_selection.go: the deterministic phrase /
// text motion selectors.
//
// Split out of preset_selection.go to respect max_lines_per_file_strict=600
// (same package, no behavior change).
package overlays

import (
	"strings"
)

// Animation-count lanes. Each lane is ONE canonical key plus its aliases,
// resolved in a single place so every call site reading the same pool sees
// the same limit. Scattered inline key lists drifted apart (the
// images-with-text pool was capped by two different alias sets in different
// planner branches, so one alias was silently ignored on one path). Adding a
// lane means adding one entry here; call sites use laneAnimationCount.

func uniqueMotionPool(pool []string) []string {
	unique := make([]string, 0, len(pool))
	seen := make(map[string]struct{}, len(pool))
	for _, id := range pool {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}
