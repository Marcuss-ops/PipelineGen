package overlays

import (
	"fmt"
	"sort"
)

// MultiEntityKind classifies the elements within an EntityGroup.
type MultiEntityKind string

const (
	MultiEntityKindImage  MultiEntityKind = "image"
	MultiEntityKindPhrase MultiEntityKind = "phrase"
	MultiEntityKindMixed  MultiEntityKind = "mixed"
)

var (
	MultiImageDuoMotions = []string{
		"duo_split_reveal",
		"duo_depth_stagger",
		"duo_cross_focus",
		"duo_parallax_balance",
		"duo_compare_hold",
	}
	MultiImageTrioMotions = []string{
		"trio_fan_reveal",
		"trio_center_priority",
		"trio_ladder_stagger",
		"trio_arc_focus",
		"trio_depth_peel",
	}
	MultiImageQuadMotions = []string{
		"quad_grid_assemble",
		"quad_corner_converge",
		"quad_pair_focus",
		"quad_mosaic_spotlight",
		"quad_crossflow",
	}
	MultiImagePentaMotions = []string{
		"penta_hero_plus_four",
		"penta_carousel_focus",
		"penta_cluster_expand",
		"penta_strip_wave",
		"penta_priority_cycle",
	}
	MultiPhraseMotions = []string{
		"phrase_vertical_focus_stack",
		"phrase_ladder_priority",
		"phrase_dual_side_compare",
		"phrase_stagger_keep_alive",
		"phrase_center_with_history",
	}
)

// MultiEntityCandidate is a prospective item (image or phrase) eligible for multi-entity grouping.
type MultiEntityCandidate struct {
	ID       string
	SceneID  string
	Kind     MultiEntityKind
	Name     string
	Text     string
	AssetRef *OverlayAssetRef
	StartMS  int64
	EndMS    int64
	Priority int
}

// MultiEntityGroup represents a cluster of 2..5 entities to be presented simultaneously.
type MultiEntityGroup struct {
	GroupID       string
	SceneID       string
	GroupType     MultiEntityKind
	Count         int
	Items         []MultiEntityCandidate
	StartMS       int64
	EndMS         int64
	MotionPreset  string
	FocusStrategy string
}

// GroupNearbyEntities clusters candidates within a temporal window (default <= 1800ms)
// or co-occurring within the same scene segment, avoiding fragmented sequential renders.
func GroupNearbyEntities(candidates []MultiEntityCandidate, maxGapMS int64) []MultiEntityGroup {
	if len(candidates) == 0 {
		return nil
	}
	if maxGapMS <= 0 {
		maxGapMS = 1800 // 1.8s temporal window for Vidrush-style multi-entity binding
	}

	// Sort chronologically by StartMS
	sorted := make([]MultiEntityCandidate, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartMS == sorted[j].StartMS {
			return sorted[i].Priority > sorted[j].Priority
		}
		return sorted[i].StartMS < sorted[j].StartMS
	})

	var groups []MultiEntityGroup
	var current []MultiEntityCandidate

	flush := func() {
		if len(current) == 0 {
			return
		}
		g := createGroupFromCluster(current)
		groups = append(groups, g)
		current = nil
	}

	for _, cand := range sorted {
		if len(current) == 0 {
			current = append(current, cand)
			continue
		}

		last := current[len(current)-1]
		gap := cand.StartMS - last.EndMS
		// Overlapping or closely following (gap <= maxGapMS) and same scene (if specified)
		sameScene := last.SceneID == "" || cand.SceneID == "" || last.SceneID == cand.SceneID
		canGroup := gap <= maxGapMS && sameScene && len(current) < 5

		if canGroup {
			current = append(current, cand)
		} else {
			flush()
			current = append(current, cand)
		}
	}
	flush()

	return groups
}

func createGroupFromCluster(items []MultiEntityCandidate) MultiEntityGroup {
	earliestStart := items[0].StartMS
	latestEnd := items[0].EndMS
	hasImage := false
	hasPhrase := false

	for _, it := range items {
		if it.StartMS < earliestStart {
			earliestStart = it.StartMS
		}
		if it.EndMS > latestEnd {
			latestEnd = it.EndMS
		}
		if it.Kind == MultiEntityKindImage {
			hasImage = true
		} else if it.Kind == MultiEntityKindPhrase {
			hasPhrase = true
		}
	}

	// Guarantee at least 5.0s (5000ms) presentation duration for cinematic readability
	if latestEnd-earliestStart < 5000 {
		latestEnd = earliestStart + 5000
	}

	kind := MultiEntityKindImage
	if hasImage && hasPhrase {
		kind = MultiEntityKindMixed
	} else if hasPhrase {
		kind = MultiEntityKindPhrase
	}

	count := len(items)
	motion := SelectMultiEntityMotion(kind, count, 0)

	focusStrategy := "sequential_highlight"
	if count == 2 {
		focusStrategy = "bilateral_exchange"
	} else if count >= 3 {
		focusStrategy = "stagger_and_hold"
	}

	return MultiEntityGroup{
		GroupID:       fmt.Sprintf("group_%s_%d_%d", kind, count, earliestStart),
		SceneID:       items[0].SceneID,
		GroupType:     kind,
		Count:         count,
		Items:         items,
		StartMS:       earliestStart,
		EndMS:         latestEnd,
		MotionPreset:  motion,
		FocusStrategy: focusStrategy,
	}
}

