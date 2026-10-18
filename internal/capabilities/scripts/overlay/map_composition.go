// Package overlay — map_composition.go owns the certified cartographic
// composition selection: family classification, data/presentation
// compatibility, and the deterministic family-first random draw.
//
// Selection contract (see the map audit):
//
//   - Random draws across composition FAMILIES first and within the chosen
//     family second, through the shared deterministic sampler, so one job
//     replays byte-identically while a new job may roll another variant.
//   - Random only ever selects variants compatible with the DATA (pin count,
//     authored editorial geography) and with the selected camera/label
//     presentation. Incompatible variants are excluded, never silently swapped.
//   - An explicit certified id wins verbatim unless its certified presentation
//     cannot run beside the selected camera/label — then it fails closed.
package overlay

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// MapCompositionInput is the selection context: what the planner decided
// (strategy, per-pin spoken timing) and what the composition must honour
// (pin count, duration, camera/label pairing).
type MapCompositionInput struct {
	PinCount   int
	Pins       []map[string]any
	DurationUS int64
	Strategy   string
	Camera     string
	Label      string
}

// MapCompositionFamily is the editorial family of a certified cartographic
// composition. Random selection draws across the FAMILIES compatible with the
// data and the backend, so adding compositions enriches each family instead of
// deepening one look.
type MapCompositionFamily string

const (
	MapFamilyLocationFocus MapCompositionFamily = "location_focus"
	MapFamilyMultiPin      MapCompositionFamily = "multi_pin"
	MapFamilyJourney       MapCompositionFamily = "journey_route"
	MapFamilyRegion        MapCompositionFamily = "region_highlight"
	MapFamilyComparison    MapCompositionFamily = "comparison"
	MapFamilyEditorial     MapCompositionFamily = "editorial_documentary"
)

// MapCompositionFamilyOf classifies one certified composition id. Unknown ids
// belong to the single-location family; a route composition joins the journey
// family as soon as the catalog certifies one.
func MapCompositionFamilyOf(id string) MapCompositionFamily {
	switch strings.TrimSpace(id) {
	case "region_zoom":
		return MapFamilyRegion
	case "pulse_marker", "spotlight":
		return MapFamilyLocationFocus
	case "route_line", "journey_route":
		return MapFamilyJourney
	case "multi_locations", "multi_pin_reveal":
		return MapFamilyMultiPin
	case "comparison_split", "territory_compare":
		return MapFamilyComparison
	}
	if strings.HasPrefix(strings.TrimSpace(id), "ed_") {
		return MapFamilyEditorial
	}
	return MapFamilyLocationFocus
}

// MapEditorialSceneBounds mirrors the Python renderer's authored camera bounds
// (render_dynamic_map_overlay.py scene_camera_bounds). An editorial scene is
// only eligible for a map whose pins lie inside its authored geography —
// otherwise the compositor's own fail-closed off-screen gate would abort the
// render, so the selector excludes it instead.
var MapEditorialSceneBounds = map[string][4]float64{
	// {south, west, north, east}
	"ed_saudi_base":     {16.0, 34.0, 33.0, 56.0},
	"ed_iran_glow":      {23.5, 46.0, 32.5, 57.0},
	"ed_yemen_fill":     {11.0, 42.0, 20.0, 53.0},
	"ed_yemen_markers":  {12.2, 40.5, 19.8, 47.0},
	"ed_hormuz_routes":  {5.0, 37.0, 29.0, 75.0},
	"ed_mocha_tactical": {12.4, 42.0, 15.8, 45.2},
}

// MapCompositionPresentationCompatible is the camera/label pairing gate: a
// composition whose certified presentation cannot run beside the selected
// camera/label treatment is excluded from random selection and rejected for
// explicit requests (the `exact` error path).
func MapCompositionPresentationCompatible(id string, input MapCompositionInput) error {
	label := strings.TrimSpace(input.Label)
	if strings.HasPrefix(id, "ed_") && label != "" && label != "none" {
		return fmt.Errorf("certified editorial map scenes require label_animation=none, got %q", label)
	}
	return nil
}

// MapCompositionGeographyCompatible reports whether every pin lies inside the
// composition's authored geography (editorial scenes only).
func MapCompositionGeographyCompatible(id string, pins []map[string]any) bool {
	bounds, authored := MapEditorialSceneBounds[id]
	if !authored {
		return true
	}
	if len(pins) == 0 {
		return false
	}
	for _, pin := range pins {
		lat, okLat := MapPinFloat(pin, "latitude")
		lon, okLon := MapPinFloat(pin, "longitude")
		if !okLat || !okLon {
			return false
		}
		if lat < bounds[0] || lat > bounds[2] || lon < bounds[1] || lon > bounds[3] {
			return false
		}
	}
	return true
}

