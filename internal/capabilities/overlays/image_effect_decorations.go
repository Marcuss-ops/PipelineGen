package overlays

import (
	"strings"
)

// Image effect decorations are the producer-side projection of ChrononTemplate's
// EffectsPack (chronontemplate::effects::EffectKind, twenty procedural cinematic
// overlays). The EffectsPack authors full shape compositions in C++; this table
// maps every pack kind onto the closest NATIVE wire layer effect with subtle
// ("light") parameters, so a decoration rides renderinggen.overlay-plan.v1 as an
// ordinary item `effects` entry and lowers through RenderingGen's shared
// layer-effect resolver onto the image surface. A few pack kinds have no exact
// native equivalent (film scratches, gate weave, moving shadows, exposure
// flicker); those rows are documented approximations of the pack's look, not
// pixel-faithful ports — the naming keeps the pack's vocabulary so the mapping
// stays auditable against EffectsPack.hpp one row at a time.
type ImageEffectDecoration struct {
	// Name is the snake_case EffectsPack kind, mirroring effectName().
	Name string
	// WireType is the renderinggen.overlay-plan.v1 layer-effect type.
	WireType string
	// Params is the effect parameter object handed to the wire. The noise-like
	// decorations carry a seed placeholder filled per item by
	// ImageEffectDecorationEffect so every image gets its own deterministic
	// grain without a second selection call.
	Params map[string]any
	// Seeded marks decorations whose Params carry the per-item seed.
	Seeded bool
}

// imageEffectDecorations is the SINGLE owner of the decoration vocabulary.
// Selection callers receive copies; nothing downstream keeps a second list.
var imageEffectDecorations = []ImageEffectDecoration{
	// LightLeak — warm additive spill across the photo (glow plate look).
	{Name: "light_leak", WireType: "glow", Params: map[string]any{"radius": 170.0, "intensity": 0.22, "color": []float64{1.0, 0.72, 0.45, 0.35}}},
	// FilmGrain — animated fine RGB grain.
	{Name: "film_grain", WireType: "noise", Seeded: true, Params: map[string]any{"amount": 0.06, "size": 1.0, "animated": true, "color_mode": "rgb"}},
	// FilmScratches — fine monochrome streaking (approximation).
	{Name: "film_scratches", WireType: "fractal_noise", Seeded: true, Params: map[string]any{"amplitude": 0.06, "frequency": 0.9, "octaves": 2, "color_mode": "monochrome"}},
	// FilmBurn — hot-edge halation around highlights.
	{Name: "film_burn", WireType: "bloom", Params: map[string]any{"radius": 90.0, "intensity": 0.30, "threshold": 0.55}},
	// ScanlineInterference — thin square-wave horizontal bands.
	{Name: "scanline_interference", WireType: "wave_warp", Params: map[string]any{"amplitude": 0.012, "wavelength": 0.02, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}},
	// GateWeave — slow whole-frame weave (approximation).
	{Name: "gate_weave", WireType: "wave_warp", Params: map[string]any{"amplitude": 0.004, "wavelength": 0.9, "speed": 0.15, "direction": []float64{1, 0}, "wave_type": "triangle"}},
	// FloatingDust — sparse drifting motes (coarse animated grain).
	{Name: "floating_dust", WireType: "noise", Seeded: true, Params: map[string]any{"amount": 0.04, "size": 7.0, "animated": true, "color_mode": "monochrome"}},
	// AtmosphericHaze — barely-there diffusion.
	{Name: "atmospheric_haze", WireType: "gaussian_blur", Params: map[string]any{"radius": 1.4}},
	// DefocusedBokeh — soft defocus.
	{Name: "defocused_bokeh", WireType: "gaussian_blur", Params: map[string]any{"radius": 5.0}},
	// LightRays — warm diagonal shafts from the upper-left.
	{Name: "light_rays", WireType: "light_rays", Params: map[string]any{"origin": []float64{0.25, 0.15}, "length": 320.0, "density": 0.5, "decay": 0.92, "color": []float64{1.0, 0.95, 0.8, 1.0}}},
	// MovingLightShadow — soft off-frame darkening (approximation).
	{Name: "moving_light_shadow", WireType: "vignette", Params: map[string]any{"amount": 0.16, "radius": 0.85, "softness": 0.7, "color": []float64{0, 0, 0, 1}}},
	// LensFlare — cool glassy halo.
	{Name: "lens_flare", WireType: "glow", Params: map[string]any{"radius": 120.0, "intensity": 0.18, "color": []float64{0.85, 0.92, 1.0, 0.3}}},
	// CinematicVignette — the classic soft corner falloff.
	{Name: "cinematic_vignette", WireType: "vignette", Params: map[string]any{"amount": 0.22, "radius": 0.75, "softness": 0.6}},
	// ChromaticAberration — subtle RGB fringing.
	{Name: "chromatic_aberration", WireType: "chromatic_aberration", Params: map[string]any{"red_offset": 2.5, "blue_offset": -2.5}},
	// PaperGrain — static fine monochrome tooth.
	{Name: "paper_grain", WireType: "noise", Seeded: true, Params: map[string]any{"amount": 0.05, "size": 2.5, "animated": false, "color_mode": "monochrome"}},
	// FilmHalation — red-channel bleed around bright edges.
	{Name: "film_halation", WireType: "bloom", Params: map[string]any{"radius": 55.0, "intensity": 0.22, "threshold": 0.5}},
	// OpticalDiffusion — gentle halation blur.
	{Name: "optical_diffusion", WireType: "gaussian_blur", Params: map[string]any{"radius": 2.4}},
	// FilmEdgeMatte — tighter edge darkening.
	{Name: "film_edge_matte", WireType: "vignette", Params: map[string]any{"amount": 0.3, "radius": 0.95, "softness": 0.35}},
	// DirectionalMotionSmear — light motion streaking.
	{Name: "directional_motion_smear", WireType: "directional_blur", Params: map[string]any{"angle": 12.0, "length": 6.0, "samples": 8}},
	// ExposureFlicker — warm highlight bloom lift (approximation of the
	// pack's animated exposure wobble at its resting state).
	{Name: "exposure_flicker", WireType: "bloom", Params: map[string]any{"radius": 140.0, "intensity": 0.14, "threshold": 0.3}},
}

