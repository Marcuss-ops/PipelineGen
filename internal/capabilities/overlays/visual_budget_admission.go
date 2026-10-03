package overlays

import (
	"sort"
	"strings"
)

func rankedUniqueOverlayIndices(items []OverlayItem, images bool, phraseLimit int, perSceneEntityImages ...bool) []int {
	indices := make([]int, 0)
	seen := make(map[string]int)
	for i, item := range items {
		key := ""
		if images {
			if item.Kind != "entity_image" && item.Kind != "image" {
				continue
			}
			key = imageOverlayIdentity(item)
			// Context images belong to scenes. The same content-addressed hit
			// returned by two independent scene searches must not silently erase
			// one of those scene occurrences from a per-scene render plan.
			if item.Kind == "image" && strings.TrimSpace(item.SceneID) != "" {
				key = strings.TrimSpace(item.SceneID) + ":" + key
			}
			if item.Kind == string(KindEntityImage) && len(perSceneEntityImages) > 0 && perSceneEntityImages[0] && strings.TrimSpace(item.SceneID) != "" {
				key = strings.TrimSpace(item.SceneID) + ":" + key
			}
		} else {
			if item.Kind != "text_phrase" {
				continue
			}
			key = strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		}
		if key == "" {
			continue
		}
		if current, ok := seen[key]; ok {
			if overlayItemPriority(item) > overlayItemPriority(items[current]) {
				seen[key] = i
			}
			continue
		}
		seen[key] = i
	}
	for _, index := range seen {
		indices = append(indices, index)
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := indices[i], indices[j]
		if !images {
			leftLong := len(strings.Fields(items[left].Text)) >= 8
			rightLong := len(strings.Fields(items[right].Text)) >= 8
			if leftLong != rightLong {
				return leftLong
			}
		}
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	limit := phraseLimit
	if images {
		limit = MaxImageOverlaysPerRun
	} else {
		// Reserve roughly half of the phrase budget for complete headline
		// candidates (8+ words) when they exist. Remaining slots are filled
		// from the same deterministic priority ranking, so short phrases still
		// fill the budget when long grounded phrases are unavailable.
		longLimit := (limit + 1) / 2
		long := make([]int, 0, len(indices))
		short := make([]int, 0, len(indices))
		for _, index := range indices {
			if len(strings.Fields(items[index].Text)) >= 8 {
				long = append(long, index)
			} else {
				short = append(short, index)
			}
		}
		if len(long) >= longLimit {
			// The reserved slice is a NEW backing array on purpose. Writing
			// into long[:longLimit]'s spare capacity (the natural
			// append(long[:longLimit], short...)) overwrites long[longLimit:],
			// which the very next statement still reads: the long phrases that
			// did not fit the reservation were clobbered before being
			// re-appended, so which candidates were admitted depended on
			// slice capacity rather than on the ranking.
			reserved := make([]int, 0, len(long)+len(short))
			reserved = append(reserved, long[:longLimit]...)
			reserved = append(reserved, short...)
			// Keep the full ranked reserve pool until overlap screening. A short
			// phrase can be rejected because a selected long phrase already
			// covers its timing; the next long candidate must remain available to
			// fill that freed slot.
			reserved = append(reserved, long[longLimit:]...)
			indices = reserved
		} else {
			indices = append(long, short...)
		}
		// Spend the run-level budget across scenes before taking a second
		// phrase from any one scene. Keep the existing long-phrase/priority
		// ranking within each pass, and leave the full reserve pool available
		// for overlap screening to backfill rejected candidates.
		if phraseLimit > 0 && len(indices) > phraseLimit {
			seenScenes := make(map[string]struct{}, phraseLimit)
			diverse := make([]int, 0, len(indices))
			remaining := make([]int, 0, len(indices))
			for _, index := range indices {
				sceneID := strings.TrimSpace(items[index].SceneID)
				if sceneID == "" {
					remaining = append(remaining, index)
					continue
				}
				if _, ok := seenScenes[sceneID]; ok {
					remaining = append(remaining, index)
					continue
				}
				seenScenes[sceneID] = struct{}{}
				diverse = append(diverse, index)
			}
			indices = append(diverse, remaining...)
		}
	}
	// The remote final-job lane accepts replace overlays only, so two phrase
	// cards cannot occupy intersecting frame ranges. Keep the editor's ranking
	// (long phrases first, then priority), but skip an overlapping phrase and
	// continue down the ranked candidate pool to fill the run-level budget.
	// Compare only within a scene: scene-local speech timings are authoritative
	// and one scene must never evict a phrase from another scene.
	if !images && len(indices) > 1 {
		selected := make([]int, 0, len(indices))
		for _, candidate := range indices {
			overlaps := false
			for _, prior := range selected {
				if items[candidate].SceneID == items[prior].SceneID && overlayWindowsOverlap(items[candidate], items[prior]) {
					overlaps = true
					break
				}
			}
			if !overlaps {
				selected = append(selected, candidate)
			}
		}
		indices = selected
	}
	if len(indices) > limit {
		indices = indices[:limit]
	}
	return indices
}

func overlayWindowsOverlap(a, b OverlayItem) bool {
	aStart, aEnd := overlayItemWindowUS(a)
	bStart, bEnd := overlayItemWindowUS(b)
	return aStart < bEnd && bStart < aEnd
}

func overlayItemWindowUS(item OverlayItem) (int64, int64) {
	if item.DurationUS > 0 {
		return item.StartUS, item.StartUS + item.DurationUS
	}
	return item.StartMs * 1_000, item.EndMs * 1_000
}

func imageOverlayIdentity(item OverlayItem) string {
	if item.EntityRef != nil {
		if id := strings.TrimSpace(item.EntityRef.CanonicalEntityID); id != "" {
			return "entity:" + id
		}
		if id := strings.TrimSpace(item.EntityRef.EntityID); id != "" {
			return "entity:" + id
		}
	}
	for _, ref := range item.AssetRefs {
		if hash := strings.TrimSpace(ref.SHA256); hash != "" {
			return "sha256:" + strings.ToLower(hash)
		}
		if id := strings.TrimSpace(ref.AssetID); id != "" {
			return "asset:" + id
		}
	}
	return strings.TrimSpace(item.ID)
}

// dedupeImageIndicesByContent keeps one image overlay per content identity for
// the whole run. rankedUniqueOverlayIndices scopes its image dedup to what
// each producing arm knows — entity identity for entity cards, scene+sha for
// context hits — so the SAME downloaded bytes entering through two arms (an
// entity portrait that also answered a later scene's query) survived twice
// and rendered again. The content key here is the strongest available:
// sha256 first (bytes, never wrong), then asset id, and only then the
// arm-local identity so a keyless item cannot erase its peers. Highest-ranked
// (earliest in imageIndices) wins; the function never reorders survivors.
func dedupeImageIndicesByContent(items []OverlayItem, imageIndices []int, allowRepeatedEntityImagesPerScene ...bool) []int {
	if len(imageIndices) <= 1 {
		return imageIndices
	}
	seen := make(map[string]struct{}, len(imageIndices))
	entityScenes := make(map[string]map[string]struct{}, len(imageIndices))
	out := make([]int, 0, len(imageIndices))
	for _, index := range imageIndices {
		item := items[index]
		keys := make([]string, 0, 3)
		for _, ref := range item.AssetRefs {
			if hash := strings.ToLower(strings.TrimSpace(ref.SHA256)); hash != "" {
				keys = append(keys, "sha256:"+hash)
			}
			if id := strings.TrimSpace(ref.AssetID); id != "" {
				keys = append(keys, "asset:"+id)
			}
		}
		if len(keys) == 0 {
			if identity := strings.TrimSpace(imageOverlayIdentity(item)); identity != "" {
				keys = append(keys, identity)
			}
		}
		if len(keys) == 0 {
			// A completely unidentifiable image cannot collide with anything;
			// keep it so the budget still admits countable content.
			out = append(out, index)
			continue
		}
		duplicate := false
		for _, key := range keys {
			if _, exists := seen[key]; exists {
				if len(allowRepeatedEntityImagesPerScene) > 0 && allowRepeatedEntityImagesPerScene[0] && item.Kind == string(KindEntityImage) && strings.TrimSpace(item.SceneID) != "" {
					if entityScenes[key] == nil {
						entityScenes[key] = make(map[string]struct{})
					}
					if _, sameScene := entityScenes[key][strings.TrimSpace(item.SceneID)]; !sameScene {
						continue
					}
				}
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		for _, key := range keys {
			seen[key] = struct{}{}
			if item.Kind == string(KindEntityImage) && strings.TrimSpace(item.SceneID) != "" {
				if entityScenes[key] == nil {
					entityScenes[key] = make(map[string]struct{})
				}
				entityScenes[key][strings.TrimSpace(item.SceneID)] = struct{}{}
			}
		}
		out = append(out, index)
	}
	return out
}

// VisualBudget caps how many visual overlays a scene may carry. A cap of 0
// means "unlimited" (no cap for that dimension); a positive cap is enforced.
type VisualBudget struct {
	SceneID string `json:"scene_id"`
	// MaxEntityImages caps entity-bound image cards (ENTITY_IMAGE kind).
	MaxEntityImages int `json:"max_entity_images"`
	// MaxTextCallouts caps text callouts (every non-image, non-number kind).
	MaxTextCallouts int `json:"max_text_callouts"`
	// MaxNumberCards caps number/stat cards (IMPORTANT_NUMBER kind).
	MaxNumberCards int `json:"max_number_cards"`
	// MaxOverlaysTotal caps the total number of overlays regardless of kind.
	MaxOverlaysTotal int `json:"max_overlays_total"`
}

// DefaultVisualBudget returns the canonical per-scene budget: at most 4 entity
// images, 2 text callouts, 1 number card and 7 overlays in total.
