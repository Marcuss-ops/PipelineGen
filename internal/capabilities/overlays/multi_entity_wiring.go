package overlays

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MultiEntityKind classifies the elements within a temporally grouped scene.
type MultiEntityKind string

const (
	MultiEntityKindImage  MultiEntityKind = "image"
	MultiEntityKindPhrase MultiEntityKind = "phrase"
	MultiEntityKindMixed  MultiEntityKind = "mixed"
)

// MultiEntityCandidate is one grounded image or phrase that may share a
// presentation window with nearby candidates.
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

// MultiEntityGroup is an editorial grouping, not a renderer animation. Each
// child image receives its own normal image motion; phrase content stays on
// the existing important_phrase primitive.
type MultiEntityGroup struct {
	GroupID       string
	SceneID       string
	GroupType     MultiEntityKind
	Count         int
	Items         []MultiEntityCandidate
	StartMS       int64
	EndMS         int64
	FocusStrategy string
}

// GroupNearbyEntities clusters valid same-scene candidates within maxGapMS,
// preserving chronological order and the existing maximum of five per group.
func GroupNearbyEntities(candidates []MultiEntityCandidate, maxGapMS int64) []MultiEntityGroup {
	valid := make([]MultiEntityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.StartMS < 0 || candidate.EndMS <= candidate.StartMS {
			continue
		}
		if candidate.Kind == MultiEntityKindImage && (candidate.AssetRef == nil || strings.TrimSpace(candidate.AssetRef.AssetID) == "") {
			continue
		}
		if candidate.Kind == MultiEntityKindPhrase && strings.TrimSpace(candidate.Text) == "" {
			continue
		}
		if candidate.Kind != MultiEntityKindImage && candidate.Kind != MultiEntityKindPhrase {
			continue
		}
		valid = append(valid, candidate)
	}
	if len(valid) == 0 {
		return nil
	}
	if maxGapMS <= 0 {
		maxGapMS = 1800
	}
	// Stable chronology preserves the caller's order for equal-start candidates.
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].StartMS < valid[j].StartMS })
	var groups []MultiEntityGroup
	current := make([]MultiEntityCandidate, 0, 5)
	flush := func() {
		if len(current) == 0 {
			return
		}
		groups = append(groups, createGroupFromCluster(current))
		current = nil
	}
	for _, candidate := range valid {
		if len(current) == 0 {
			current = append(current, candidate)
			continue
		}
		previous := current[len(current)-1]
		sameScene := candidate.SceneID == ""
		if candidate.SceneID != "" {
			sameScene = true
			for _, member := range current {
				if member.SceneID != "" && member.SceneID != candidate.SceneID {
					sameScene = false
					break
				}
			}
		}
		imageStackWithinWindow := candidate.Kind != MultiEntityKindImage || imageStackFitsWindow(current, candidate)
		if sameScene && imageStackWithinWindow && candidate.StartMS-previous.EndMS <= maxGapMS && len(current) < 5 {
			current = append(current, candidate)
			continue
		}
		flush()
		current = append(current, candidate)
	}
	flush()
	return groups
}

func imageStackFitsWindow(current []MultiEntityCandidate, candidate MultiEntityCandidate) bool {
	start, end := candidate.StartMS, min(candidate.EndMS, candidate.StartMS+MaxImageOverlayDurationMS)
	for _, item := range current {
		if item.Kind != MultiEntityKindImage {
			continue
		}
		itemEnd := min(item.EndMS, item.StartMS+MaxImageOverlayDurationMS)
		if item.StartMS < start {
			start = item.StartMS
		}
		if itemEnd > end {
			end = itemEnd
		}
	}
	return end-start <= MaxImageOverlayDurationMS
}

func createGroupFromCluster(items []MultiEntityCandidate) MultiEntityGroup {
	start, end := items[0].StartMS, items[0].EndMS
	hasImage, hasPhrase := false, false
	for _, item := range items {
		if item.StartMS < start {
			start = item.StartMS
		}
		if item.EndMS > end {
			end = item.EndMS
		}
		hasImage = hasImage || item.Kind == MultiEntityKindImage
		hasPhrase = hasPhrase || item.Kind == MultiEntityKindPhrase
	}
	if end-start < 5000 {
		end = start + 5000
	}
	kind := MultiEntityKindImage
	if hasImage && hasPhrase {
		kind = MultiEntityKindMixed
	} else if hasPhrase {
		kind = MultiEntityKindPhrase
	}
	focus := "sequential_highlight"
	if len(items) == 2 {
		focus = "bilateral_exchange"
	} else if len(items) >= 3 {
		focus = "stagger_and_hold"
	}
	sceneID := ""
	for _, item := range items {
		if item.SceneID != "" {
			sceneID = item.SceneID
			break
		}
	}
	return MultiEntityGroup{
		GroupID: fmt.Sprintf("group_%s_%d_%d", kind, len(items), start),
		SceneID: sceneID, GroupType: kind, Count: len(items),
		Items: append([]MultiEntityCandidate(nil), items...), StartMS: start, EndMS: end,
		FocusStrategy: focus,
	}
}

