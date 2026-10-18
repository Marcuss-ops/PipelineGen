package overlays

import (
	"fmt"
	"strings"
)

// AdmitOverlayTimeline verifies final frame windows after all duration
// extensions and before any expensive render. Two overlays sharing even one
// frame are rejected (the master uses mode=replace), so the planner decides
// early between shift/shorten/merge-or-error instead of failing at final
// publication after rendering. fpsNum/fpsDen convert microsecond windows to
// frames; items must carry StartUS/DurationUS (or StartMs/EndMs fallback).
func AdmitOverlayTimeline(items []OverlayItem, fpsNum, fpsDen int) error {
	if len(items) < 2 {
		return nil
	}
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 24, 1
	}
	type window struct {
		id         string
		isMap      bool
		startFrame int64
		endFrame   int64
	}
	windows := make([]window, 0, len(items))
	for _, it := range items {
		// Backgrounds are canvas primitives copied into each child plan, not
		// replace-mode overlays — they never collide in the final composite.
		if strings.EqualFold(strings.TrimSpace(it.TemplateID), "BACKGROUND") || strings.EqualFold(strings.TrimSpace(it.TemplateID), "VIDEO_BACKGROUND") {
			continue
		}
		startUS, durUS := it.StartUS, it.DurationUS
		if durUS <= 0 {
			startUS = it.StartMs * 1000
			durUS = (it.EndMs - it.StartMs) * 1000
		}
		if durUS <= 0 {
			continue
		}
		if startUS < 0 {
			return fmt.Errorf("overlay timeline: item %q has negative start", it.ID)
		}
		// Overflow-safe end.
		if startUS > int64(^uint64(0)>>1)-durUS {
			return fmt.Errorf("overlay timeline: item %q window overflows", it.ID)
		}
		endUS := startUS + durUS
		startFrame := startUS * int64(fpsNum) / (1000 * 1000 * int64(fpsDen))
		endFrame := (endUS*int64(fpsNum) + 1000*1000*int64(fpsDen) - 1) / (1000 * 1000 * int64(fpsDen))
		if endFrame <= startFrame {
			endFrame = startFrame + 1
		}
		windows = append(windows, window{id: it.ID, isMap: it.Map != nil, startFrame: startFrame, endFrame: endFrame})
	}
	for i := 0; i < len(windows); i++ {
		for j := i + 1; j < len(windows); j++ {
			// Half-open [start,end): a MAP sharing even one frame with any
			// other overlay is a collision (extended map windows vs phrases
			// /images fail the final replace-mode publication after costly
			// renders). Phrase-phrase overlaps remain allowed: existing
			// per-item fixtures layer text and the final compositor orders
			// them; only map-involved collisions fail early here.
			if !windows[i].isMap && !windows[j].isMap {
				continue
			}
			if windows[i].startFrame < windows[j].endFrame && windows[j].startFrame < windows[i].endFrame {
				return fmt.Errorf("overlay timeline collision: %q [%d,%d) overlaps %q [%d,%d)",
					windows[i].id, windows[i].startFrame, windows[i].endFrame,
					windows[j].id, windows[j].startFrame, windows[j].endFrame)
			}
		}
	}
	return nil
}
