// Package scriptgeneration — entity_projection.go owns the deterministic
// projection of the incremental VidRush enrichment results onto the durable
// result's typed entity aggregate (persons / places / concepts), the legacy
// compatibility projection derived from it, the SINGLE derivation of an
// annotation entity's identity, and the entity-image overlay composition
// (pairing nearby portraits into one composite overlay item with per-layer
// certified motions).
//
// The durable runner consumes these helpers after the final barrier so a
// SUCCEEDED run exposes the entities its extraction backend actually produced —
// the same typed buckets the batch flow projects into Artifacts.Entities — and
// so every downstream join (overlay intent, entity-card media index, semantic
// render bundle) resolves an entity's identity through ONE function instead of
// re-deriving it from a display name.
package scriptgeneration

import (
	"fmt"
	"sort"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// aggregateEntityResult merges the fenced per-scene VidRush segment results
// into one canonical EntityResult. Classification is deterministic and matches
// the legacy batch projection: PERSON → persons; LOCATION/PLACE/COUNTRY/CITY →
// places; every other type → concepts. Returns nil when no segment produced
// any entity (or no segments were passed), so the durable surface omits the
// block instead of exposing an empty aggregate.
func aggregateEntityResult(segments []scriptpkg.VidRushSegmentResult) *scriptpkg.EntityResult {
	agg := &scriptpkg.EntityResult{}
	seenPhrases := make(map[string]struct{})
	seenWords := make(map[string]struct{})
	for _, seg := range segments {
		for _, phrase := range seg.Insights.ImportantPhrases {
			phrase = strings.TrimSpace(phrase)
			key := strings.ToLower(phrase)
			if phrase != "" {
				if _, exists := seenPhrases[key]; !exists {
					seenPhrases[key] = struct{}{}
					agg.ImportantPhrases = append(agg.ImportantPhrases, phrase)
				}
			}
		}
		for _, word := range seg.Insights.ImportantWords {
			word = strings.TrimSpace(word)
			key := strings.ToLower(word)
			if word != "" {
				if _, exists := seenWords[key]; !exists {
					seenWords[key] = struct{}{}
					agg.ImportantWords = append(agg.ImportantWords, word)
				}
			}
		}
		for _, ent := range seg.Insights.Entities {
			value := strings.TrimSpace(ent.Value)
			if value == "" {
				continue
			}
			entity := scriptpkg.Entity{Value: value, Type: ent.Type, Score: float32(ent.Confidence)}
			switch strings.ToUpper(strings.TrimSpace(ent.Type)) {
			case "PERSON":
				agg.Persons = append(agg.Persons, entity)
			case "LOCATION", "PLACE", "COUNTRY", "CITY":
				agg.Places = append(agg.Places, entity)
			default:
				agg.Concepts = append(agg.Concepts, entity)
			}
		}
	}
	if len(agg.Persons)+len(agg.Places)+len(agg.Concepts)+
		len(agg.ImportantPhrases)+len(agg.ImportantWords) == 0 {
		return nil
	}
	return agg
}

// GenerateArtifacts contains compatibility projections that are derived from
// the canonical durable result. It is not an independent entity source.
type GenerateArtifacts struct {
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`
}

// projectEntityCompatibility restores the legacy wire surfaces consumed by
// existing E2E clients while keeping EntityResult and VidRush segment results
// as the only semantic sources of truth.
func projectEntityCompatibility(result *GenerateResult, segments []scriptpkg.VidRushSegmentResult) {
	if result == nil {
		return
	}
	if len(segments) > 0 {
		result.Segments = append([]scriptpkg.VidRushSegmentResult(nil), segments...)
	}
	if result.Entities != nil {
		result.Artifacts = &GenerateArtifacts{Entities: result.Entities}
	}
}

// annotationCanonicalEntityID returns the canonical, readable identity
// ("person:floyd-mayweather", "gpe:los-angeles") of one annotation entity.
//
// Precedence — one identity owner, never a second spelling:
//
//  1. the id the Image Search Intent resolver stamped on the annotation
//     (AnnotatedEntity.CanonicalEntityID): the DISAMBIGUATED decision (an
//     Italian surface that canonicalizes to the English identity);
//  2. the deterministic derivation from (type, canonical name) through the
//     canonical identity owner (capabilities/entities.CanonicalEntityID), which
//     is exactly the derivation the image-search resolver and the entity-image
//     catalog use, so an unstamped annotation still yields the SAME id instead
//     of a locally invented one.
//
// Empty only when the annotation carries no usable (type, name); a caller must
// then treat the entity as identity-less rather than mint an id itself. Every
// consumer that needs the canonical id (overlay intent, entity card media
// index, semantic render bundle) joins through this one function.
func annotationCanonicalEntityID(entity scriptpkg.AnnotatedEntity) string {
	if id := strings.TrimSpace(entity.CanonicalEntityID); id != "" {
		return id
	}
	return capabilityentities.CanonicalEntityID(entity.Type, entity.CanonicalName)
}

// annotationStableEntityID returns the content-addressed machine identity
// ("ent_<16 hex>") of one annotation entity — the id the canonical entity
// timeline stamps on every occurrence and the render plane keys plan items by.
// It is derived through the same single owner as annotationCanonicalEntityID,
// so the annotation surface, the overlay intent and the plan item always agree
// WITHOUT any consumer comparing display names.
func annotationStableEntityID(entity scriptpkg.AnnotatedEntity) string {
	return capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)
}

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
