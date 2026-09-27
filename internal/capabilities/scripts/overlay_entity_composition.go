package scriptgeneration

import (
	"fmt"
	"sort"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// entityImageMergeGapMS is the largest gap between certified mentions that
// still reads as one short visual beat.
const entityImageMergeGapMS int64 = 3_000

// composeNearbyEntityImages merges pairs of image-backed entity items whose
// spoken anchors are no more than three seconds apart. Each portrait keeps its
// own relative reveal time and receives its own independently selected motion.
// The combined parent window runs from the first anchor through the final
// five-second image hold; it is published as exactly one overlay/video.
func composeNearbyEntityImages(items []capabilityoverlay.OverlayItem, width, height int) []capabilityoverlay.OverlayItem {
	indices := make([]int, 0, len(items))
	for index := range items {
		if items[index].Kind == string(capabilityoverlay.KindEntityImage) && len(items[index].AssetRefs) == 1 {
			indices = append(indices, index)
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := items[indices[i]], items[indices[j]]
		if left.StartMs != right.StartMs {
			return left.StartMs < right.StartMs
		}
		return left.ID < right.ID
	})

	paired := make(map[int]bool, len(indices))
	var out []capabilityoverlay.OverlayItem
	for cursor := 0; cursor+1 < len(indices); {
		firstIndex, secondIndex := indices[cursor], indices[cursor+1]
		first, second := items[firstIndex], items[secondIndex]
		gap := second.StartMs - first.StartMs
		if gap < 0 || gap > entityImageMergeGapMS {
			cursor++
			continue
		}

		parentStart := first.StartMs
		firstEnd := first.EndMs - first.StartMs
		secondOffset := second.StartMs - parentStart
		secondEnd := secondOffset + (second.EndMs - second.StartMs)
		parentEnd := parentStart + max(firstEnd, secondEnd)
		parent := first
		parent.ID = fmt.Sprintf("%s+%s", first.ID, second.ID)
		parent.EndMs = parentEnd
		parent.StartUS = first.StartUSValue()
		parent.DurationUS = parentEnd*1_000 - parent.StartUS
		parent.AssetRefs = append(append([]capabilityoverlay.OverlayAssetRef(nil), first.AssetRefs...), second.AssetRefs...)
		parent.MotionID = ""
		parent.MotionParams = nil
		parent.Params = nil
		parent.RenderKey = ""
		parent.ImageLayers = []capabilityoverlay.OverlayImageLayer{
			{ID: first.ID, AssetID: first.AssetRefs[0].AssetID, StartMS: 0, EndMS: firstEnd,
				PresetID: first.PresetID, Params: entityImageLayerParams(width, height, 0)},
			{ID: second.ID, AssetID: second.AssetRefs[0].AssetID, StartMS: secondOffset, EndMS: secondEnd,
				PresetID: second.PresetID, Params: entityImageLayerParams(width, height, 1)},
		}
		if parent.EntityRef == nil {
			parent.EntityRef = &capabilityoverlay.OverlayEntityRef{}
		}
		parent.EntityRef.CanonicalEntityID = entityRefCanonicalID(first)
		out = append(out, parent)
		paired[firstIndex], paired[secondIndex] = true, true
		cursor += 2
	}

	for index, item := range items {
		if paired[index] {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// assignEntityImageMotions samples a fresh pool offset once per newly compiled
// plan, then rotates through the 18 certified motion IDs. In a composite each
// portrait consumes its own ordinal, so the animations remain independent.
func assignEntityImageMotions(items []capabilityoverlay.OverlayItem, offset, width, height int) {
	ordinal := 0
	for itemIndex := range items {
		item := &items[itemIndex]
		if item.Kind != string(capabilityoverlay.KindEntityImage) {
			continue
		}
		if len(item.ImageLayers) > 0 {
			for layerIndex := range item.ImageLayers {
				item.ImageLayers[layerIndex].MotionID = capabilityoverlay.ImageMotionAtOffset(offset, ordinal)
				ordinal++
			}
			continue
		}
		item.MotionID = capabilityoverlay.ImageMotionAtOffset(offset, ordinal)
		item.Params = capabilityoverlay.EntityImageParams(width, height)
		ordinal++
	}
}

func entityRefCanonicalID(item capabilityoverlay.OverlayItem) string {
	if item.EntityRef == nil {
		return ""
	}
	return item.EntityRef.CanonicalEntityID
}

func entityImageLayerParams(width, height, slot int) map[string]any {
	if width <= 0 || height <= 0 {
		width, height = 1920, 1080
	}
	boxWidth := width * 22 / 100
	boxHeight := height * 42 / 100
	if boxWidth < 1 {
		boxWidth = 1
	}
	if boxHeight < 1 {
		boxHeight = 1
	}
	positionX := -float64(width) * 0.24
	if slot == 1 {
		positionX = float64(width) * 0.24
	}
	return map[string]any{
		"width": boxWidth, "height": boxHeight,
		"position_x": positionX, "position_y": 0,
		"fit": "contain",
	}
}