// SelectMultiEntityMotion deterministically resolves a certified preset for a given entity count and kind.
func SelectMultiEntityMotion(kind MultiEntityKind, count int, seed int) string {
	switch kind {
	case MultiEntityKindPhrase:
		if count == 2 {
			return "phrase_dual_side_compare"
		}
		idx := seed % len(MultiPhraseMotions)
		if idx < 0 {
			idx = -idx
		}
		return MultiPhraseMotions[idx]

	case MultiEntityKindImage, MultiEntityKindMixed:
		switch count {
		case 2:
			return MultiImageDuoMotions[seed%len(MultiImageDuoMotions)]
		case 3:
			return MultiImageTrioMotions[seed%len(MultiImageTrioMotions)]
		case 4:
			return MultiImageQuadMotions[seed%len(MultiImageQuadMotions)]
		case 5:
			return MultiImagePentaMotions[seed%len(MultiImagePentaMotions)]
		default:
			if count > 5 {
				return MultiImagePentaMotions[seed%len(MultiImagePentaMotions)]
			}
			return MultiImageDuoMotions[0]
		}
	}
	return "duo_split_reveal"
}

// WireMultiEntityOverlay converts an EntityGroup into a unified OverlayItem for the Chronon pipeline.
func WireMultiEntityOverlay(group MultiEntityGroup) OverlayItem {
	templateID := "MULTI_ENTITY"
	presetID := "multi_entity_layout_v1"
	if group.GroupType == MultiEntityKindPhrase {
		templateID = "MULTI_PHRASE"
		presetID = "multi_phrase_layout_v1"
	}

	var assetRefs []OverlayAssetRef
	var imageLayers []OverlayImageLayer
	var texts []string

	for idx, it := range group.Items {
		if it.AssetRef != nil {
			assetRefs = append(assetRefs, *it.AssetRef)
			relStart := it.StartMS - group.StartMS
			if relStart < 0 {
				relStart = 0
			}
			relEnd := it.EndMS - group.StartMS
			if relEnd > (group.EndMS - group.StartMS) {
				relEnd = group.EndMS - group.StartMS
			}

			imageLayers = append(imageLayers, OverlayImageLayer{
				ID:       fmt.Sprintf("layer_%d", idx+1),
				AssetID:  it.AssetRef.AssetID,
				StartMS:  relStart,
				EndMS:    relEnd,
				PresetID: presetID,
				MotionID: group.MotionPreset,
			})
		}
		if it.Text != "" {
			texts = append(texts, it.Text)
		}
	}

	combinedText := ""
	if len(texts) > 0 {
		for i, t := range texts {
			if i > 0 {
				combinedText += "\n"
			}
			combinedText += t
		}
	}

	return OverlayItem{
		ID:          group.GroupID,
		SceneID:     group.SceneID,
		Kind:        "multi_entity_card",
		StartMs:     group.StartMS,
		EndMs:       group.EndMS,
		StartUS:     group.StartMS * 1000,
		DurationUS:  (group.EndMS - group.StartMS) * 1000,
		TemplateID:  templateID,
		PresetID:    presetID,
		MotionID:    group.MotionPreset,
		Text:        combinedText,
		AssetRefs:   assetRefs,
		ImageLayers: imageLayers,
		Params: map[string]any{
			"entity_count":   group.Count,
			"group_type":     string(group.GroupType),
			"focus_strategy": group.FocusStrategy,
		},
	}
}

// IsMultiEntityMotion returns true if the motion ID belongs to any certified multi-entity family.
func IsMultiEntityMotion(motionID string) bool {
	for _, m := range MultiImageDuoMotions {
		if m == motionID {
			return true
		}
	}
	for _, m := range MultiImageTrioMotions {
		if m == motionID {
			return true
		}
	}
	for _, m := range MultiImageQuadMotions {
		if m == motionID {
			return true
		}
	}
	for _, m := range MultiImagePentaMotions {
		if m == motionID {
			return true
		}
	}
	for _, m := range MultiPhraseMotions {
		if m == motionID {
			return true
		}
	}
	return false
}
