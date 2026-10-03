// Package scriptgeneration — entity_projection.go owns the deterministic
// projection of the incremental VidRush enrichment results onto the durable
// result's typed entity aggregate (persons / places / concepts), the legacy
// compatibility projection derived from it, the SINGLE derivation of an
// annotation entity's identity, and the entity-image overlay composition
// (grouping nearby portraits into composites of up to five items with per-layer
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

// entityImageMergeGapMS is the largest gap between certified mention anchors
// that still reads as one short visual beat: mentions five seconds apart or
// closer are presented together inside ONE composite overlay instead of two
// sequential image videos.
const (
	entityImageMergeGapMS       int64 = 5_000
	maxEntityImageGroup               = 5
	maxNamedEntityCardsPerScene       = 2
)

// capNamedEntityCardsPerScene keeps the requested two-card ceiling separate
// from entity-image composites: only text-only named entity cards are capped,
// while image-backed entities can still form groups of two to five. Resolver
// items arrive importance-ranked, so retaining the earliest two is deterministic
// and preserves the canonical ranker's choice.
func capNamedEntityCardsPerScene(items []capabilityoverlay.OverlayItem, max int) []capabilityoverlay.OverlayItem {
	if max <= 0 {
		return items
	}
	counts := make(map[string]int)
	out := make([]capabilityoverlay.OverlayItem, 0, len(items))
	for _, item := range items {
		if entityCardKind(capabilityoverlay.OverlayKind(item.Kind)) && len(item.AssetRefs) == 0 && strings.TrimSpace(item.SceneID) != "" {
			sceneID := strings.TrimSpace(item.SceneID)
			if counts[sceneID] >= max {
				continue
			}
			counts[sceneID]++
		}
		out = append(out, item)
	}
	return out
}

