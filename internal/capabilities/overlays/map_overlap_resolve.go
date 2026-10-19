package overlays

import (
	"sort"
	"strings"
)

// ResolveMapOverlaps removes map-involved timeline collisions before sealing:
// the final compositor runs maps in replace mode, so a map sharing even one
// frame with any other overlay fails admission after costly renders
// (AdmitOverlayTimeline). Maps win their window — a map is the scene-defining
// ambient visual — and every other overlapping item yields:
//
//   - shiftable items (phrases, images, quotes, numbers, recaps, charts) move
//     to start right after the overlapping map's end frame, clamped to their
//     scene window; an item with no room left is dropped (fail-soft: the run
//     continues with one fewer card, never a failed job);
//   - anchored items are never shifted: editorial scene_title/scene_bullet
//     cards carry certified word timing, and chain items carry per-segment
//     timing, so moving them would silently break their grounding contract —
//     they are dropped when a map covers them instead;
//   - BACKGROUND/VIDEO_BACKGROUND primitives are not replace-mode overlays
//     and never collide (same exemption as the admission gate).
//
// Only forward moves: a card may appear after its spoken moment, never before
// it. Deterministic: same items → same items. An already collision-free plan
// is returned unchanged.
func ResolveMapOverlaps(items []OverlayItem, sceneWindows map[string][2]int64, fpsNum, fpsDen int) []OverlayItem {
	if len(items) < 2 {
		return items
	}
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 24, 1
	}
	unit := int64(1000 * 1000 * fpsDen)
	toFrames := func(startUS, durUS int64) (int64, int64) {
		startFrame := startUS * int64(fpsNum) / unit
		endFrame := ((startUS+durUS)*int64(fpsNum) + unit - 1) / unit
		if endFrame <= startFrame {
			endFrame = startFrame + 1
		}
		return startFrame, endFrame
	}
	mapIdx := make([]int, 0, len(items))
	for i := range items {
		if items[i].Map != nil && !isBackgroundItem(items[i]) {
			mapIdx = append(mapIdx, i)
		}
	}
	if len(mapIdx) == 0 {
		return items
	}
	sort.SliceStable(mapIdx, func(a, b int) bool {
		sa, _ := itemWindowUS(items[mapIdx[a]])
		sb, _ := itemWindowUS(items[mapIdx[b]])
		if sa != sb {
			return sa < sb
		}
		return items[mapIdx[a]].ID < items[mapIdx[b]].ID
	})
	// mapKept tracks kept-slice indices of map items: mapIdx addresses the
	// input slice, but shifting decisions must read the kept slice (which
	// grows as items resolve).
	kept := make([]OverlayItem, 0, len(items))
	mapKept := make([]int, 0, len(mapIdx))
	for _, it := range items {
		if it.Map != nil || isBackgroundItem(it) {
			if it.Map != nil && !isBackgroundItem(it) {
				mapKept = append(mapKept, len(kept))
			}
			kept = append(kept, it)
			continue
		}
		resolved, ok := shiftPastMaps(it, kept, mapKept, sceneWindows, toFrames, fpsNum, fpsDen)
		if !ok {
			continue // no room after the map: drop the card, keep the run
		}
		kept = append(kept, resolved)
	}
	if len(kept) == len(items) {
		unchanged := true
		for i := range kept {
			if kept[i].ID != items[i].ID || kept[i].StartUS != items[i].StartUS ||
				kept[i].DurationUS != items[i].DurationUS {
				unchanged = false
				break
			}
		}
		if unchanged {
			return items
		}
	}
	return kept
}

// shiftPastMaps moves it after every overlapping map (bounded passes: each
// pass clears at least the earliest overlap, and passes never exceed the map
// count plus one). Anchored and chain items are dropped instead of shifted.
func shiftPastMaps(it OverlayItem, kept []OverlayItem, mapKept []int, sceneWindows map[string][2]int64, toFrames func(int64, int64) (int64, int64), fpsNum, fpsDen int) (OverlayItem, bool) {
	if isAnchoredItem(it) || IsChainItemKind(it.Kind) {
		for _, m := range mapKept {
			if framesOverlap(it, kept[m], toFrames) {
				return OverlayItem{}, false
			}
		}
		return it, true
	}
	current := it
	for pass := 0; pass <= len(mapKept); pass++ {
		latestEnd := int64(-1)
		for _, m := range mapKept {
			if framesOverlap(current, kept[m], toFrames) {
				_, mend := toFrames(kept[m].StartUS, kept[m].DurationUS)
				if mend > latestEnd {
					latestEnd = mend
				}
			}
		}
		if latestEnd < 0 {
			return current, true
		}
		unit := int64(1000 * 1000 * fpsDen)
		startUS := latestEnd * unit / int64(fpsNum)
		durUS := current.DurationUS
		if window, ok := sceneWindows[strings.TrimSpace(current.SceneID)]; ok {
			if startUS >= window[1] {
				return OverlayItem{}, false
			}
			if startUS+durUS > window[1] {
				durUS = window[1] - startUS
			}
		} else if strings.TrimSpace(current.SceneID) != "" {
			// A scene-bound card without a known scene window cannot prove
			// it still fits: fail-soft drops it instead of guessing.
			return OverlayItem{}, false
		}
		if durUS <= 0 {
			return OverlayItem{}, false
		}
		current.StartUS = startUS
		current.DurationUS = durUS
		current.StartMs = startUS / 1000
		current.EndMs = (startUS + durUS + 999) / 1000
	}
	return OverlayItem{}, false
}

func itemWindowUS(it OverlayItem) (int64, int64) {
	startUS, durUS := it.StartUS, it.DurationUS
	if durUS <= 0 {
		startUS = it.StartMs * 1000
		durUS = (it.EndMs - it.StartMs) * 1000
	}
	return startUS, durUS
}

func framesOverlap(a, b OverlayItem, toFrames func(int64, int64) (int64, int64)) bool {
	astart, adur := itemWindowUS(a)
	bstart, bdur := itemWindowUS(b)
	if adur <= 0 || bdur <= 0 {
		return false
	}
	as, ae := toFrames(astart, adur)
	bs, be := toFrames(bstart, bdur)
	return as < be && bs < ae
}

func isBackgroundItem(it OverlayItem) bool {
	template := strings.ToLower(strings.TrimSpace(it.TemplateID))
	return template == "background" || template == "video_background"
}

// isAnchoredItem reports cards that must never shift: editorial scene
// titles/bullets against word-level TTS timing (shifting breaks the proof),
// and chapter cards against their chapter opening (shifting breaks the
// chapter-link contract). All of them yield by absence instead.
func isAnchoredItem(it OverlayItem) bool {
	if it.Params == nil {
		return false
	}
	role, _ := it.Params["editorial_role"].(string)
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "scene_title", "scene_bullet", ChapterBulletRole:
		return true
	}
	return false
}