// ImageEffectDecorationNames returns a copy of the decoration vocabulary in
// selection order.
func ImageEffectDecorationNames() []string {
	names := make([]string, 0, len(imageEffectDecorations))
	for _, decoration := range imageEffectDecorations {
		names = append(names, decoration.Name)
	}
	return names
}

// SelectImageEffectDecorationEnabled is the per-image RANDOM YES/NO draw: the
// same deterministic seed contract as every other selection (plan identity +
// item id), so retries of a job replay the same decorations while different
// items and jobs vary. Half of the candidates say "on".
func SelectImageEffectDecorationEnabled(planID, itemID string) bool {
	return DeterministicPresetChoice(planID, "", itemID, "image_effect_toggle", []string{"on", "off"}) == "on"
}

// SelectImageEffectDecoration deterministically picks one EffectsPack-derived
// decoration for a plan/item key. Empty result means the vocabulary is empty.
func SelectImageEffectDecoration(planID, itemID string) string {
	names := ImageEffectDecorationNames()
	if len(names) == 0 {
		return ""
	}
	return DeterministicPresetChoice(planID, "", itemID, "image_effect_decoration", names)
}

// ImageEffectDecorationEffect builds the wire layer-effect object for one
// decoration, injecting the per-item seed into seeded decorations. The result
// is the exact `{"type": ..., "params": {...}}` shape RenderingGen's
// resolveLayerEffects validates and lowers.
func ImageEffectDecorationEffect(name string, seed uint32) map[string]any {
	for _, decoration := range imageEffectDecorations {
		if decoration.Name != name {
			continue
		}
		params := make(map[string]any, len(decoration.Params)+1)
		for key, value := range decoration.Params {
			params[key] = value
		}
		if decoration.Seeded {
			params["seed"] = seed
		}
		return map[string]any{"type": decoration.WireType, "params": params}
	}
	return nil
}

// IsImageOverlayItem reports whether an overlay item is a raster image surface
// (standalone image, entity image, popup, product or logo card) that can carry
// image treatments and effects. It is the canonical predicate shared by every
// image-decoration policy (SFX, runtime effects); no caller keeps a private
// copy of the kind vocabulary.
func IsImageOverlayItem(item OverlayItem) bool {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	return (kind == "image" || kind == "entity_image" || kind == "image_popup" || kind == "product" || kind == "logo" ||
		kind == "" && IsImageOverlayTemplate(item.TemplateID)) && (len(item.AssetRefs) > 0 || len(item.ImageLayers) > 0)
}

// IsImageOverlayTemplate reports whether a template id names an image overlay
// surface when the item carries no explicit kind.
func IsImageOverlayTemplate(templateID string) bool {
	switch strings.ToUpper(strings.TrimSpace(templateID)) {
	case "IMAGE_OVERLAY", "IMAGE_POPUP", "ENTITY_IMAGE", "PRODUCT", "LOGO":
		return true
	default:
		return false
	}
}
