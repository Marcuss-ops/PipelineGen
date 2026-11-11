package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
	AssetRef *capmodel.OverlayAssetRef
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
func WireMultiEntityOverlays(group MultiEntityGroup, canvasWidth, canvasHeight int) ([]capmodel.OverlayItem, error) {
	if strings.TrimSpace(group.GroupID) == "" || strings.TrimSpace(group.SceneID) == "" || group.StartMS < 0 || group.EndMS <= group.StartMS || len(group.Items) == 0 || len(group.Items) > 5 || canvasWidth <= 0 || canvasHeight <= 0 {
		return nil, fmt.Errorf("multi-entity wiring: invalid group identity, timing, or contents")
	}
	seenAssets := make(map[string]struct{}, len(group.Items))
	seenIDs := make(map[string]struct{}, len(group.Items))
	var out []capmodel.OverlayItem
	for index, candidate := range group.Items {
		if candidate.StartMS < group.StartMS || candidate.EndMS > group.EndMS || candidate.EndMS <= candidate.StartMS {
			return nil, fmt.Errorf("multi-entity wiring: candidate %q falls outside group window", candidate.ID)
		}
		if candidate.SceneID != "" && candidate.SceneID != group.SceneID {
			return nil, fmt.Errorf("multi-entity wiring: candidate %q belongs to scene %q, not %q", candidate.ID, candidate.SceneID, group.SceneID)
		}
		switch candidate.Kind {
		case MultiEntityKindImage:
			if candidate.AssetRef == nil || strings.TrimSpace(candidate.AssetRef.AssetID) == "" || strings.TrimSpace(candidate.AssetRef.SHA256) == "" {
				return nil, fmt.Errorf("multi-entity wiring: image candidate %q requires a content-addressed asset", candidate.ID)
			}
			if _, duplicate := seenAssets[candidate.AssetRef.AssetID]; duplicate {
				return nil, fmt.Errorf("multi-entity wiring: duplicate image asset %q", candidate.AssetRef.AssetID)
			}
			seenAssets[candidate.AssetRef.AssetID] = struct{}{}
			imageID := strings.TrimSpace(candidate.ID)
			if imageID == "" {
				imageID = fmt.Sprintf("%s-image-%d", group.GroupID, index+1)
			}
			if _, duplicate := seenIDs[imageID]; duplicate {
				return nil, fmt.Errorf("multi-entity wiring: duplicate image candidate id %q", imageID)
			}
			seenIDs[imageID] = struct{}{}
			ref := *candidate.AssetRef
			motionOffset := ImageMotionOffsetForSeed(group.GroupID + "\x00" + group.SceneID)
			out = append(out, capmodel.OverlayItem{
				ID: imageID, SceneID: group.SceneID, GroupID: group.GroupID,
				Kind: string(capmodel.KindEntityImage), TemplateID: "IMAGE_OVERLAY",
				PresetID:        selectImagePreset(group.GroupID, group.SceneID, imageID),
				MotionID:        EntityImageMotionAtOffset(motionOffset, index),
				CaptionMotionID: RuntimeEntityCaptionMotionAtOffset(motionOffset, index),
				EntityCaption:   strings.TrimSpace(candidate.Name),
				StartMs:         candidate.StartMS, EndMs: candidate.EndMS,
				StartUS: candidate.StartMS * 1000, DurationUS: (candidate.EndMS - candidate.StartMS) * 1000,
				AssetRefs: []capmodel.OverlayAssetRef{ref},
			})
		case MultiEntityKindPhrase:
			if strings.TrimSpace(candidate.Text) == "" {
				return nil, fmt.Errorf("multi-entity wiring: phrase candidate %q requires text", candidate.ID)
			}
			phraseID := strings.TrimSpace(candidate.ID)
			if phraseID == "" {
				phraseID = fmt.Sprintf("%s-phrase-%d", group.GroupID, index+1)
			}
			item := capmodel.OverlayItem{
				ID: phraseID, SceneID: group.SceneID, GroupID: group.GroupID, SubgroupID: phraseID,
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
		default:
			return nil, fmt.Errorf("multi-entity wiring: unsupported candidate kind %q", candidate.Kind)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
