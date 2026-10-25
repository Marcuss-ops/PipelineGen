// Package scriptgeneration — semantic_plan_binding.go is the consumption half
// of the semantic production plan: it turns a satisfied, content-addressed
// planner hit into the canonical EntityImageBinding the overlay binder reads.
//
// The rule is deliberately strict. A hit is admitted only when it carries a
// verified SHA-256, because the overlay's media index is content-addressed and
// silently drops a binding without a hash. A URL-only hit stays a discovery
// record on the plan; it is not promoted to a rendered card, and the media
// stage remains the owner of acquiring those bytes.
package scriptgeneration

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ApplySemanticResolvedImages projects the plan's resolved, content-addressed
// hits onto the annotations of the scene each composition belongs to. It never
// overwrites an already-bound asset: the durable media pipeline is the
// authority, and the plan is a fallback for compositions that pipeline did not
// reach. Returns the number of bindings written.
func ApplySemanticResolvedImages(result *GenerateResult) int {
	if result == nil || result.SemanticProductionPlan == nil || len(result.Scenes) == 0 {
		return 0
	}
	plan := result.SemanticProductionPlan
	surfaces := make(map[string]string, len(plan.Identity))
	for _, identity := range plan.Identity {
		id := strings.TrimSpace(identity.ID)
		surface := strings.TrimSpace(identity.Surface)
		if id == "" || surface == "" {
			continue
		}
		surfaces[id] = surface
	}
	scenes := make(map[string]int, len(result.Scenes))
	for index := range result.Scenes {
		if id := strings.TrimSpace(result.Scenes[index].ID); id != "" {
			scenes[id] = index
		}
	}
	applied := 0
	for index := range plan.Units {
		unit := &plan.Units[index]
		hit := unit.ResolvedImage
		if hit == nil || strings.TrimSpace(hit.SHA256) == "" || unit.SearchStatus != ProductionSearchSatisfied {
			continue
		}
		// A composition naming several identities was retrieved with one
		// query; binding that photo to each of them would assert an identity
		// match the search never made. Leave multi-identity units to the
		// identity-aware media stage.
		if len(unit.EntityIDs) != 1 {
			continue
		}
		surface := strings.TrimSpace(surfaces[strings.TrimSpace(unit.EntityIDs[0])])
		if surface == "" {
			continue
		}
		sceneIndex, ok := scenes[strings.TrimSpace(unit.SceneID)]
		if !ok {
			continue
		}
		scene := &result.Scenes[sceneIndex]
		if scene.Annotations == nil {
			continue
		}
		if bindSemanticImageToScene(scene, surface, hit) {
			applied++
		}
	}
	return applied
}

// bindSemanticImageToScene writes the binding on the one annotation that names
// the identity. It preserves a verified existing binding, which is the media
// stage's output and must win over a plan fallback.
func bindSemanticImageToScene(scene *Scene, surface string, hit *SemanticResolvedImage) bool {
	if scene == nil || hit == nil || strings.TrimSpace(hit.SHA256) == "" {
		return false
	}
	for index := range scene.Annotations.PrimaryEntities {
		entity := &scene.Annotations.PrimaryEntities[index]
		if !sameEntitySurface(entity, surface) {
			continue
		}
		if preservedSemanticBinding(entity.Image) {
			return false
		}
		entity.Image = &scriptpkg.EntityImageBinding{
			Status:     "bound",
			AssetID:    firstNonEmpty(hit.AssetID, hit.SHA256),
			DriveLink:  strings.TrimSpace(hit.DriveLink),
			PreviewURL: strings.TrimSpace(hit.URL),
			SHA256:     strings.TrimSpace(hit.SHA256),
			MediaType:  firstNonEmpty(strings.TrimSpace(hit.MediaType), mediaTypeFromImageURL(hit.URL)),
			Source:     strings.TrimSpace(hit.Provider),
		}
		return true
	}
	return false
}

// preservedSemanticBinding reports whether the annotation already carries a
// verified, content-addressed image. Those are exactly the bindings the
// materializer produced, and a plan fallback must never replace them.
func preservedSemanticBinding(binding *scriptpkg.EntityImageBinding) bool {
	if binding == nil {
		return false
	}
	if strings.TrimSpace(binding.SHA256) == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(binding.Status), "bound") {
		return false
	}
	return strings.TrimSpace(binding.PreviewURL) != "" || strings.TrimSpace(binding.DriveLink) != "" || strings.TrimSpace(binding.LocalPath) != ""
}

func sameEntitySurface(entity *scriptpkg.AnnotatedEntity, surface string) bool {
	if entity == nil {
		return false
	}
	target := normalizeEntitySurface(surface)
	if target == "" {
		return false
	}
	for _, candidate := range []string{entity.CanonicalName, entity.Text} {
		if normalizeEntitySurface(candidate) == target {
			return true
		}
	}
	return false
}

func normalizeEntitySurface(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimPrefix(value, "the ")
}

func mediaTypeFromImageURL(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if cut := strings.IndexAny(lower, "?#"); cut >= 0 {
		lower = lower[:cut]
	}
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