// WireMultiEntityOverlays lowers a group to the existing semantic primitives.
// Image children use image_layers with independent certified motions. Phrase
// children remain separate IMPORTANT_PHRASE items to preserve their own timing.
// Mixed groups produce both types rather than inventing a composite renderer.
func WireMultiEntityOverlays(group MultiEntityGroup, canvasWidth, canvasHeight int) ([]OverlayItem, error) {
	if strings.TrimSpace(group.GroupID) == "" || strings.TrimSpace(group.SceneID) == "" || group.StartMS < 0 || group.EndMS <= group.StartMS || len(group.Items) == 0 || len(group.Items) > 5 || canvasWidth <= 0 || canvasHeight <= 0 {
		return nil, fmt.Errorf("multi-entity wiring: invalid group identity, timing, or contents")
	}
	assets := make([]OverlayAssetRef, 0, len(group.Items))
	layers := make([]OverlayImageLayer, 0, len(group.Items))
	phrases := make([]MultiEntityCandidate, 0, len(group.Items))
	layerIDs := make(map[string]struct{}, len(group.Items))
	imageStartOffset, imageEndOffset := int64(math.MaxInt64), int64(0)
	for index, candidate := range group.Items {
		if candidate.StartMS < group.StartMS || candidate.EndMS > group.EndMS || candidate.EndMS <= candidate.StartMS {
			return nil, fmt.Errorf("multi-entity wiring: candidate %q falls outside group window", candidate.ID)
		}
		if candidate.SceneID != "" && candidate.SceneID != group.SceneID {
			return nil, fmt.Errorf("multi-entity wiring: candidate %q belongs to scene %q, not %q", candidate.ID, candidate.SceneID, group.SceneID)
		}
		if candidate.Kind == MultiEntityKindImage && candidate.AssetRef != nil {
			for _, ref := range assets {
				if ref.AssetID == candidate.AssetRef.AssetID {
					return nil, fmt.Errorf("multi-entity wiring: duplicate image asset %q", ref.AssetID)
				}
			}
		}
		switch candidate.Kind {
		case MultiEntityKindImage:
			if candidate.AssetRef == nil || strings.TrimSpace(candidate.AssetRef.AssetID) == "" || strings.TrimSpace(candidate.AssetRef.SHA256) == "" {
				return nil, fmt.Errorf("multi-entity wiring: image candidate %q requires a content-addressed asset", candidate.ID)
			}
			endMS := min(candidate.EndMS, candidate.StartMS+MaxImageOverlayDurationMS)
			if endMS <= candidate.StartMS {
				return nil, fmt.Errorf("multi-entity wiring: image candidate %q starts outside the %dms image window", candidate.ID, MaxImageOverlayDurationMS)
			}
			ref := *candidate.AssetRef
			assets = append(assets, ref)
			layerID := strings.TrimSpace(candidate.ID)
			if layerID == "" {
				layerID = fmt.Sprintf("image_%d", index+1)
			}
			if _, duplicate := layerIDs[layerID]; duplicate {
				return nil, fmt.Errorf("multi-entity wiring: duplicate image candidate id %q", layerID)
			}
			layerIDs[layerID] = struct{}{}
			imageIndex := len(layers)
			startOffset, endOffset := candidate.StartMS-group.StartMS, endMS-group.StartMS
			if startOffset < imageStartOffset {
				imageStartOffset = startOffset
			}
			if endOffset > imageEndOffset {
				imageEndOffset = endOffset
			}
			position := multiImagePosition(countKind(group.Items, MultiEntityKindImage), imageIndex, canvasWidth, canvasHeight)
			layers = append(layers, OverlayImageLayer{
				ID: layerID, AssetID: ref.AssetID,
				StartMS: candidate.StartMS - group.StartMS, EndMS: endMS - group.StartMS,
				PresetID:        selectImagePreset(group.GroupID, group.SceneID, layerID),
				MotionID:        EntityImageMotionAtOffset(0, imageIndex),
				Caption:         strings.TrimSpace(candidate.Name),
				CaptionMotionID: EntityCaptionMotionAtOffset(0, imageIndex),
				Params:          map[string]any{"position_x": position[0], "position_y": position[1], "width": position[2], "height": position[3], "fit": "contain"},
			})
		case MultiEntityKindPhrase:
			if strings.TrimSpace(candidate.Text) == "" {
				return nil, fmt.Errorf("multi-entity wiring: phrase candidate %q requires text", candidate.ID)
			}
			phrases = append(phrases, candidate)
		default:
			return nil, fmt.Errorf("multi-entity wiring: unsupported candidate kind %q", candidate.Kind)
		}
	}
	if len(layers) > 0 && imageEndOffset-imageStartOffset > MaxImageOverlayDurationMS {
		return nil, fmt.Errorf("multi-entity wiring: image stack spans %dms, exceeding the %dms image ceiling", imageEndOffset-imageStartOffset, MaxImageOverlayDurationMS)
	}
	if len(layers) > 0 {
		for index := range layers {
			layers[index].StartMS -= imageStartOffset
			layers[index].EndMS -= imageStartOffset
		}
	}
	var out []OverlayItem
	if len(layers) > 0 {
		imageID := group.GroupID + "-images"
		imageWindowStart := group.StartMS + imageStartOffset
		imageWindowEnd := group.StartMS + imageEndOffset
		imageItem := OverlayItem{
			ID: imageID, SceneID: group.SceneID,
			Kind: string(KindEntityImage), TemplateID: "IMAGE_OVERLAY",
			PresetID: selectImagePreset(group.GroupID, group.SceneID, imageID),
			StartMs:  imageWindowStart, EndMs: imageWindowEnd, StartUS: imageWindowStart * 1000, DurationUS: (imageWindowEnd - imageWindowStart) * 1000,
			AssetRefs: assets,
		}
		if len(layers) == 1 {
			imageItem.EntityCaption = layers[0].Caption
			imageItem.CaptionMotionID = layers[0].CaptionMotionID
			imageItem.MotionID = layers[0].MotionID
			imageItem.Params = layers[0].Params
		} else {
			imageItem.ImageLayers = layers
		}
		// Composite entity stacks carry the certified "random" style selector
		// too: RenderingGen samples the full 25 Apple Spatial compositions per
		// (plan, item) identity, so grouped portraits keep the same runtime
		// variety as single cards. Multi-layer stacks keep captions on their
		// child layers (no item-level entity_caption), so they stay unstamped:
		// the worker's style precondition requires the item-level caption.
		if imageItem.EntityCaption != "" {
			imageItem.EntityStyleID = IdentityEntityStyleSelector
		}
		out = append(out, imageItem)
	}
	for index, candidate := range phrases {
		phraseID := fmt.Sprintf("%s-phrase-%d", group.GroupID, index+1)
		item := OverlayItem{
			ID: phraseID, SceneID: group.SceneID,
			Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
			PresetID: selectPhrasePreset(group.GroupID, group.SceneID, phraseID),
			StartMs:  candidate.StartMS, EndMs: candidate.EndMS, StartUS: candidate.StartMS * 1000,
			DurationUS: (candidate.EndMS - candidate.StartMS) * 1000,
			Text:       strings.TrimSpace(candidate.Text),
			Params:     map[string]any{"position": "center", "style": "headline", "priority": candidate.Priority},
		}
		words := len(strings.Fields(item.Text))
		if words > 0 && words < 6 {
			item.MotionID = selectShortPhraseMotion(group.GroupID, group.SceneID, index, words, nil)
		} else {
			item.MotionID = selectLongPhraseMotion(group.GroupID, group.SceneID, index, nil)
		}
		out = append(out, item)
	}
	return out, nil
}

