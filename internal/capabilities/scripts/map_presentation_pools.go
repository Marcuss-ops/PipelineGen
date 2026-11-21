package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func validateMapPresentationPools(p scriptpkg.MapPresentation) error {
	animations := make(map[string]bool, len(p.AnimationPool))
	for _, id := range p.AnimationPool {
		id = strings.TrimSpace(id)
		if id == "" || !stringPoolHas(capabilityoverlay.MapCompositionRandomPool(), id) {
			return fmt.Errorf("map_presentation.animation_pool contains unsupported runtime animation %q", id)
		}
		if animations[id] {
			return fmt.Errorf("map_presentation.animation_pool repeats %q", id)
		}
		animations[id] = true
	}
	if len(animations) == 0 {
		return fmt.Errorf("map_presentation.animation_pool has no renderer-supported choices")
	}
	if requested := strings.TrimSpace(p.Animation); requested != "" && !strings.EqualFold(requested, "random") && !strings.EqualFold(requested, "auto") && !capabilityoverlay.NativeMapCompositions(requested) {
		return fmt.Errorf("map_presentation.animation %q is not runtime-supported", requested)
	}
	styles := make(map[string]bool, len(p.StylePool))
	for _, id := range p.StylePool {
		id = strings.TrimSpace(id)
		if id == "" || !capabilityoverlay.NativeMapBasemaps()[id] {
			return fmt.Errorf("map_presentation.style_pool contains unsupported runtime basemap %q", id)
		}
		if styles[id] {
			return fmt.Errorf("map_presentation.style_pool repeats %q", id)
		}
		styles[id] = true
	}
	if len(styles) == 0 {
		return fmt.Errorf("map_presentation.style_pool has no renderer-supported choices")
	}
	if requested := strings.TrimSpace(p.Style); requested != "" && !strings.EqualFold(requested, "random") && !capabilityoverlay.NativeMapBasemaps()[requested] {
		return fmt.Errorf("map_presentation.style %q is not runtime-supported", requested)
	}
	if strings.HasPrefix(strings.TrimSpace(p.Animation), "editorial_") {
		if p.Style != "chronon_editorial" && !strings.EqualFold(p.Style, "random") {
			return fmt.Errorf("map_presentation.animation %q requires style=chronon_editorial", p.Animation)
		}
		if !styles["chronon_editorial"] {
			return fmt.Errorf("map_presentation.animation %q requires chronon_editorial in style_pool", p.Animation)
		}
	}
	return nil
}

// stringPoolHas reports whether id is one of the certified pool entries. It is
// deliberately not named containsString: that name belongs to a test helper in
// this package and the two were colliding at compile time.
func stringPoolHas(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
