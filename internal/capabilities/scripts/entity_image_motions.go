// Package scriptgeneration assigns deterministic motions to admitted images
// and applies the text-only named-entity-card ceiling.
package scriptgeneration

import (
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func entityImageCaption(item capmodel.OverlayItem) string {
	if item.EntityRef != nil {
		if name := strings.TrimSpace(item.EntityRef.Name); name != "" {
			return name
		}
	}
	return strings.TrimSpace(item.Text)
}

// resolveEntityImageMotionPool returns the caller pool when present, otherwise
// the complete certified generated-entity catalog. The Rust planner rejects an
// admitted entity image with an empty pool, and an omitted pool means automatic
// rotation, so the builder and the runtime branch share this resolution.
func resolveEntityImageMotionPool(requested []string) []string {
	if len(requested) > 0 {
		return requested
	}
	return capabilityoverlay.CertifiedEntityImageMotions()
}

// resolveEntityCaptionMotionPool returns the caller pool when present, otherwise
// the complete certified generated-entity caption catalog. The Rust planner
// rejects an admitted captioned entity image with an empty pool, and an omitted
// pool means automatic rotation, so the builder and the runtime branch share this resolution.
func resolveEntityCaptionMotionPool(requested []string) []string {
	if len(requested) > 0 {
		return requested
	}
	return capabilityoverlay.CertifiedEntityCaptionMotions()
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
