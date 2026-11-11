// Package scriptgeneration assigns deterministic motions to admitted images
// and applies the text-only named-entity-card ceiling.
package scriptgeneration

import (
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// capNamedEntityCardsPerScene caps text-only named entity cards. Resolver
// items arrive importance-ranked, so retaining the earliest two is deterministic
// and preserves the canonical ranker's choice.
func capNamedEntityCardsPerScene(items []capmodel.OverlayItem, max int) []capmodel.OverlayItem {
	if max <= 0 {
		return items
	}
	counts := make(map[string]int)
	out := make([]capmodel.OverlayItem, 0, len(items))
	for _, item := range items {
		if entityCardKind(capmodel.OverlayKind(item.Kind)) && len(item.AssetRefs) == 0 && strings.TrimSpace(item.SceneID) != "" {
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

func entityImageCaption(item capmodel.OverlayItem) string {
	if item.EntityRef != nil {
		if name := strings.TrimSpace(item.EntityRef.Name); name != "" {
			return name
		}
	}
	return strings.TrimSpace(item.Text)
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
func assignEntityImageMotions(items []capmodel.OverlayItem, offset, width, height int, entityStyleID string, requestedPools ...[]string) error {
	imageOrdinal, captionOrdinal := 0, 0
	// Composite groups draw from a per-size entrance pool (see
	// caprender.CompositeImageMotionPool); each size rotates its own ordinal so a 2-, 3-,
	// 4- or 5-image group never reuses another size's entrance family.
	compositeOrdinal := make(map[int]int)
	compositeMotion := func(size int) string {
		pool := caprender.CompositeImageMotionPool(size)
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
		if item.Kind != string(capmodel.KindEntityImage) {
			continue
		}
		if len(item.ImageLayers) > 0 {
			item.CameraMotionID = ""
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
			if capabilityoverlay.IsRuntimeTextStyleParam(key) {
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
		switch {
		case entitytype.Is(ent.Type, entitytype.Person):
			res.Persons = append(res.Persons, entity)
		case entitytype.Is(ent.Type, entitytype.Place):
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
