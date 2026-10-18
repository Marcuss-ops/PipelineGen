package scriptgeneration

import (
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
)

// The composition-selection facade moved here from render_queue_map.go
// (strict 600-LOC per-file cap): the root package keeps the renderer,
// this file keeps the overlay-subpackage compatibility wrappers.

// The composition selection lives in the overlay subpackage (the sanctioned
// migration target). These thin wrappers keep the root package as the
// compatibility facade for existing callers and tests.

type mapCompositionInput = scriptoverlay.MapCompositionInput

const (
	MapFamilyLocationFocus = scriptoverlay.MapFamilyLocationFocus
	MapFamilyMultiPin      = scriptoverlay.MapFamilyMultiPin
	MapFamilyJourney       = scriptoverlay.MapFamilyJourney
	MapFamilyRegion        = scriptoverlay.MapFamilyRegion
	MapFamilyComparison    = scriptoverlay.MapFamilyComparison
	MapFamilyEditorial     = scriptoverlay.MapFamilyEditorial
)

func mapCompositionFamily(id string) scriptoverlay.MapCompositionFamily {
	return scriptoverlay.MapCompositionFamilyOf(id)
}

func eligibleMapCompositionsFor(input mapCompositionInput, certified []string) []string {
	return scriptoverlay.EligibleMapCompositionsFor(input, certified)
}

func deterministicMapCompositionChoice(seed, slot string, pool []string) string {
	return scriptoverlay.DeterministicMapCompositionChoice(seed, slot, pool)
}

func resolveMapCompositionFor(requested, seed, slot string, input mapCompositionInput) (string, string, error) {
	return scriptoverlay.ResolveMapCompositionFor(requested, seed, slot, input)
}

// resolveMapComposition is the classic selection API retained for callers and
// tests that hold only the pin count.
func resolveMapComposition(requested, seed, slot string, pinCounts ...int) (string, error) {
	input := mapCompositionInput{}
	if len(pinCounts) > 0 {
		input.PinCount = pinCounts[0]
	}
	selected, _, err := resolveMapCompositionFor(requested, seed, slot, input)
	return selected, err
}

// eligibleMapCompositions removes only renderer-documented multi-pin systems
// that cannot run for this map's grounded pin count. Explicit selections still
// validate against the full certified catalog and fail closed in the renderer.
func eligibleMapCompositions(pinCount int, certified []string) []string {
	return scriptoverlay.EligibleMapCompositions(pinCount, certified)
}

// mapMapAnimationsCertified lists every renderer-approved map_animation id:
// map systems from map_compositions plus the GPU-certified editorial
// scenes in map_editorial.MAP_EDITORIAL_SCENES.
// TestMapCompositionsMatchPython guards cross-repository drift.
func mapMapAnimationsCertified() []string {
	return capoverlay.AnimationCandidates("map.composition")
}

// mapLabelAnimationsCertified lists every label id the Python renderer
// accepts, including explicit-only choices.
func mapLabelAnimationsCertified() []string {
	return capoverlay.AnimationCandidates("map.label")
}

// mapRunSeed is the stable per-run seed for a map's presentation choices. The
// plan fingerprint is the canonical variant knob — a new fingerprint IS a new
// variant — with PlanID/VideoID as fallbacks for a plan stamped before the
// fingerprint was computed. It never reads wall-clock or process state, so the
// same run always resolves the same map presentation.
func mapRunSeed(plan capoverlay.OverlayPlan) string {
	// Child render plans get their own fingerprint and ID. Prefer the parent
	// job identity so a run-level style choice remains the same across all map
	// clips belonging to that video.
	for _, candidate := range []string{plan.ResultJobID, plan.DriveJobID, plan.Fingerprint, plan.PlanID, plan.VideoID} {
		if seed := strings.TrimSpace(candidate); seed != "" {
			return seed
		}
	}
	return "map"
}

// deterministicMapStyle resolves ONE presentation value from options as a pure
// function of the run seed and the style channel. It samples through the
// overlays package's single deterministic sampler, so map presentation obeys
// exactly the same seed→choice contract as every other semantic selection in
// the pipeline. The channel keeps the camera move and basemap independent:
// the same seed picks them from separate digests.
func deterministicMapStyle(seed, channel, itemID string, options []string) string {
	return capoverlay.DefaultDeterministicPresetSampler.Sample(capoverlay.PresetSampleInput{
		JobFingerprint: seed,
		SceneID:        "map",
		SemanticID:     itemID,
		PresetFamily:   channel,
		Presets:        options,
	}).Preset
}
