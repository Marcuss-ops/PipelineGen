package overlays

// The installed REQUIRE_GPU_NATIVE text lane accepts exactly one shape of
// generated text overlay, and it is fixed by the engine, not by taste. Both
// rules below were measured on the installed chronon3d_cli (vulkan backend,
// gpu-hot-path-mode=require_gpu_native, nvenc), one variable at a time:
//
//  1. GLOW IS NOT RENDERABLE ON THIS LANE. A text layer that carries
//     style.glow cannot stay resident on the GPU: the halo effect stack needs
//     a CPU pixel-backed source, so the frame dies in the composite
//     ("[native-surface] cannot upload from framebuffer without CPU pixel
//     backing" / "native residency violation") even when the layer itself is
//     static. The canonical apple_v2 phrase preset authors canaryGlow(), so
//     every generated phrase/name/word built from it is unrenderable here.
//
//  2. PER-GLYPH/PER-WORD TEXT ANIMATORS ARE REJECTED. The native MTSDF text
//     kernel only accepts a lowerable animator stack; the glyph/word selector
//     family resolves to route=reject reason=unsupported_animation
//     ("animated glyph state requires software path"), which is fail-closed
//     and — with a glow present — crashes the render process. The motions the
//     planner used to rotate over (kinetic_split_word, masked_upward_reveal,
//     staggered_char_float, soft_edge_spotlight_dissolve,
//     velocity_inertia_snap) are exactly that family, and so is the apple_v2
//     preset's own apple_phrase_v2 motion.
//
// What DOES render is the glow-free text preset certified on this lane, with a
// motion that lowers to COMPOSITION-level tracks only: the whole layer fades /
// slides / scales in, the shaped text run stays static, and the native kernel
// accepts it. Every generated text item therefore carries an explicit motion
// id from renderSafeTextMotions — the preset's own motion is bypassed by the
// compiler (semantic_compile: MotionID wins over PresetID), so no text
// animator is ever transported for a generated overlay.
//
// These ids are owned by Chronon's VisualPresetRegistry/catalog. PipelineGen
// only selects deterministically and transports the opaque id downstream.
var (
	// static_text_smoke is the ONLY glow-free, motion-free text preset the
	// installed RenderingGen catalog declares, and it is the preset the
	// certified runtime overlay scenario renders its phrases with. It owns the
	// name, phrase and word surfaces so there is a single render-safe owner and
	// no second candidate list can drift off the certified lane.
	namePresetRenderSafeCandidates = []string{"static_text_smoke"}
	phrasePresetCandidates         = []string{"static_text_smoke"}
	wordPresetCandidates           = []string{"static_text_smoke"}
	imagePresetCandidates          = []string{
		// Auto-selected images use an entrance long enough to remain visible.
		// image_fast_fade is an explicit short variant (<1.5 s), so it stays
		// available for editorial plans but is not picked for generated overlays.
		"image_focus_in", "image_fade_in", "image_scale_in",
		"image_slide_left", "image_slide_right",
		// modern_rounded_pop adds rounded-corner masking. The current strict
		// Vulkan/NVENC image path rejects that mask and has no legacy fallback;
		// keep the preset available for explicit certification plans, but never
		// select it for generated overlays until the native mask path lands.
		"bottom_card_rise",
	}
	imageAnimationCandidates = []string{
		"fade_in", "reveal_from_bottom", "scale_drop", "fade_shift_vertical",
	}
	// renderSafeTextMotions is the whole motion vocabulary a generated text
	// overlay may use on this lane. Every id is a catalog motion that lowers to
	// composition tracks ONLY (no text_animators), so it renders natively
	// without a soft-* or an animator stack: fade_in (opacity), slide_up and
	// precision_spring_up (position_y), slide_from_right (position_x), scale_in
	// (scale) and soft_scale_reveal (scale + opacity). They carry distinct,
	// visible entrances and the layer owns its exit fade.
	renderSafeTextMotions = []string{
		"fade_in", "slide_up", "slide_from_right",
		"scale_in", "soft_scale_reveal", "precision_spring_up",
	}
	// phraseMotionCandidates is the run-wide rotation pool for IMPORTANT_PHRASE
	// overlays. It is the render-safe vocabulary above, nothing else.
	phraseMotionCandidates = renderSafeTextMotions
)