// composeNearbyEntityImages groups two to five image-backed entity items from
// the same scene when their certified spoken anchors are no more than five
// seconds apart. Every portrait retains its own relative reveal time, caption,
// asset, preset and independently selected certified motion. Isolated images
// and any remainder beyond a five-image group stay as separate items.
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

	candidateIndexes := make(map[int]bool, len(indices))
	for _, index := range indices {
		candidateIndexes[index] = true
	}
	var out []capabilityoverlay.OverlayItem
	for cursor := 0; cursor < len(indices); {
		firstIndex := indices[cursor]
		first := items[firstIndex]
		cluster := []int{firstIndex}
		for next := cursor + 1; first.SceneID != "" && next < len(indices); next++ {
			candidateIndex := indices[next]
			candidate := items[candidateIndex]
			prior := items[cluster[len(cluster)-1]]
			gap := candidate.StartMs - prior.StartMs
			if candidate.SceneID != first.SceneID || gap < 0 || gap > entityImageMergeGapMS {
				break
			}
			cluster = append(cluster, candidateIndex)
		}
		if len(cluster) < 2 {
			out = append(out, first)
			cursor++
			continue
		}

		for remaining := cluster; len(remaining) > 0; {
			groupSize := min(maxEntityImageGroup, len(remaining))
			// Never strand one image after a composite; repartition (6→4+2,
			// 11→5+4+2) so every nearby group has 2–5 children.
			if len(remaining)-groupSize == 1 {
				groupSize--
			}
			out = append(out, composeEntityImageGroup(items, remaining[:groupSize], width, height))
			remaining = remaining[groupSize:]
		}
		cursor += len(cluster)
	}

	for index, item := range items {
		if candidateIndexes[index] {
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

func composeEntityImageGroup(items []capabilityoverlay.OverlayItem, group []int, width, height int) capabilityoverlay.OverlayItem {
	first := items[group[0]]
	parentStart, parentEnd := first.StartMs, first.EndMs
	parent := first
	assetRefs := make([]capabilityoverlay.OverlayAssetRef, 0, len(group))
	layers := make([]capabilityoverlay.OverlayImageLayer, 0, len(group))
	for _, itemIndex := range group {
		if items[itemIndex].EndMs > parentEnd {
			parentEnd = items[itemIndex].EndMs
		}
	}
	groupDuration := parentEnd - parentStart
	for slot, itemIndex := range group {
		child := items[itemIndex]
		offset := child.StartMs - parentStart
		assetRefs = append(assetRefs, child.AssetRefs[0])
		layers = append(layers, capabilityoverlay.OverlayImageLayer{
			ID: child.ID, AssetID: child.AssetRefs[0].AssetID, EntityID: stableIDForOverlayItem(child),
			StartMS: offset, EndMS: groupDuration, PresetID: child.PresetID,
			Caption: entityImageCaption(child),
			Params:  entityImageLayerParams(width, height, len(group), slot),
		})
	}
	parent.ID = first.ID
	for _, itemIndex := range group[1:] {
		parent.ID += "+" + items[itemIndex].ID
	}
	parent.EndMs = parentEnd
	parent.StartUS = first.StartUSValue()
	parent.DurationUS = parentEnd*1_000 - parent.StartUS
	parent.AssetRefs = assetRefs
	parent.MotionID = ""
	parent.MotionParams = nil
	parent.Params = nil
	parent.RenderKey = ""
	parent.ImageLayers = layers
	// Composite names are rendered from each child layer. A parent caption
	// would duplicate the first entity's name over the whole group.
	parent.EntityCaption = ""
	parent.CaptionMotionID = ""
	captions := make([]string, 0, len(layers))
	for _, layer := range layers {
		if layer.Caption != "" {
			captions = append(captions, layer.Caption)
		}
	}
	parent.Text = strings.Join(captions, " • ")
	// The parent represents a group, not the first person only. Keep identities
	// on child layers for intent joins and avoid misleading single-entity
	// timelines or deduplication at downstream consumers.
	parent.EntityID = ""
	parent.EntityRef = nil
	return parent
}

func stableIDForOverlayItem(item capabilityoverlay.OverlayItem) string {
	if strings.TrimSpace(item.EntityID) != "" {
		return item.EntityID
	}
	if item.EntityRef != nil && strings.TrimSpace(item.EntityRef.EntityID) != "" {
		return item.EntityRef.EntityID
	}
	if item.EntityRef != nil && strings.TrimSpace(item.EntityRef.Type) != "" && strings.TrimSpace(item.EntityRef.Name) != "" {
		return capabilityentities.StableEntityID(item.EntityRef.Type, item.EntityRef.Name)
	}
	return ""
}

func entityImageCaption(item capabilityoverlay.OverlayItem) string {
	if item.EntityRef != nil {
		if name := strings.TrimSpace(item.EntityRef.Name); name != "" {
			return name
		}
	}
	return strings.TrimSpace(item.Text)
}

func entityImageLayerParams(width, height, count, slot int) map[string]any {
	if width <= 0 || height <= 0 {
		width, height = 1920, 1080
	}
	boxWidth, boxHeight := width*22/100, height*42/100
	// Lift entity groups into the visual center. The caption renderer places
	// each name below its image, so a small upward bias keeps the whole card
	// (portrait + name) centered instead of pinning the name to the bottom.
	positionX, positionY := 0.0, -float64(height)*0.10
	switch count {
	case 3:
		boxWidth, boxHeight = width*29/100, height*48/100
		positionX = float64(slot-1) * float64(width) * 0.29
	case 4:
		boxWidth, boxHeight = width*29/100, height*39/100
		positionX = []float64{-0.17, 0.17, -0.17, 0.17}[slot] * float64(width)
		positionY += []float64{-0.10, -0.10, 0.14, 0.14}[slot] * float64(height)
	case 5:
		boxWidth, boxHeight = width*24/100, height*34/100
		positions := [][2]float64{{-0.25, -0.10}, {0, -0.10}, {0.25, -0.10}, {-0.16, 0.16}, {0.16, 0.16}}
		positionX = positions[slot][0] * float64(width)
		positionY += positions[slot][1] * float64(height)
	default:
		boxWidth, boxHeight = width*34/100, height*56/100
		if slot == 0 {
			positionX = -float64(width) * 0.18
		} else {
			positionX = float64(width) * 0.18
		}
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	if boxHeight < 1 {
		boxHeight = 1
	}
	return map[string]any{
		"width": boxWidth, "height": boxHeight,
		"position_x": positionX, "position_y": positionY,
		"fit": "contain",
	}
}

// assignEntityImageMotions samples a fresh pool offset once per newly compiled
// plan, then rotates through the curated generated-portrait motions. In a
// composite each portrait consumes its own ordinal, so entrances stay subtle
// while retaining a small amount of variation.
//
// The broader image catalog remains callable for explicit editorial plans;
// this automatic path avoids abrupt flips and strong perspective effects.
func assignEntityImageMotions(items []capabilityoverlay.OverlayItem, offset, width, height int) {
	ordinal := 0
	for itemIndex := range items {
		item := &items[itemIndex]
		if item.Kind != string(capabilityoverlay.KindEntityImage) {
			continue
		}
		if len(item.ImageLayers) > 0 {
			for layerIndex := range item.ImageLayers {
				item.ImageLayers[layerIndex].MotionID = capabilityoverlay.EntityImageMotionAtOffset(offset, ordinal)
				item.ImageLayers[layerIndex].MotionParams = map[string]any{"enter_frames": 8}
				ordinal++
			}
			continue
		}
		item.MotionID = capabilityoverlay.EntityImageMotionAtOffset(offset, ordinal)
		item.MotionParams = map[string]any{"enter_frames": 8}
		item.Params = capabilityoverlay.EntityImageParams(width, height)
		ordinal++
	}
}

// projectSceneEntityResult builds the per-scene typed EntityResult from one
// segment's extracted entities, using the same classification as the
// aggregate projection (PERSON → persons; LOCATION/PLACE/COUNTRY/CITY →
// places; every other type → concepts). It returns an explicit empty result
// (never nil) so a scene with no entities is represented as entities=[] with
// entity_overlay_required=false — no entity is invented.
func projectSceneEntityResult(seg scriptpkg.VidRushSegmentResult) *scriptpkg.EntityResult {
	res := &scriptpkg.EntityResult{
		Persons:          []scriptpkg.Entity{},
		Places:           []scriptpkg.Entity{},
		Concepts:         []scriptpkg.Entity{},
		ImportantPhrases: append([]string(nil), seg.Insights.ImportantPhrases...),
		ImportantWords:   append([]string(nil), seg.Insights.ImportantWords...),
	}
	for _, ent := range seg.Insights.Entities {
		value := strings.TrimSpace(ent.Value)
		if value == "" {
			continue
		}
		entity := scriptpkg.Entity{Value: value, Type: ent.Type, Score: float32(ent.Confidence)}
		switch strings.ToUpper(strings.TrimSpace(ent.Type)) {
		case "PERSON":
			res.Persons = append(res.Persons, entity)
		case "LOCATION", "PLACE", "COUNTRY", "CITY":
			res.Places = append(res.Places, entity)
		default:
			res.Concepts = append(res.Concepts, entity)
		}
	}
	return res
}

// entityResultHasValues reports whether a per-scene EntityResult carries at
// least one typed entity value (the entity_overlay_required signal). It
// never invents an entity: an empty result is false.
func entityResultHasValues(res *scriptpkg.EntityResult) bool {
	if res == nil {
		return false
	}
	return len(res.Persons)+len(res.Places)+len(res.Concepts) > 0
}

// safeEntityID normalizes a value into a lowercase alphanumeric ID.
func safeEntityID(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