func countKind(items []MultiEntityCandidate, kind MultiEntityKind) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func multiImagePosition(count, index, canvasWidth, canvasHeight int) []float64 {
	width, height := float64(canvasWidth)*0.42, float64(canvasHeight)*0.56
	var x, y float64
	switch count {
	case 1:
		x, y = 0, 0
	case 2:
		x = []float64{-0.25, 0.25}[index]
	case 3:
		width, height = float64(canvasWidth)*0.40, float64(canvasHeight)*0.46
		positions := [][2]float64{{-0.22, -0.22}, {0.22, -0.22}, {0, 0.25}}
		x, y = positions[index][0], positions[index][1]
	case 4:
		positions := [][2]float64{{-0.24, -0.22}, {0.24, -0.22}, {-0.24, 0.22}, {0.24, 0.22}}
		x, y = positions[index][0], positions[index][1]
	case 5:
		width, height = float64(canvasWidth)*0.34, float64(canvasHeight)*0.42
		positions := [][2]float64{{-0.32, -0.25}, {0, -0.25}, {0.32, -0.25}, {-0.18, 0.24}, {0.18, 0.24}}
		x, y = positions[index][0], positions[index][1]
	default:
		columns := int(math.Ceil(math.Sqrt(float64(max(1, count)))))
		row := index / columns
		column := index % columns
		rows := (count + columns - 1) / columns
		x = (float64(column) - float64(columns-1)/2) * float64(canvasWidth) * 0.46
		y = (float64(row) - float64(rows-1)/2) * float64(canvasHeight) * 0.52
		width = float64(canvasWidth) * 0.42 / math.Sqrt(float64(columns))
		height = float64(canvasHeight) * 0.56 / math.Sqrt(float64(rows))
	}
	if count >= 2 && count <= 5 {
		x *= float64(canvasWidth)
		y *= float64(canvasHeight)
	}
	return []float64{x, y, width, height}
}