// MapPinFloat reads one numeric pin coordinate from a wire-shaped pin entry.
func MapPinFloat(pin map[string]any, key string) (float64, bool) {
	switch value := pin[key].(type) {
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	}
	return 0, false
}

// MapCompositionCertified lists every composition id the Python renderer
// accepts: the map systems plus the GPU-certified editorial scenes.
func MapCompositionCertified() []string {
	return capabilityoverlay.AnimationCandidates("map.composition")
}

// MapCompositionRandomPool is the restrained random-draw pool (the subset of
// certified compositions the sampler may pick when the caller chose nothing).
func MapCompositionRandomPool() []string {
	return []string{"region_zoom", "pulse_marker", "spotlight"}
}

// EligibleMapCompositions removes only renderer-documented multi-pin systems
// that cannot run for this map's grounded pin count. Explicit selections are
// NOT filtered here — they validate against the full certified catalog.
func EligibleMapCompositions(pinCount int, certified []string) []string {
	allowed := make(map[string]bool, len(certified))
	for _, id := range certified {
		allowed[id] = true
	}
	out := capabilityoverlay.AnimationMapCompositionCandidates(pinCount)
	filtered := out[:0]
	for _, id := range out {
		if allowed[id] {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// EligibleMapCompositionsFor removes every composition the DATA cannot carry:
// pin-count constraints, the camera/label pairing and the authored editorial
// geography.
func EligibleMapCompositionsFor(input MapCompositionInput, certified []string) []string {
	out := EligibleMapCompositions(input.PinCount, certified)
	filtered := out[:0]
	for _, id := range out {
		if MapCompositionPresentationCompatible(id, input) != nil {
			continue
		}
		if !MapCompositionGeographyCompatible(id, input.Pins) {
			continue
		}
		filtered = append(filtered, id)
	}
	return filtered
}

// DeterministicMapCompositionChoice draws across composition FAMILIES first
// and within the chosen family second, both through the shared deterministic
// sampler: one job replays byte-identically, a new job may roll another
// variant, and each family stays represented as the catalog grows.
func DeterministicMapCompositionChoice(seed, slot string, pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	if len(pool) == 1 {
		return pool[0]
	}
	familyIndex := make(map[MapCompositionFamily][]string, len(pool))
	families := make([]string, 0, len(pool))
	for _, id := range pool {
		family := MapCompositionFamilyOf(id)
		if _, seen := familyIndex[family]; !seen {
			families = append(families, string(family))
		}
		familyIndex[family] = append(familyIndex[family], id)
	}
	mapSlot := string(media.SlotMap)
	family := capabilityoverlay.DeterministicPresetChoice(seed, mapSlot, slot, "map_composition_family", families)
	return capabilityoverlay.DeterministicPresetChoice(seed, mapSlot, slot, "map_composition", familyIndex[MapCompositionFamily(family)])
}

// ResolveMapCompositionFor resolves ONE cartographic composition against the
// planner's decision and the composition's data. Empty and "random" draw
// deterministically across the compatible families; an explicit certified id
// wins verbatim unless its certified presentation cannot run beside the
// selected camera/label (then it fails closed — never a silent swap). The
// second result is the DECLARED fallback reason recorded in the map decision
// report when the planner's strategy redirected the presentation.
func ResolveMapCompositionFor(requested, seed, slot string, input MapCompositionInput) (string, string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" && !strings.EqualFold(requested, "random") {
		found := false
		for _, candidate := range MapCompositionCertified() {
			if requested == candidate {
				found = true
				break
			}
		}
		if !found {
			return "", "", fmt.Errorf("unsupported runtime map_animation %q", requested)
		}
		if err := MapCompositionPresentationCompatible(requested, input); err != nil {
			return "", "", fmt.Errorf("map_animation %q is incompatible with the selected presentation: %w", requested, err)
		}
		return requested, "", nil
	}
	pool := EligibleMapCompositionsFor(input, MapCompositionRandomPool())
	selected := DeterministicMapCompositionChoice(seed, slot, pool)
	if selected == "" {
		return "", "", fmt.Errorf("no certified map composition available for %d pins", input.PinCount)
	}
	fallbackReason := ""
	if strings.TrimSpace(input.Strategy) == "multi_pin" && input.PinCount > 1 {
		// The spoken span cannot carry a stop-by-stop tour (the planner's
		// declared decision): the composition shows every pin together and
		// activates each one on its spoken timing. Nothing is dropped.
		fallbackReason = "duration_too_short_for_tour:declared multi_pin composition keeps all places"
	}
	return selected, fallbackReason, nil
}
