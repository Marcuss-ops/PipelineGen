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
	"fmt"
	"math"
	"sort"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
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
// that still reads as one shared visual sequence. Grouping prevents nearby
// images from being rendered as independent overlays that cover one another.
const (
	entityImageMergeGapMS       int64 = 5_000
	entityImageHoldMS           int64 = 1_500
	entityImageTravelMS         int64 = 2_000
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

// composeNearbyEntityImages groups two to six image-backed items from the same
// scene when their certified anchors are no more than five seconds apart.
// Entity portraits and contextual images share the same composite so each
// enters on its own anchor without covering the earlier images. Every child
// keeps its own caption, asset, preset and certified motion.
func composeNearbyEntityImages(items []capabilityoverlay.OverlayItem, width, height int) []capabilityoverlay.OverlayItem {
	indices := make([]int, 0, len(items))
	for index := range items {
		if isComposableImageItem(items[index]) {
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
	composedScenes := make(map[string]bool)
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
			composedScenes[strings.TrimSpace(first.SceneID)] = true
			remaining = remaining[groupSize:]
		}
		cursor += len(cluster)
	}

	for index, item := range items {
		if candidateIndexes[index] {
			continue
		}
		// A place label such as "Garlasco" is not part of a portrait
		// sequence. Keeping it in the same mixed chain creates a stray title
		// before the first photograph, so omit text-only location cards when
		// the scene already has a multi-portrait camera sequence.
		if composedScenes[strings.TrimSpace(item.SceneID)] && isTextOnlyLocationCard(item) {
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

func isComposableImageItem(item capabilityoverlay.OverlayItem) bool {
	if len(item.AssetRefs) != 1 {
		return false
	}
	switch item.Kind {
	case string(capabilityoverlay.KindEntityImage), "image":
		return true
	case string(capabilityoverlay.KindEntityCard):
		// Image-backed person cards carry a verified photo but still need to
		// join the same camera sequence as entity_image items.
		return item.EntityRef != nil && strings.EqualFold(strings.TrimSpace(item.EntityRef.Type), "PERSON")
	default:
		return false
	}
}

func isTextOnlyLocationCard(item capabilityoverlay.OverlayItem) bool {
	if len(item.AssetRefs) != 0 {
		return false
	}
	if item.EntityRef != nil && strings.EqualFold(strings.TrimSpace(item.EntityRef.Type), "LOCATION") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(item.Kind), "location")
}

func composeEntityImageGroup(items []capabilityoverlay.OverlayItem, group []int, width, height int) capabilityoverlay.OverlayItem {
	first := items[group[0]]
	parentStart, parentEnd := first.StartMs, first.EndMs
	parent := first
	assetRefs := make([]capabilityoverlay.OverlayAssetRef, 0, len(group))
	layers := make([]capabilityoverlay.OverlayImageLayer, 0, len(group))
	sequenceStarts := make([]int64, len(group))
	for slot, itemIndex := range group {
		child := items[itemIndex]
		anchorOffset := max(int64(0), child.StartMs-parentStart)
		minimumOffset := int64(slot) * (entityImageHoldMS + entityImageTravelMS)
		if slot > 0 {
			minimumOffset = sequenceStarts[slot-1] + entityImageHoldMS + entityImageTravelMS
		}
		sequenceStarts[slot] = max(anchorOffset, minimumOffset)
	}
	// The composite is a spatial set: all portraits and labels exist from the
	// start, while the camera reveals each pair by travelling to its position.
	parentEnd = parentStart + sequenceStarts[len(sequenceStarts)-1] + entityImageHoldMS
	groupDuration := parentEnd - parentStart
	for slot, itemIndex := range group {
		child := items[itemIndex]
		offset := sequenceStarts[slot]
		// StartMS remains the camera departure marker. RenderingGen keeps each
		// portrait and label in the spatial scene for the full group duration.
		layerStart := offset
		layerEnd := groupDuration
		assetRefs = append(assetRefs, child.AssetRefs[0])
		layers = append(layers, capabilityoverlay.OverlayImageLayer{
			ID: child.ID, AssetID: child.AssetRefs[0].AssetID, EntityID: stableIDForOverlayItem(child),
			StartMS: layerStart, EndMS: layerEnd, CaptionStartMS: 0, CaptionEndMS: groupDuration, PresetID: child.PresetID,
			CaptionLayout: "below",
			Caption:       entityImageCaption(child),
			Params:        entityImageLayerParams(width, height, len(group), slot),
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
	// image_layers are carried by the entity_image semantic primitive even
	// when the group contains contextual images without entity captions.
	parent.Kind = string(capabilityoverlay.KindEntityImage)
	parent.TemplateID = "image_popup"
	// The sequence owns the single shared camera rig; a sampled single-entity
	// composition must not layer a second camera animation onto the group.
	parent.EntityStyleID = ""
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
	parent.Params = map[string]any{"camera_travel_ms": entityImageTravelMS}
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
	// Bound each photo to most of the canvas while preserving safe caption room.
	// Contain preserves the source aspect ratio; one canvas-width step keeps
	// neighboring photos and captions outside the camera viewport.
	boxHeight := float64(height) * 0.76
	boxWidth := float64(width) * 0.90
	positionX, positionY := float64(slot)*float64(width), 0.0
	if count > 1 {
		// A shallow shared arc gives the same pan a natural, data-derived angle
		// between stops without introducing separate camera motion modes.
		positionY = math.Sin(2*math.Pi*float64(slot)/float64(count-1)) * 0.045 * float64(height)
	}
	return map[string]any{"width": int(boxWidth), "height": int(boxHeight), "position_x": positionX, "position_y": positionY, "fit": "contain"}
}

// assignEntityImageMotions samples a fresh pool offset once per newly compiled
// plan, then rotates through the complete certified 32-motion image catalog.
// In a composite each portrait consumes its own ordinal, so every image gets
// an independent motion and a run of up to 20 images avoids repeats.
//
// The broader image catalog remains callable for explicit editorial plans;
// this automatic path avoids abrupt flips and strong perspective effects.
// entityStyleID is an explicitly requested entity-card composition selector.
// Empty leaves the planner's simple image and caption motions in place, so
// spatial compositions are never added implicitly to runtime renders.
// requestedPools[0] is the optional run-level entity-image pool
// (entity_image_motions); requestedPools[1] is the optional run-level
// entity-caption pool (entity_caption_motions), restricted to the
// camera-safe automatic caption catalog because it drives automatic
// assignment. Both are validated fail-closed.
func assignEntityImageMotions(items []capabilityoverlay.OverlayItem, offset, width, height int, entityStyleID string, requestedPools ...[]string) error {
	imageOrdinal, captionOrdinal := 0, 0
	// Composite groups draw from a per-size entrance pool (see
	// CompositeImageMotionPool); each size rotates its own ordinal so a 2-, 3-,
	// 4- or 5-image group never reuses another size's entrance family.
	compositeOrdinal := make(map[int]int)
	compositeMotion := func(size int) string {
		pool := capabilityoverlay.CompositeImageMotionPool(size)
		if len(pool) == 0 {
			return ""
		}
		index := (offset + compositeOrdinal[size]) % len(pool)
		if index < 0 {
			index += len(pool)
		}
		compositeOrdinal[size]++
		return pool[index]
	}
	var requested []string
	if len(requestedPools) > 0 {
		requested = requestedPools[0]
	}
	var requestedCaptions []string
	if len(requestedPools) > 1 {
		requestedCaptions = requestedPools[1]
	}
	allowed := make(map[string]bool)
	for _, id := range capabilityoverlay.CertifiedEntityImageMotions() {
		allowed[id] = true
	}
	for _, id := range capabilityoverlay.CertifiedLightLeakMotions() {
		allowed[id] = true
	}
	seen := make(map[string]bool, len(requested))
	for _, id := range requested {
		if !allowed[id] {
			return fmt.Errorf("entity_image_motions contains uncertified motion %q", id)
		}
		if seen[id] {
			return fmt.Errorf("entity_image_motions repeats motion %q", id)
		}
		seen[id] = true
	}
	captionAllowed := make(map[string]bool)
	for _, id := range capabilityoverlay.CertifiedAutomaticEntityCaptionMotions() {
		captionAllowed[id] = true
	}
	seenCaption := make(map[string]bool, len(requestedCaptions))
	for _, id := range requestedCaptions {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("entity_caption_motions carries an empty id")
		}
		if !captionAllowed[id] {
			return fmt.Errorf("entity_caption_motions contains uncertified motion %q", id)
		}
		if seenCaption[id] {
			return fmt.Errorf("entity_caption_motions repeats motion %q", id)
		}
		seenCaption[id] = true
	}
	captionMotionAt := func(ordinal int) string {
		// A run-level caption pool rotates offset-aware like the automatic
		// pool so retries of the same plan stay bit-identical.
		if len(requestedCaptions) > 0 {
			index := (offset + ordinal) % len(requestedCaptions)
			if index < 0 {
				index += len(requestedCaptions)
			}
			return requestedCaptions[index]
		}
		return capabilityoverlay.RuntimeEntityCaptionMotionAtOffset(offset, ordinal)
	}
	pinned := strings.TrimSpace(entityStyleID)
	imageMotion := func() string {
		motion := ""
		if len(requested) > 0 {
			// A run-level image pool rotates offset-aware like the
			// automatic pool and the caption pool below: without the
			// offset every run replayed the payload pool from index 0,
			// so the same entrances recurred in the same order.
			index := (offset + imageOrdinal) % len(requested)
			if index < 0 {
				index += len(requested)
			}
			motion = requested[index]
		} else {
			motion = capabilityoverlay.EntityImageMotionAtOffset(offset, imageOrdinal)
		}
		imageOrdinal++
		return motion
	}
	for itemIndex := range items {
		item := &items[itemIndex]
		if item.Kind == "image" && strings.TrimSpace(item.EntityCaption) != "" {
			item.MotionID = imageMotion()
			item.CaptionMotionID = captionMotionAt(captionOrdinal)
			captionOrdinal++
			if pinned != "" {
				item.EntityStyleID = pinned
			}
			item.ImagePresetID = "image_scale_in"
			continue
		}
		if item.Kind == "image" {
			continue
		}
		if item.Kind != string(capabilityoverlay.KindEntityImage) {
			continue
		}
		if len(item.ImageLayers) > 0 {
			item.CameraMotionID = capabilityoverlay.EntitySequenceCameraMotion()
			for layerIndex := range item.ImageLayers {
				layer := &item.ImageLayers[layerIndex]
				if len(requested) > 0 {
					layer.MotionID = imageMotion()
				} else {
					layer.MotionID = compositeMotion(len(item.ImageLayers))
				}
				if strings.TrimSpace(layer.Caption) != "" {
					layer.CaptionMotionID = captionMotionAt(captionOrdinal)
					captionOrdinal++
				} else {
					layer.CaptionMotionID = ""
				}
				layer.MotionParams = map[string]any{"enter_frames": 8}
			}
			continue
		}
		item.MotionID = imageMotion()
		if strings.TrimSpace(item.EntityCaption) != "" {
			item.CaptionMotionID = captionMotionAt(captionOrdinal)
			captionOrdinal++
			item.EntityStyleID = pinned
		} else {
			item.CaptionMotionID = ""
		}
		item.MotionParams = map[string]any{"enter_frames": 8}
		// The geometry rebuild must not drop the payload's runtime text
		// style: overlay_plan stamped font/glow/stroke/shadow/size onto this
		// item before motions were assigned, and the caption compiler reads
		// them from Params. Re-seed geometry, carry the text style through.
		geometry := capabilityoverlay.EntityImageParams(width, height)
		for key, value := range item.Params {
			if scriptoverlay.IsRuntimeTextStyleParam(key) {
				geometry[key] = value
			}
		}
		item.Params = geometry
	}
	return nil
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
