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
//   - The certified vocabulary is the projection of the native map renderer's
//     own catalog (ChrononPlugin/plugins/maps/maps-renderer-catalog.v1.json):
//     the retired Python compositions are gone from the registry, so a stale
//     request fails by name here and never reaches a renderer that no longer
//     draws it.
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
	// TerritoryFeature is the ADM3 shapeName resolved for the map's FIRST pin
	// (the renderer draws the territory of pins.front()); empty means the
	// catalog could not resolve the pin and territory compositions are data-
	// incompatible (TICKET map parity: the renderer matches the payload value
	// EXACTLY against the ADM3 catalog, so callers may never guess a name).
	TerritoryFeature string
	// AreaGlowRadiusKM carries the plan item's area glow radius: the ported
	// area_glow_pulse composition refuses to render without a positive one.
	AreaGlowRadiusKM float64
}

// MapCompositionFamily is the editorial family of a certified cartographic
// composition. Random selection draws across the FAMILIES compatible with the
// data and the backend, so adding compositions enriches each family instead of
// deepening one look.
type MapCompositionFamily string

const (
	MapFamilyLocationFocus MapCompositionFamily = capabilityoverlay.MapFamilyLocationFocus
	MapFamilyMultiPin      MapCompositionFamily = capabilityoverlay.MapFamilyMultiPin
	MapFamilyJourney       MapCompositionFamily = capabilityoverlay.MapFamilyJourney
	MapFamilyRegion        MapCompositionFamily = capabilityoverlay.MapFamilyRegion
	MapFamilyComparison    MapCompositionFamily = capabilityoverlay.MapFamilyComparison
	MapFamilyEditorial     MapCompositionFamily = capabilityoverlay.MapFamilyEditorial
)

// MapCompositionFamilyOf classifies one certified composition id. The single
// classification switch lives with the registry projection
// (capabilityoverlay.MapCompositionFamilyOf); this is the typed facade, so
// planner and resolver can never disagree about a family.
func MapCompositionFamilyOf(id string) MapCompositionFamily {
	return MapCompositionFamily(capabilityoverlay.MapCompositionFamilyOf(id))
}

// MapEditorialSceneBounds is the Go projection of the single geographic
// manifest (map_geo_manifest.go): accepted_pin_bounds filters admissibility.
// The Python camera projection (scene_camera_bounds) is generated from the
// same manifest — see ChrononTemplate/tools/map/map_geo_manifest.json.
// Never edit bounds in two places; edit the manifest and regenerate.
var MapEditorialSceneBounds = func() map[string][4]float64 {
	out := make(map[string][4]float64, len(MapGeoManifest))
	for k, v := range MapGeoManifest {
		out[k] = v.AcceptedPinBounds
	}
	return out
}()

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

// MapCompositionCertified lists only compositions exposed by the native map
// renderer. Gallery concepts remain in the global animation registry for
// reference, but explicit and random map requests cannot select them.
func MapCompositionCertified() []string {
	return capabilityoverlay.PortedMapCompositions()
}

// MapCompositionRandomPool is the restrained random-draw pool: the subset of
// certified compositions the sampler may pick when the caller chose nothing.
// It projects the single ported-subset declaration
// (capabilityoverlay.PortedMapCompositions) — the compositions the single
// dynamic renderer (ChrononPlugin native) draws from the data the planner
// supplies. A draw that fits no pool composition resolves to the certified
// base flyover presentation; a composition joins the pool the moment the
// renderer draws it from planner-supplied data.
func MapCompositionRandomPool() []string {
	return capabilityoverlay.PortedMapCompositions()
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
		// Random callers drop, never fail, on data incompatibility (the same
		// rules the explicit gate reports with a precise reason).
		if err := mapCompositionWireDataCompatible(id, input); err != nil {
			continue
		}
		filtered = append(filtered, id)
	}
	return filtered
}