// ImagePresetCandidates returns a copy of the render-safe generated-image
// preset ids in selection order. It is the read-only projection of the SINGLE
// owner of that list: a consumer (including a structural test) must never keep
// a second copy that can drift from the ids the planner can actually emit.
func ImagePresetCandidates() []string {
	return append([]string(nil), imagePresetCandidates...)
}

func selectPreset(jobID, sceneID, itemID, family string, candidates []string) string {
	return DefaultDeterministicPresetSampler.Sample(PresetSampleInput{
		JobFingerprint: jobID,
		SceneID:        sceneID,
		SemanticID:     itemID,
		PresetFamily:   family,
		Presets:        candidates,
	}).Preset
}

// SelectEntityNamePreset chooses a stable-but-varied name treatment for one
// entity occurrence. A new job fingerprint can select another treatment,
// while retries of the same job remain bit-identical.
func SelectEntityNamePreset(jobID, sceneID, itemID, entityType string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_name:"+entityType, namePresetRenderSafeCandidates)
}

func selectPhrasePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_phrase", phrasePresetCandidates)
}

func selectPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	candidates := phraseMotionCandidates
	if len(pool) > 0 {
		candidates = pool
	}
	if len(candidates) == 0 {
		return ""
	}
	// The caller supplies one run-wide ordinal after editorial ranking and
	// dedupe. Hash selection varies the first effect by job; rotating the
	// certified catalog then guarantees distinct motions across the admitted
	// phrase set.
	seeded := DefaultDeterministicPresetSampler.Sample(PresetSampleInput{
		JobFingerprint: jobID,
		SceneID:        sceneID,
		// The caller passes the stable run sentinel here. Per-scene seeds would
		// choose different starting points and could reintroduce collisions.
		SemanticID:   sceneID,
		PresetFamily: "important_phrase_motion",
		Presets:      candidates,
	}).Preset
	start := 0
	for i, candidate := range candidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return candidates[(start+ordinal)%len(candidates)]
}

// CertifiedPhraseMotions returns the certified render-safe motion pool this
// build rotates over. It is the membership authority a caller-supplied pool is
// validated against (see PlanInput.PhraseMotions): every id is a catalog motion
// that lowers to composition tracks only, so an id outside this list either
// needs a text-animator stack or a glow the native text lane rejects — it
// cannot render here.
//
// The returned slice is a copy — callers may keep it without pinning the
// package's own storage.
func CertifiedPhraseMotions() []string {
	return append([]string(nil), phraseMotionCandidates...)
}

// RenderSafeTextMotions returns the render-safe text-motion vocabulary. It is
// the read-only projection of the single owner of that list (the rotation pool
// itself), so a consumer — including a structural test — never keeps a second
// copy that could drift from what the planner can actually emit.
func RenderSafeTextMotions() []string {
	return append([]string(nil), renderSafeTextMotions...)
}

// SelectTextMotion chooses the entrance motion of one generated TEXT item
// (entity name card, quote, important word, number, keyword). The item's
// preset alone would fall back to the preset's own motion — which on the
// installed catalog is the glyph-level apple_phrase_v2, i.e. exactly the
// animator stack this lane rejects — so every generated text item states its
// motion explicitly. A new job fingerprint can select another treatment while
// retries of the same job stay bit-identical.
func SelectTextMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "text_motion", renderSafeTextMotions)
}

func selectWordPreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_word", wordPresetCandidates)
}

func selectImagePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image", imagePresetCandidates)
}

// SelectEntityImagePreset chooses the image motion independently from the
// entity name treatment. Both choices are made by the shared sampler so the
// renderer never has to infer or invent a preset.
func SelectEntityImagePreset(jobID, sceneID, itemID string) string {
	return selectImagePreset(jobID, sceneID, itemID)
}

// SelectEntityImageAnimation chooses a stable-but-varied Chronon entry
// animation for an entity image. Retries of the same job remain bit-identical.
func SelectEntityImageAnimation(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image_animation", imageAnimationCandidates)
}

// SelectImageAnimation is the generic image/product/logo planner selector.
func SelectImageAnimation(jobID, sceneID, itemID string) string {
	return SelectEntityImageAnimation(jobID, sceneID, itemID)
}