// mapCompositionWireDataCompatible is the shared native-payload data
// compatibility gate used by BOTH the random eligibility filter and the
// explicit admission (AdmitMapComposition). One rule set, two surfacings:
// random skips, exact requests fail closed with the reason.
func mapCompositionWireDataCompatible(id string, input MapCompositionInput) error {
	if MapTerritoryOverlayRequired(id) && strings.TrimSpace(input.TerritoryFeature) == "" {
		return fmt.Errorf("map_animation %q requires an ADM3 territory_feature resolvable for the map's first pin", id)
	}
	if id == "area_glow_pulse" && input.AreaGlowRadiusKM <= 0 {
		return fmt.Errorf("map_animation %q requires a positive area_glow_radius_km payload", id)
	}
	return nil
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

// AdmitMapComposition is the SINGLE admission gate for cartographic
// compositions, identical for random and exact requests. It checks pin-count
// eligibility, camera/label presentation compatibility and authored editorial
// geography. Random callers skip incompatible variants; exact callers surface
// the precise reason as an error. This prevents a request that looks valid at
// planning time from failing much later inside the renderer.
// mapMaxPinsPerGroup mirrors overlays.maxMapPinsPerGroup (one static map shows
// at most 5 grounded places; larger plates are split into consecutive items).
// The resolver enforces it so an explicit request for an overfull map fails in
// the planner instead of reaching the renderer.
const mapMaxPinsPerGroup = 5

func AdmitMapComposition(id string, input MapCompositionInput, certified []string) error {
	if input.PinCount < 1 || input.PinCount > mapMaxPinsPerGroup {
		return fmt.Errorf("map_animation %q cannot carry %d pins: one map holds 1..%d pins", id, input.PinCount, mapMaxPinsPerGroup)
	}
	allowed := make(map[string]bool, len(certified))
	for _, c := range certified {
		allowed[c] = true
	}
	if !allowed[id] {
		return fmt.Errorf("unsupported runtime map_animation %q", id)
	}
	eligibleCounts := make(map[string]bool)
	for _, c := range EligibleMapCompositions(input.PinCount, certified) {
		eligibleCounts[c] = true
	}
	if !eligibleCounts[id] {
		return fmt.Errorf("map_animation %q cannot carry %d pins", id, input.PinCount)
	}
	if err := MapCompositionPresentationCompatible(id, input); err != nil {
		return fmt.Errorf("map_animation %q is incompatible with the selected presentation: %w", id, err)
	}
	// Wire-data compatibility with the NATIVE renderer's own payload
	// validation (map_plate_render.cpp): one shared rule set with the random
	// eligibility filter (mapCompositionWireDataCompatible); exact callers
	// surface the reason as an error instead of silently skipping.
	if err := mapCompositionWireDataCompatible(id, input); err != nil {
		return fmt.Errorf("map_animation %q is incompatible with the plan's map data: %w", id, err)
	}
	// Geography is enforced only when pins are present: the classic pin-count-only
	// selection API (no pins yet) defers it to the render boundary where real
	// coordinates ride the payload. Empty pins never pass an authored scene —
	// MapCompositionGeographyCompatible returns false for them — so skip here
	// and let the full-input path fail closed instead of rejecting every
	// pin-less explicit check.
	if len(input.Pins) > 0 && !MapCompositionGeographyCompatible(id, input.Pins) {
		return fmt.Errorf("map_animation %q is incompatible with pin geography: at least one pin lies outside its authored bounds", id)
	}
	return nil
}

// ResolveMapCompositionFor resolves ONE cartographic composition against the
// planner's decision and the composition's data. Empty and "random" draw
// deterministically across the compatible families; an explicit certified id
// wins verbatim only when it passes the single admission gate (then it fails
// closed with a precise reason — never a silent swap). The second result is
// the DECLARED fallback reason recorded in the map decision report when the
// planner's strategy redirected the presentation.
func ResolveMapCompositionFor(requested, seed, slot string, input MapCompositionInput) (string, string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" && !strings.EqualFold(requested, "random") {
		if err := AdmitMapComposition(requested, input, MapCompositionCertified()); err != nil {
			return "", "", err
		}
		return requested, "", nil
	}
	pool := EligibleMapCompositionsFor(input, MapCompositionRandomPool())
	selected := DeterministicMapCompositionChoice(seed, slot, pool)
	if selected == "" {
		// Base flyover (map_animation empty): the native renderer's certified
		// default presentation. No composed family is ported yet, so the random
		// draw resolves to the base flyover — a presentation the executing
		// renderer certifies — instead of failing the map or swapping in a
		// family it cannot draw. The declared fallback records why when the
		// planner's strategy implied a composed presentation.
		fallbackReason := ""
		if strategy := strings.TrimSpace(input.Strategy); strategy != "" {
			fallbackReason = "base_flyover:" + strategy + " presentation is not ported to the native renderer yet (parity gate pending); the base flyover keeps all places"
		}
		return "", fallbackReason, nil
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
